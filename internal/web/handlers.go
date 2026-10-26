package web

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/internal/tax"
	"github.com/RedRiotTank/Fiscus/internal/treasury"
)

const (
	syncRequestHeader       = "X-Fiscus-Request"
	defaultTransactionLimit = 50
	maxTransactionLimit     = 200
)

func (s *Server) baseData(r *http.Request, title, active string) PageData {
	path := "/"
	if r != nil && r.URL != nil && r.URL.Path != "" {
		path = r.URL.Path
	}
	return PageData{
		Title:     title,
		Active:    active,
		Version:   s.version,
		Now:       s.now().UTC(),
		Workspace: s.resolveWorkspace(r),
		Path:      path,
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Overview", "dashboard")
	ctx := r.Context()
	if err := s.loadOverview(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.loadAlerts(ctx, &data, 12); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.loadConnections(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	transactions, err := s.store.RecentTransactions(ctx, 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(data.Accounts, data.Workspace.View), data.Workspace.View)
	data.Transactions = transactionViews(transactions, accountMap(data.Accounts))
	s.enrichPassThroughViews(ctx, data.Transactions)
	s.render(w, "dashboard", data)
}

func (s *Server) handleTransactions(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Transactions", "transactions")
	data.FilterAccount = strings.TrimSpace(r.URL.Query().Get("account"))
	data.SearchQuery = strings.TrimSpace(r.URL.Query().Get("q"))
	data.Page = parsePage(r.URL.Query().Get("page"))
	data.Limit = parseLimit(r.URL.Query().Get("limit"))
	query := r.URL.Query()
	data.FlowFacet = string(storage.NormalizeFlowFacet(query.Get("flow")))
	data.CategoryFacet = string(storage.NormalizeCategoryFacet(query.Get("facet")))
	data.FilterCategory = strings.TrimSpace(query.Get("category"))
	var monthFrom, monthTo time.Time
	if raw := strings.TrimSpace(query.Get("month")); raw != "" {
		if parsed, ok := parseMonthStart(raw); ok {
			monthFrom = parsed
			monthTo = parsed.AddDate(0, 1, 0)
			data.TransactionMonth = parsed.Format("2006-01")
			data.TransactionMonthLabel = parsed.Format("January 2006")
			data.TransactionMonthCurrent = parsed.Equal(firstOfMonth(s.now().UTC()))
		}
	}
	if legacy := strings.TrimSpace(query.Get("filter")); legacy != "" {
		legacyFlow, legacyCategory := storage.NormalizeLegacyFilter(legacy)
		if strings.TrimSpace(query.Get("flow")) == "" {
			data.FlowFacet = string(legacyFlow)
		}
		if strings.TrimSpace(query.Get("facet")) == "" {
			data.CategoryFacet = string(legacyCategory)
		}
	}
	data.TaxEnabled = s.tax != nil

	ctx := r.Context()
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Accounts = accounts
	data.TotalAccounts = len(accounts)
	allowed := allowedAccountIDs(accounts, data.Workspace.View)
	filterBlocked := false
	if data.FilterAccount != "" {
		if _, ok := allowed[data.FilterAccount]; !ok {
			filterBlocked = true
		}
	}

	currency := budgetCurrency(accounts)
	var facetCategories []string
	if data.CategoryFacet == string(storage.CategoryFacetNeeds) || data.CategoryFacet == string(storage.CategoryFacetWants) {
		facetCategories, err = s.facetCategories(ctx, data.Workspace.View, currency, data.CategoryFacet)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}

	accountIDs := accountIDsArg(accounts, data.Workspace.View)
	restrictedEmpty := filterBlocked || (data.Workspace.View != domain.WorkspaceAll && len(accounts) == 0)
	searchMode := data.SearchQuery != "" && s.search != nil
	var transactions []domain.Transaction
	if !restrictedEmpty {
		if searchMode {
			transactions, err = s.search.SearchPage(ctx, data.SearchQuery, data.Limit, (data.Page-1)*data.Limit)
			if err != nil {
				data.ErrorMessage = err.Error()
				transactions = nil
			} else {
				transactions = filterTransactions(transactions, allowed, data.Workspace.View)
				if data.FilterAccount != "" {
					filtered := transactions[:0]
					for _, tx := range transactions {
						if tx.AccountID == data.FilterAccount {
							filtered = append(filtered, tx)
						}
					}
					transactions = filtered
				}
				if data.FlowFacet != string(storage.FlowAll) ||
					data.CategoryFacet != string(storage.CategoryFacetAll) ||
					data.FilterCategory != "" || !monthFrom.IsZero() {
					filtered := transactions[:0]
					for _, tx := range transactions {
						if matchesTransactionFacets(tx, data.FlowFacet, data.CategoryFacet, facetCategories) &&
							matchesTransactionPeriod(tx, monthFrom, monthTo, data.FilterCategory) {
							filtered = append(filtered, tx)
						}
					}
					transactions = filtered
				}
			}
		} else {
			filter := transactionFilter(data.FilterAccount, accountIDs, data.Page, data.Limit)
			filter.Flow = storage.NormalizeFlowFacet(data.FlowFacet)
			filter.Category = storage.NormalizeCategoryFacet(data.CategoryFacet)
			filter.Categories = facetCategories
			filter.CategoryName = data.FilterCategory
			filter.MonthFrom = monthFrom
			filter.MonthTo = monthTo
			transactions, err = s.store.ListTransactions(ctx, filter)
			if err != nil {
				s.fail(w, r, err)
				return
			}
		}
	}
	data.Transactions = transactionViews(transactions, accountMap(accounts))
	s.enrichPassThroughViews(ctx, data.Transactions)
	categoriesSeen := make([]string, 0, len(data.Transactions))
	for _, transaction := range data.Transactions {
		categoriesSeen = append(categoriesSeen, transaction.Category)
	}
	data.Categories = s.categoryOptions(categoriesSeen...)
	data.HasNext = len(transactions) == data.Limit
	data.PrevPage = max(data.Page-1, 1)
	data.NextPage = data.Page + 1
	var total int
	if !restrictedEmpty {
		switch {
		case searchMode:
			total = len(transactions)
			err = nil
		case data.FlowFacet == string(storage.FlowAll) &&
			data.CategoryFacet == string(storage.CategoryFacetAll) &&
			data.FilterCategory == "" && monthFrom.IsZero():
			total, err = s.store.CountTransactionsFiltered(ctx, accountIDs)
		default:
			total, err = s.store.CountTransactionsMatching(ctx, storage.TransactionFilter{
				AccountID: data.FilterAccount, AccountIDs: accountIDs,
				Flow:     storage.NormalizeFlowFacet(data.FlowFacet),
				Category: storage.NormalizeCategoryFacet(data.CategoryFacet), Categories: facetCategories,
				CategoryName: data.FilterCategory, MonthFrom: monthFrom, MonthTo: monthTo,
			})
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	data.TotalTransactions = total
	data.FlowFilters = buildFlowFacetViews(data.FilterAccount, data.SearchQuery, data.Limit, data.FlowFacet, data.CategoryFacet, data.FilterCategory, data.TransactionMonth)
	data.CategoryFilters = buildCategoryFacetViews(data.FilterAccount, data.SearchQuery, data.Limit, data.FlowFacet, data.CategoryFacet, data.TransactionMonth)
	if data.FilterCategory != "" {
		data.CategoryFilters = append(data.CategoryFilters, FacetView{
			Label: data.FilterCategory, Value: "category", Active: true,
			Href: transactionListURL(data.FilterAccount, data.SearchQuery, data.FlowFacet, data.CategoryFacet, "", data.TransactionMonth, 1, data.Limit),
		})
	}
	data.AttachmentNotice = attachmentNotice(r.URL.Query().Get("attachment"))
	if err := s.loadAttachmentData(ctx, &data); err != nil {
		s.fail(w, r, err)
		return
	}
	if data.Workspace.View != domain.WorkspaceAll {
		if err := s.loadRecurringBills(ctx, &data, data.Workspace.View, currency, nil, s.now().UTC()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderPartial(w, "transactions_history", data)
		return
	}
	s.render(w, "transactions", data)
}

func buildFlowFacetViews(account, query string, limit int, selectedFlow, category, categoryName, month string) []FacetView {
	specs := []struct {
		label string
		value storage.FlowFacet
	}{
		{label: "All Flow", value: storage.FlowAll},
		{label: "Money In", value: storage.FlowInflows},
		{label: "Money Out", value: storage.FlowOutflows},
	}
	selectedFlow = string(storage.NormalizeFlowFacet(selectedFlow))
	out := make([]FacetView, 0, len(specs))
	for _, spec := range specs {
		href := transactionListURL(account, query, string(spec.value), category, categoryName, month, 1, limit)
		out = append(out, FacetView{
			Label: spec.label, Value: string(spec.value), Href: href,
			Active: string(spec.value) == selectedFlow,
		})
	}
	return out
}

func buildCategoryFacetViews(account, query string, limit int, flow, selectedCategory, month string) []FacetView {
	specs := []struct {
		label string
		value storage.CategoryFacet
	}{
		{label: "All Categories", value: storage.CategoryFacetAll},
		{label: "Needs", value: storage.CategoryFacetNeeds},
		{label: "Wants", value: storage.CategoryFacetWants},
		{label: "Housing / Rent", value: storage.CategoryFacetHousing},
		{label: "Utilities & Telecom", value: storage.CategoryFacetUtilities},
		{label: "Groceries", value: storage.CategoryFacetGroceries},
		{label: "Transfers / P2P", value: storage.CategoryFacetP2P},
		{label: "Cash / ATM", value: storage.CategoryFacetCash},
	}
	selectedCategory = string(storage.NormalizeCategoryFacet(selectedCategory))
	out := make([]FacetView, 0, len(specs))
	for _, spec := range specs {
		// Choosing a category facet replaces any exact category drill-down.
		href := transactionListURL(account, query, flow, string(spec.value), "", month, 1, limit)
		out = append(out, FacetView{
			Label: spec.label, Value: string(spec.value), Href: href,
			Active: string(spec.value) == selectedCategory,
		})
	}
	return out
}

func transactionListURL(account, query, flow, category, categoryName, month string, page, limit int) string {
	values := make([]string, 0, 6)
	if page > 1 {
		values = append(values, "page="+strconv.Itoa(page))
	}
	values = append(values, "limit="+strconv.Itoa(limit))
	if account != "" {
		values = append(values, "account="+url.QueryEscape(account))
	}
	if query != "" {
		values = append(values, "q="+url.QueryEscape(query))
	}
	if flow != "" && flow != string(storage.FlowAll) {
		values = append(values, "flow="+url.QueryEscape(flow))
	}
	if category != "" && category != string(storage.CategoryFacetAll) {
		values = append(values, "facet="+url.QueryEscape(category))
	}
	if categoryName != "" {
		values = append(values, "category="+url.QueryEscape(categoryName))
	}
	if month != "" {
		values = append(values, "month="+url.QueryEscape(month))
	}
	return "/transactions?" + strings.Join(values, "&")
}

func matchesTransactionFacets(tx domain.Transaction, flow, category string, categories []string) bool {
	switch storage.NormalizeFlowFacet(flow) {
	case storage.FlowInflows:
		if tx.AmountMinor <= 0 {
			return false
		}
	case storage.FlowOutflows:
		if tx.AmountMinor >= 0 {
			return false
		}
	}
	switch storage.NormalizeCategoryFacet(category) {
	case storage.CategoryFacetP2P:
		return transactionCategoryIn(tx.Category, []string{"transfers / p2p", "bizum", "transfers / bizum", "p2p", "transfers / pass-through", "pass-through"})
	case storage.CategoryFacetHousing:
		return transactionCategoryIn(tx.Category, []string{"housing / rent", "housing", "rent", "renta", "alquiler", "mortgage", "hipoteca"})
	case storage.CategoryFacetUtilities:
		return transactionCategoryIn(tx.Category, []string{"utilities & telecom", "utilities", "utility", "telecom", "telecomunicaciones"})
	case storage.CategoryFacetGroceries:
		return transactionCategoryIn(tx.Category, []string{"groceries", "grocery", "supermarket", "supermercado"})
	case storage.CategoryFacetCash:
		return transactionCategoryIn(tx.Category, []string{"cash / atm", "cash", "atm", "cajero", "reintegro"})
	case storage.CategoryFacetNeeds, storage.CategoryFacetWants:
		return transactionCategoryIn(tx.Category, categories)
	default:
		return true
	}
}

func matchesTransactionPeriod(tx domain.Transaction, from, to time.Time, category string) bool {
	if !from.IsZero() && tx.BookingDate.Before(from) {
		return false
	}
	if !to.IsZero() && !tx.BookingDate.Before(to) {
		return false
	}
	if category != "" && !transactionCategoryIn(tx.Category, []string{category}) {
		return false
	}
	return true
}

func transactionCategoryIn(category string, allowed []string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if category == candidate {
			return true
		}
		if isP2PCategoryName(category) && isP2PCategoryName(candidate) {
			return true
		}
	}
	return false
}

func isP2PCategoryName(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "transfers / p2p", "transfers/p2p", "p2p",
		"transfers / bizum", "transfers/bizum", "bizum":
		return true
	default:
		return false
	}
}

func (s *Server) facetCategories(ctx context.Context, workspace, currency, dataFilter string) ([]string, error) {
	pillar := budgets.PillarNeeds
	if storage.NormalizeCategoryFacet(dataFilter) == storage.CategoryFacetWants {
		pillar = budgets.PillarWants
	}
	allocations, err := s.workspaceBudgetAllocations(ctx, workspace, currency)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(allocations))
	out := make([]string, 0, len(allocations))
	for _, allocation := range allocations {
		if allocation.Pillar != pillar {
			continue
		}
		category := strings.TrimSpace(allocation.Category)
		if category == "" {
			continue
		}
		key := strings.ToLower(category)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, category)
	}
	return out, nil
}

func (s *Server) workspaceBudgetAllocations(ctx context.Context, workspace, currency string) ([]budgets.Allocation, error) {
	var allocations []budgets.Allocation
	profile := budgets.DefaultProfile(workspace, currency)
	if workspace != domain.WorkspaceAll && s.budgetPlanner != nil {
		stored, err := s.budgetPlanner.Load(ctx, workspace, currency)
		if err != nil {
			return nil, err
		}
		if stored.Exists {
			profile = stored.Profile
		}
		if stored.Exists && len(stored.Allocations) > 0 {
			allocations = stored.Allocations
		}
	}
	if len(allocations) == 0 {
		allocations = budgets.DefaultAllocations(workspace)
		if workspace == domain.WorkspaceAll {
			allocations = append(allocations, budgets.DefaultAllocations(domain.WorkspaceBusiness)...)
		}
	}
	return budgets.EnsureP2PWantsAllocation(profile, allocations), nil
}

const maxNoteRunes = 2000

func (s *Server) handleTransactionNotes(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if len([]rune(notes)) > maxNoteRunes {
		http.Error(w, "Note is too long", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateTransactionNotes(r.Context(), id, notes); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("update transaction notes", "transaction_id", id, "error", err)
		http.Error(w, "Could not update note", http.StatusInternalServerError)
		return
	}
	s.renderPartialData(w, "transaction_notes", domain.Transaction{ID: id, Notes: notes})
}

func (s *Server) handleAccountType(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	accountID := strings.TrimSpace(r.PathValue("id"))
	accountType := strings.ToLower(strings.TrimSpace(r.FormValue("type")))
	if accountID == "" || (accountType != domain.AccountTypePersonal && accountType != domain.AccountTypeBusiness) {
		http.Error(w, "Invalid account or workspace type", http.StatusBadRequest)
		return
	}
	if err := s.store.SetAccountType(r.Context(), accountID, accountType); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Error("update account workspace", "account_id", accountID, "error", err)
		http.Error(w, "Could not update workspace", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/connections", http.StatusSeeOther)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if strings.EqualFold(strings.TrimSpace(query.Get("format")), "tax-summary") {
		s.exportTaxSummary(w, r)
		return
	}
	format, err := treasury.ParseFormat(query.Get("format"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	from, err := parseExportDate(query.Get("from"), false)
	if err != nil {
		http.Error(w, "Invalid from date: "+err.Error(), http.StatusBadRequest)
		return
	}
	to, err := parseExportDate(query.Get("to"), true)
	if err != nil {
		http.Error(w, "Invalid to date: "+err.Error(), http.StatusBadRequest)
		return
	}
	if to.IsZero() {
		to = s.now().UTC().Add(24 * time.Hour)
	}
	if !from.IsZero() && !from.Before(to) {
		http.Error(w, "The from date must be before the to date", http.StatusBadRequest)
		return
	}
	accountID := strings.TrimSpace(query.Get("account"))
	transactions, err := s.store.ListTransactionsBetween(r.Context(), from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	workspace := s.workspaceState(r)
	workspaceAccounts, err := s.workspaceAccounts(r.Context(), workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(workspaceAccounts, workspace.View), workspace.View)
	if query.Get("include_transfers") != "1" {
		pairs := treasury.PairTransfers(transactions, 3)
		transactions = treasury.ExcludeTransfers(transactions, pairs)
	}
	if accountID != "" {
		filtered := make([]domain.Transaction, 0, len(transactions))
		for _, transaction := range transactions {
			if transaction.AccountID == accountID {
				filtered = append(filtered, transaction)
			}
		}
		transactions = filtered
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := treasury.Export(format, transactions, accounts)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", treasury.ContentType(format))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fiscus-export-%s%s"`, s.now().UTC().Format("2006-01-02"), treasury.Extension(format)))
	// #nosec G705 -- the response is a non-HTML download with an attachment disposition.
	_, _ = w.Write(data)
}

func (s *Server) exportTaxSummary(w http.ResponseWriter, r *http.Request) {
	if s.tax == nil {
		http.Error(w, "Tax estimation is disabled", http.StatusServiceUnavailable)
		return
	}
	now := s.now().UTC()
	year := now.Year()
	if raw := strings.TrimSpace(r.URL.Query().Get("year")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 2000 || parsed > 2200 {
			http.Error(w, "Invalid year", http.StatusBadRequest)
			return
		}
		year = parsed
	}
	quarter := s.tax.QuarterNumber(s.tax.QuarterFor(now))
	if raw := strings.TrimSpace(r.URL.Query().Get("quarter")); raw != "" {
		parsed, err := parseTaxQuarterParam(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		quarter = parsed
	}
	shields, err := s.tax.ShieldsForQuarter(r.Context(), year, quarter)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data, err := tax.SummaryCSV(shields)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="fiscus-tax-summary-%d-Q%d.csv"`, year, quarter))
	// #nosec G705 -- the response is a non-HTML CSV download with an attachment disposition.
	_, _ = w.Write(data)
}

func parseTaxQuarterParam(raw string) (int, error) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "Q")
	quarter, err := strconv.Atoi(value)
	if err != nil || quarter < 1 || quarter > 4 {
		return 0, errors.New("quarter must be Q1, Q2, Q3, or Q4")
	}
	return quarter, nil
}

func parseExportDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("use YYYY-MM-DD")
	}
	if endOfDay {
		parsed = parsed.Add(24 * time.Hour)
	}
	return parsed, nil
}

func (s *Server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Subscriptions & Fees", "subscriptions")
	ctx := r.Context()
	snapshot, err := s.analyzer.Snapshot(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	accounts, err := s.workspaceAccounts(ctx, data.Workspace.View)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	data.Accounts = accounts
	currency := budgetCurrency(accounts)
	fixedBills, fixedRules, err := s.loadFixedBills(ctx, data.Workspace.View, accounts, s.now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	housingRules, err := s.loadHousingRules(ctx, data.Workspace.View, accounts)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	fixedNeeds, err := s.fixedNeedCategories(ctx, data.Workspace.View, currency)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allowed := allowedAccountIDs(accounts, data.Workspace.View)
	subscriptions := make([]analysis.Subscription, 0, len(snapshot.Subscriptions))
	for _, subscription := range snapshot.Subscriptions {
		if _, ok := allowed[subscription.AccountID]; !ok {
			continue
		}
		subscriptions = append(subscriptions, subscription)
	}
	fees := make([]analysis.Fee, 0, len(snapshot.Fees))
	for _, fee := range snapshot.Fees {
		if _, ok := allowed[fee.AccountID]; ok {
			fees = append(fees, fee)
		}
	}
	subscriptions = filterFixedBillSubscriptions(subscriptions, fixedRules, housingRules, fixedNeeds)
	monthlyCosts, annualCosts := subscriptionCurrencyAmounts(subscriptions)
	snapshot.Subscriptions = subscriptions
	snapshot.Fees = fees
	views := subscriptionViews(snapshot.Subscriptions, accountMap(accounts))
	data.Subscriptions = views
	for _, view := range views {
		if view.Active {
			data.ActiveSubscriptions = append(data.ActiveSubscriptions, view)
		} else {
			data.InactiveSubscriptions = append(data.InactiveSubscriptions, view)
		}
	}
	data.Fees = feeViews(snapshot.Fees, accountMap(accounts))
	data.FixedBills = fixedBills
	data.MonthlyCosts = currencyAmountViews(monthlyCosts)
	data.AnnualCosts = currencyAmountViews(annualCosts)
	data.FeeTotals = currencyAmountViews(snapshot.FeeTotals)
	s.render(w, "subscriptions", data)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Alerts", "alerts")
	if err := s.loadAlerts(r.Context(), &data, 200); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "alerts", data)
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Connections", "connections")
	switch r.URL.Query().Get("auth") {
	case "success":
		data.AuthStatus = "success"
	case "error":
		data.AuthStatus = "error"
	}
	if err := s.loadConnections(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "connections", data)
}

// handleAuthorizationCallback completes an OAuth-style bank consent. It is
// intentionally unauthenticated: the bank redirects the user here, and the
// stored, random state value authenticates the pending connection.
func (s *Server) handleAuthorizationCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if providerErr := query.Get("error"); providerErr != "" {
		s.logger.Warn("bank authorization failed", "error", providerErr, "description", query.Get("error_description"))
		http.Redirect(w, r, "/connections?auth=error", http.StatusSeeOther)
		return
	}
	code := query.Get("code")
	state := query.Get("state")
	if code == "" || state == "" {
		http.Error(w, "Missing authorization code or state", http.StatusBadRequest)
		return
	}
	if s.authHandler == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.authHandler.CompleteAuthorization(r.Context(), code, state); err != nil {
		s.logger.Error("complete bank authorization", "error", err)
		http.Redirect(w, r, "/connections?auth=error", http.StatusSeeOther)
		return
	}
	if s.trigger != nil {
		s.trigger.Trigger("")
	}
	http.Redirect(w, r, "/connections?auth=success", http.StatusSeeOther)
}

func (s *Server) handleOverviewPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Overview", "dashboard")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.logger.Error("load overview partial", "error", err)
		http.Error(w, "Could not load overview", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "overview_panel", data)
}

