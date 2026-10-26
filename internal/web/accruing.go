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

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
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

func formValueAny(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := r.FormValue(name); strings.TrimSpace(value) != "" {
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
		Workspace: workspace, Currency: currency, Name: "Revolut Cuenta Remunerada",
		AnchorDate: s.now().UTC().Format("2006-01-02"), APYPercent: "3.10",
		TransferMatchKeyword:  "Cuenta Remunerada",
		TaxWithholdingPercent: "0", CompoundingFrequency: domain.AccruingDaily,
	}
	s.renderPartialData(w, "accruing_account_form", form)
}

func (s *Server) handleAccruingAccountEdit(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	account, err := s.store.GetAccruingAccount(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderPartialData(w, "accruing_account_form", accruingFormFromAccount(account))
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
		CompoundingFrequency:  account.CompoundingFrequency, Editing: true,
	}
}

func newAccruingAccountID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "accruing-" + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("accruing-%d", time.Now().UTC().UnixNano())
}
