package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
	"github.com/RedRiotTank/Fiscus/pkg/categorizer"
)

func (s *Server) listAccruingAccounts(ctx context.Context, workspace string) ([]domain.AccruingAccount, error) {
	accounts, err := s.store.ListAccruingAccounts(ctx)
	if err != nil {
		return nil, err
	}
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	out := make([]domain.AccruingAccount, 0, len(accounts))
	for _, account := range accounts {
		if !account.IsActive {
			continue
		}
		if workspace == domain.WorkspaceAll || workspace == "" ||
			strings.EqualFold(account.WorkspaceID, workspace) {
			out = append(out, account)
		}
	}
	return out, nil
}

func (s *Server) accruingAccountViews(ctx context.Context, workspace string, synced []domain.Account, now time.Time) ([]AccruingAccountView, error) {
	accounts, err := s.listAccruingAccounts(ctx, workspace)
	if err != nil || len(accounts) == 0 {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(synced))
	for _, account := range synced {
		allowed[account.ID] = struct{}{}
	}
	earliest := accounts[0].AnchorDate
	for _, account := range accounts[1:] {
		if account.AnchorDate.Before(earliest) {
			earliest = account.AnchorDate
		}
	}
	transactions, err := s.store.ListTransactionsBetween(ctx, earliest, now.UTC().Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	filtered := make([]domain.Transaction, 0, len(transactions))
	for _, tx := range transactions {
		if _, ok := allowed[tx.AccountID]; ok {
			filtered = append(filtered, tx)
		}
	}
	out := make([]AccruingAccountView, 0, len(accounts))
	for _, account := range accounts {
		transfers, transferErr := accrual.TransfersFromTransactions(account, filtered)
		if transferErr != nil {
			transfers = nil
		}
		report := accrual.Project(account, transfers, now)
		out = append(out, AccruingAccountView{
			AccruingAccount:       account,
			BalanceMinor:          report.BalanceMinor,
			LifetimeInterestMinor: report.LifetimeInterestMinor,
			MonthInterestMinor:    report.MonthInterestMinor,
			TAELabel: fmt.Sprintf("%.2f%% TAE · %s",
				float64(account.APYRateBasisPoints)/100, frequencyLabel(account.CompoundingFrequency)),
		})
	}
	return out, nil
}

func frequencyLabel(frequency string) string {
	if strings.EqualFold(strings.TrimSpace(frequency), domain.AccruingMonthly) {
		return "Monthly"
	}
	return "Daily"
}

func (s *Server) savingsCategoryOptions(ctx context.Context, workspace, currency string) []string {
	seen := map[string]struct{}{
		budgets.CategoryAccruingSavings: {},
		"Emergency Fund":                {},
		"Investments":                   {},
	}
	allocations, err := s.workspaceBudgetAllocations(ctx, workspace, currency)
	if err == nil {
		for _, allocation := range allocations {
			if allocation.Pillar == budgets.PillarSavings {
				category := strings.TrimSpace(allocation.Category)
				if category != "" {
					seen[category] = struct{}{}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for category := range seen {
		out = append(out, category)
	}
	sort.Strings(out)
	return out
}

func ensureCategoryOption(options []string, category string) []string {
	category = strings.TrimSpace(category)
	if category == "" {
		return options
	}
	for _, option := range options {
		if strings.EqualFold(option, category) {
			return options
		}
	}
	options = append(options, category)
	sort.Strings(options)
	return options
}

func needsGenericAccruingSavingsBar(accounts []AccruingAccountView) bool {
	for _, account := range accounts {
		category := strings.TrimSpace(account.BudgetCategory)
		if category == "" || strings.EqualFold(category, budgets.CategoryAccruingSavings) {
			return true
		}
	}
	return false
}

func formValueAny(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := r.FormValue(name); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstQueryValue(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func normalizeMoneyInput(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.NewReplacer("€", "", "$", "", "£", "", " ", "").Replace(raw)
	lastComma := strings.LastIndex(raw, ",")
	lastDot := strings.LastIndex(raw, ".")
	if lastComma > lastDot {
		raw = strings.ReplaceAll(raw, ".", "")
		raw = strings.ReplaceAll(raw, ",", ".")
	} else {
		raw = strings.ReplaceAll(raw, ",", "")
	}
	return raw
}

func summarizeAccountsAndAccruing(accounts []domain.Account, accruing []AccruingAccountView) []CurrencySummary {
	summary := summarizeAccounts(accounts)
	index := make(map[string]int, len(summary))
	for i, item := range summary {
		index[item.Currency] = i
	}
	for _, account := range accruing {
		currency := strings.ToUpper(strings.TrimSpace(account.Currency))
		if currency == "" {
			currency = "EUR"
		}
		if position, ok := index[currency]; ok {
			summary[position].Minor += account.BalanceMinor
			summary[position].Accounts++
		} else {
			index[currency] = len(summary)
			summary = append(summary, CurrencySummary{Currency: currency, Minor: account.BalanceMinor, Accounts: 1})
		}
	}
	sort.Slice(summary, func(i, j int) bool { return summary[i].Currency < summary[j].Currency })
	return summary
}

func (s *Server) detectSavingsPrompt(ctx context.Context, workspace string, synced []domain.Account, accruing []AccruingAccountView, now time.Time) (*SavingsPromptView, error) {
	allowed := make(map[string]domain.Account, len(synced))
	hints := make([]string, 0, len(synced)*2)
	for _, account := range synced {
		allowed[account.ID] = account
		for _, value := range []string{account.Name, account.IBAN, account.ID} {
			value = strings.ToLower(strings.TrimSpace(value))
			if len(value) >= 3 {
				hints = append(hints, value)
			}
		}
	}
	transactions, err := s.store.ListTransactionsBetween(ctx, now.AddDate(-1, 0, 0), now.Add(24*time.Hour))
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		target   string
		amount   int64
		count    int
		currency string
		earliest time.Time
	}
	groups := make(map[string]aggregate)
	for _, tx := range transactions {
		account, ok := allowed[tx.AccountID]
		if !ok || tx.Status != domain.TransactionStatusBooked || tx.AmountMinor >= 0 {
			continue
		}
		if domain.NormalizeAccountKind(account.Kind) != domain.AccountKindChecking {
			continue
		}
		text := accrual.NormalizeText(tx.Counterparty + " " + tx.Description)
		match, ok := categorizer.SavingsMatch(text)
		if !ok {
			continue
		}
		target := normalizedSavingsTarget(match)
		if target == "" {
			continue
		}
		configured := false
		for _, existing := range accruing {
			existingName := strings.ToLower(strings.TrimSpace(existing.Name))
			targetName := strings.ToLower(target)
			if existingName != "" && (strings.Contains(existingName, targetName) || strings.Contains(targetName, existingName)) {
				configured = true
				break
			}
			re, compileErr := accrual.CompilePattern(existing.TransferMatchPattern)
			if compileErr == nil && re != nil && re.MatchString(text) {
				configured = true
				break
			}
		}
		if configured {
			continue
		}
		for _, hint := range hints {
			if strings.Contains(strings.ToLower(text), hint) || strings.EqualFold(target, hint) {
				configured = true
				break
			}
		}
		if configured {
			continue
		}
		key := strings.ToLower(target) + "\x00" + strings.ToUpper(strings.TrimSpace(tx.Currency))
		current := groups[key]
		if current.target == "" {
			current.target = target
			current.currency = strings.ToUpper(strings.TrimSpace(tx.Currency))
			current.earliest = tx.BookingDate
		}
		current.amount += -tx.AmountMinor
		current.count++
		if tx.BookingDate.Before(current.earliest) {
			current.earliest = tx.BookingDate
		}
		groups[key] = current
	}
	if len(groups) == 0 {
		return nil, nil
	}
	best := aggregate{}
	for _, group := range groups {
		if group.amount > best.amount || (group.amount == best.amount && group.target < best.target) {
			best = group
		}
	}
	if best.target == "" {
		return nil, nil
	}
	if best.currency == "" {
		best.currency = "EUR"
	}
	promptWorkspace := workspace
	if promptWorkspace != domain.WorkspaceBusiness {
		promptWorkspace = domain.WorkspacePersonal
	}
	return &SavingsPromptView{
		Target: best.target, AmountMinor: best.amount, Count: best.count,
		Currency: best.currency, Workspace: promptWorkspace,
		AnchorDate: now.UTC().Format("2006-01-02"), MatchKeyword: best.target,
	}, nil
}

func normalizedSavingsTarget(value string) string {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	for index, field := range fields {
		runes := []rune(strings.ToLower(field))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		fields[index] = string(runes)
	}
	return strings.Join(fields, " ")
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Accounts", "accounts")
	ctx := r.Context()
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Accounts = accounts
	views, err := s.accruingAccountViews(ctx, data.Workspace.View, accounts, s.now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.AccruingAccounts = views
	data.Summary = summarizeAccountsAndAccruing(accounts, views)
	data.TotalAccounts = len(accounts) + len(views)
	prompt, err := s.detectSavingsPrompt(ctx, data.Workspace.View, accounts, views, s.now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.SavingsPrompt = prompt
	if err := s.loadReconciliations(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "accounts", data)
}

func (s *Server) handleAccruingAccountNew(w http.ResponseWriter, r *http.Request) {
	workspace := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("workspace")))
	if workspace != domain.WorkspaceBusiness {
		workspace = domain.WorkspacePersonal
	}
	currency := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	if currency == "" {
		accounts, err := s.workspaceAccounts(r.Context(), workspace)
		if err == nil {
			currency = budgetCurrency(accounts)
		}
	}
	if currency == "" {
		currency = "EUR"
	}
	form := AccruingAccountFormView{
		Workspace: workspace, Currency: currency, Name: "Revolut Savings Account",
		AnchorDate: s.now().UTC().Format("2006-01-02"), APYPercent: "3.10",
		TransferMatchKeyword:  "Cuenta Remunerada",
		TaxWithholdingPercent: "0", CompoundingFrequency: domain.AccruingDaily,
	}
	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		form.Name = name
	}
	if balance := firstQueryValue(r, "anchor_balance", "opening_balance", "balance"); balance != "" {
		form.AnchorBalance = balance
	}
	if anchorDate := firstQueryValue(r, "anchor_date", "date"); anchorDate != "" {
		form.AnchorDate = anchorDate
	}
	if keyword := firstQueryValue(r, "transfer_match_keyword", "keyword", "transfer_match_pattern"); keyword != "" {
		form.TransferMatchKeyword = keyword
	}
	if apy := firstQueryValue(r, "apy_percent", "apy", "tae"); apy != "" {
		form.APYPercent = apy
	}
	if category := firstQueryValue(r, "budget_category", "savings_category"); category != "" {
		form.BudgetCategory = category
	}
	if form.BudgetCategory == "" {
		form.BudgetCategory = budgets.CategoryAccruingSavings
	}
	form.CategoryOptions = ensureCategoryOption(s.savingsCategoryOptions(r.Context(), workspace, currency), form.BudgetCategory)
	s.renderPartialData(w, "accruing_account_form", form)
}

func (s *Server) handleAccruingAccountEdit(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	account, err := s.store.GetAccruingAccount(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	form := accruingFormFromAccount(account)
	form.CategoryOptions = ensureCategoryOption(s.savingsCategoryOptions(r.Context(), account.WorkspaceID, account.Currency), form.BudgetCategory)
	s.renderPartialData(w, "accruing_account_form", form)
}

func (s *Server) handleAccruingAccountDelete(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteAccruingAccount(r.Context(), id); err != nil {
		s.logger.Error("delete accruing account", "accruing_account_id", id, "error", err)
		http.Error(w, "Could not delete savings account", http.StatusInternalServerError)
		return
	}
	if _, err := s.store.LinkAccruingTransfers(r.Context()); err != nil {
		s.logger.Error("relink transfers after savings account deletion", "error", err)
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

func (s *Server) handleAccruingAccountSave(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	form := AccruingAccountFormView{
		ID:                    strings.TrimSpace(formValueAny(r, "id")),
		Workspace:             strings.ToLower(strings.TrimSpace(formValueAny(r, "workspace", "workspace_id"))),
		Name:                  strings.TrimSpace(formValueAny(r, "name")),
		Currency:              strings.ToUpper(strings.TrimSpace(formValueAny(r, "currency"))),
		AnchorBalance:         strings.TrimSpace(formValueAny(r, "anchor_balance", "opening_balance")),
		AnchorDate:            strings.TrimSpace(formValueAny(r, "anchor_date", "date")),
		APYPercent:            strings.TrimSpace(formValueAny(r, "apy_percent", "apy", "tae_percent", "tae")),
		TransferMatchKeyword:  strings.TrimSpace(formValueAny(r, "transfer_match_keyword", "transfer_match_pattern", "keyword")),
		TaxWithholdingPercent: strings.TrimSpace(formValueAny(r, "tax_withholding_percent", "tax_withholding", "tax_rate")),
		CompoundingFrequency:  strings.ToLower(strings.TrimSpace(formValueAny(r, "compounding_frequency", "frequency"))),
		BudgetCategory:        strings.TrimSpace(formValueAny(r, "budget_category", "savings_category")),
	}
	if form.ID == "" {
		form.ID = strings.TrimSpace(r.PathValue("id"))
	}
	form.Editing = form.ID != ""
	var existing domain.AccruingAccount
	if form.Editing {
		loaded, err := s.store.GetAccruingAccount(r.Context(), form.ID)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			http.NotFound(w, r)
			return
		case err != nil:
			s.logger.Error("load accruing account for update", "error", err)
			http.Error(w, "Could not load savings account", http.StatusInternalServerError)
			return
		}
		existing = loaded
		if form.Name == "" {
			form.Name = existing.Name
		}
		if form.Workspace == "" {
			form.Workspace = existing.WorkspaceID
		}
		if form.Currency == "" {
			form.Currency = existing.Currency
		}
		if form.AnchorBalance == "" {
			form.AnchorBalance = money.Format(existing.AnchorBalanceCents, existing.Currency)
		}
		if form.AnchorDate == "" {
			form.AnchorDate = existing.AnchorDate.UTC().Format("2006-01-02")
		}
		if form.APYPercent == "" {
			form.APYPercent = bpsInput(existing.APYRateBasisPoints)
		}
		if form.TransferMatchKeyword == "" {
			form.TransferMatchKeyword = existing.TransferMatchPattern
		}
		if form.TaxWithholdingPercent == "" {
			form.TaxWithholdingPercent = bpsInput(existing.TaxWithholdingBPS)
		}
		if form.CompoundingFrequency == "" {
			form.CompoundingFrequency = existing.CompoundingFrequency
		}
		if form.BudgetCategory == "" {
			form.BudgetCategory = existing.BudgetCategory
		}
	}
	if form.Workspace != domain.WorkspaceBusiness {
		form.Workspace = domain.WorkspacePersonal
	}
	if form.Currency == "" {
		form.Currency = "EUR"
	}
	if form.CompoundingFrequency != domain.AccruingMonthly {
		form.CompoundingFrequency = domain.AccruingDaily
	}
	if form.BudgetCategory == "" {
		form.BudgetCategory = budgets.CategoryAccruingSavings
	}
	form.CategoryOptions = ensureCategoryOption(s.savingsCategoryOptions(r.Context(), form.Workspace, form.Currency), form.BudgetCategory)

	account, parseErr := accruingAccountFromForm(form, s.now().UTC())
	if parseErr != nil {
		form.Error = parseErr.Error()
		s.renderPartialData(w, "accruing_account_form", form)
		return
	}
	if form.Editing {
		account.CreatedAt = existing.CreatedAt
	} else {
		account.CreatedAt = time.Time{}
	}
	account.IsActive = true
	if err := s.store.SaveAccruingAccount(r.Context(), &account); err != nil {
		form.Error = err.Error()
		s.renderPartialData(w, "accruing_account_form", form)
		return
	}
	if _, err := s.store.LinkAccruingTransfers(r.Context()); err != nil {
		s.logger.Error("link accruing transfers after save", "error", err)
	}
	target := "/accounts?workspace=" + account.WorkspaceID
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func accruingAccountFromForm(form AccruingAccountFormView, now time.Time) (domain.AccruingAccount, error) {
	if form.Name == "" {
		return domain.AccruingAccount{}, fmt.Errorf("account name is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(form.Currency))
	if currency == "" {
		currency = "EUR"
	}
	balance, err := money.Parse(normalizeMoneyInput(form.AnchorBalance), currency)
	if err != nil {
		return domain.AccruingAccount{}, fmt.Errorf("opening balance: %w", err)
	}
	anchorDate := now.UTC()
	if form.AnchorDate != "" {
		parsed, parseErr := time.Parse("2006-01-02", form.AnchorDate)
		if parseErr != nil {
			return domain.AccruingAccount{}, fmt.Errorf("anchor date must be YYYY-MM-DD")
		}
		anchorDate = parsed
	}
	apyRaw := strings.TrimSpace(form.APYPercent)
	if apyRaw == "" {
		apyRaw = "0"
	}
	apy, err := parseBPS(apyRaw)
	if err != nil {
		return domain.AccruingAccount{}, fmt.Errorf("APY / TAE: %w", err)
	}
	tax := 0
	if strings.TrimSpace(form.TaxWithholdingPercent) != "" {
		tax, err = parseBPS(form.TaxWithholdingPercent)
		if err != nil {
			return domain.AccruingAccount{}, fmt.Errorf("IRPF retention: %w", err)
		}
	}
	pattern, err := accrual.KeywordPattern(form.TransferMatchKeyword)
	if err != nil {
		return domain.AccruingAccount{}, err
	}
	id := strings.TrimSpace(form.ID)
	if id == "" {
		id = newAccruingAccountID()
	}
	frequency := strings.ToLower(strings.TrimSpace(form.CompoundingFrequency))
	if frequency != domain.AccruingMonthly {
		frequency = domain.AccruingDaily
	}
	return domain.AccruingAccount{
		ID:                   id,
		WorkspaceID:          form.Workspace,
		Name:                 form.Name,
		Currency:             currency,
		AnchorBalanceCents:   balance,
		AnchorDate:           anchorDate,
		APYRateBasisPoints:   apy,
		CompoundingFrequency: frequency,
		TaxWithholdingBPS:    tax,
		TransferMatchPattern: pattern,
		BudgetCategory:       strings.TrimSpace(form.BudgetCategory),
		IsActive:             true,
	}, nil
}

func accruingFormFromAccount(account domain.AccruingAccount) AccruingAccountFormView {
	return AccruingAccountFormView{
		ID: account.ID, Workspace: account.WorkspaceID, Name: account.Name,
		Currency: account.Currency, AnchorBalance: money.Format(account.AnchorBalanceCents, account.Currency),
		AnchorDate:            account.AnchorDate.UTC().Format("2006-01-02"),
		APYPercent:            bpsInput(account.APYRateBasisPoints),
		TransferMatchKeyword:  account.TransferMatchPattern,
		TaxWithholdingPercent: bpsInput(account.TaxWithholdingBPS),
		CompoundingFrequency:  account.CompoundingFrequency,
		BudgetCategory:        account.BudgetCategory, Editing: true,
	}
}

func newAccruingAccountID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "accruing-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("accruing-%d", time.Now().UTC().UnixNano())
}