func (s *Server) handleAlertsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Alerts", "dashboard")
	if err := s.loadAlerts(r.Context(), &data, 12); err != nil {
		s.logger.Error("load alerts partial", "error", err)
		http.Error(w, "Could not load alerts", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "alerts_feed", data)
}

func (s *Server) handleConnectionsPartial(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Connections", "connections")
	if err := s.loadConnections(r.Context(), &data); err != nil {
		s.logger.Error("load connections partial", "error", err)
		http.Error(w, "Could not load connections", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "connections_panel", data)
}

func (s *Server) handleSyncAll(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.trigger == nil {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "Banking is not configured.")
		return
	}
	connections, err := s.store.ListConnections(r.Context())
	if err != nil {
		s.logger.Error("load connections for sync", "error", err)
		s.syncNotice(w, http.StatusInternalServerError, "error", "Could not check bank cooldowns.")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.logger.Error("load accounts for sync", "error", err)
		accounts = nil
	}
	if blocked, until := connectionsCoolingDown(connections, accounts, s.now()); blocked {
		s.syncNotice(w, http.StatusOK, "info", rateLimitMessage(until))
		return
	}
	if s.trigger.Trigger("") {
		s.syncNotice(w, http.StatusAccepted, "success", "Sync requested. Data refreshes shortly.")
		return
	}
	s.syncNotice(w, http.StatusAccepted, "info", "A sync is already queued or running.")
}

func (s *Server) handleSyncConnection(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.trigger == nil {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "Banking is not configured.")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.syncNotice(w, http.StatusNotFound, "error", "Connection not found.")
		return
	}
	connection, err := s.store.GetConnectionByID(r.Context(), id)
	if err != nil {
		s.syncNotice(w, http.StatusNotFound, "error", "Connection not found.")
		return
	}
	accounts, err := s.store.ListAccounts(r.Context())
	if err != nil {
		s.logger.Error("load accounts for sync", "error", err)
		accounts = nil
	}
	if blocked, until := connectionCoolingDown(connection, accounts, s.now()); blocked {
		s.syncNotice(w, http.StatusOK, "info", rateLimitMessage(until))
		return
	}
	if s.trigger.Trigger(connection.RequisitionID) {
		s.syncNotice(w, http.StatusAccepted, "success", "Sync requested for "+connection.InstitutionName+".")
		return
	}
	s.syncNotice(w, http.StatusAccepted, "info", "A sync is already queued or running.")
}

func rateLimitMessage(until time.Time) string {
	when := "soon"
	if !until.IsZero() {
		when = until.UTC().Format("2006-01-02 15:04 UTC")
	}
	return "Bank rate limit reached (PSD2 max daily limit). Displaying cached data. Next sync available at " + when + "."
}

func connectionsCoolingDown(connections []domain.Connection, accounts []domain.Account, now time.Time) (bool, time.Time) {
	if len(connections) == 0 {
		return false, time.Time{}
	}
	var earliest time.Time
	for _, connection := range connections {
		blocked, until := connectionCoolingDown(connection, accounts, now)
		if !blocked {
			return false, time.Time{}
		}
		earliest = earlier(earliest, until)
	}
	return true, earliest
}

func connectionCoolingDown(connection domain.Connection, accounts []domain.Account, now time.Time) (bool, time.Time) {
	if connection.CoolingDown(now) {
		return true, *connection.RateLimitedUntil
	}

	var (
		earliest time.Time
		count    int
	)
	for _, account := range accounts {
		if account.ConnectionID != connection.ID {
			continue
		}
		count++
		if !account.CoolingDown(now) {
			return false, time.Time{}
		}
		earliest = earlier(earliest, *account.RateLimitedUntil)
	}
	if count == 0 {
		return false, time.Time{}
	}
	return true, earliest
}

func earlier(current, candidate time.Time) time.Time {
	if current.IsZero() || candidate.Before(current) {
		return candidate
	}
	return current
}

func (s *Server) handleRecategorize(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.classifier == nil || s.classifier.RuleCount() == 0 {
		s.syncNotice(w, http.StatusServiceUnavailable, "error", "No categorization rules are configured.")
		return
	}
	result, err := s.store.Recategorize(r.Context(), s.classifier.Classify, true)
	if err != nil {
		s.logger.Error("recategorize transactions", "error", err)
		s.syncNotice(w, http.StatusInternalServerError, "error", "Could not re-run category rules.")
		return
	}
	paired, err := s.store.NeutralizePassThroughs(r.Context(), 3)
	if err != nil {
		s.logger.Error("neutralize pass-through transfers", "error", err)
		s.syncNotice(w, http.StatusInternalServerError, "error", "Could not neutralize pass-through transfers.")
		return
	}
	message := fmt.Sprintf("Examined %d transactions; updated %d; neutralized %d pass-through pair(s).",
		result.Examined, result.Changed, paired)
	s.syncNotice(w, http.StatusOK, "success", message)
}

func validSyncRequest(r *http.Request) bool {
	return r.Header.Get(syncRequestHeader) == "1"
}

func (s *Server) syncNotice(w http.ResponseWriter, status int, class, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<div class="notice %s" role="status">%s</div>`, class, html.EscapeString(message))
}

func firstOfMonth(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func parseMonthStart(raw string) (time.Time, bool) {
	parsed, err := time.Parse("2006-01", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, time.UTC), true
}

func budgetMonthURL(workspace, month string) string {
	values := url.Values{}
	if workspace != "" {
		values.Set("workspace", workspace)
	}
	if month != "" {
		values.Set("month", month)
	}
	if len(values) == 0 {
		return "/budgets"
	}
	return "/budgets?" + values.Encode()
}

func parsePage(raw string) int {
	page, err := strconv.Atoi(raw)
	if err != nil || page < 1 {
		return 1
	}
	if page > 10000 {
		return 10000
	}
	return page
}

func parseLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultTransactionLimit
	}
	if limit > maxTransactionLimit {
		return maxTransactionLimit
	}
	return limit
}
