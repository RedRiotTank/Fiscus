package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// BudgetPlanView augments a budget plan with display metadata.
type BudgetPlanView struct {
	budgets.Plan
	BaselineLabel      string
	BaselineSubtitle   string
	AllocatedPercent   int64
	UnallocatedPercent int64
	Nominal            bool
	Donut              *BudgetDonutView
}

// BudgetEditorView is the customize-allocation drawer model.
type BudgetEditorView struct {
	Workspace   string
	Currency    string
	Profile     budgets.Profile
	Allocations []budgets.Allocation
	Error       string
}

func (s *Server) handleBudgets(w http.ResponseWriter, r *http.Request) {
	data := s.baseData(r, "Budgets", "budgets")
	now := s.now().UTC()
	current := firstOfMonth(now)
	selected := current
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		if parsed, ok := parseMonthStart(raw); ok && !parsed.After(current) {
			selected = parsed
		}
	}
	data.BudgetMonth = selected
	data.BudgetMonthValue = selected.Format("2006-01")
	data.BudgetMonthLabel = selected.Format("January 2006")
	data.BudgetIsCurrentMonth = selected.Equal(current)
	data.BudgetPrevHref = budgetMonthURL(data.Workspace.View, selected.AddDate(0, -1, 0).Format("2006-01"))
	if !data.BudgetIsCurrentMonth {
		data.BudgetNextHref = budgetMonthURL(data.Workspace.View, selected.AddDate(0, 1, 0).Format("2006-01"))
	}
	data.BudgetCurrentHref = budgetMonthURL(data.Workspace.View, "")
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.fail(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderPartial(w, "budgets_container", data)
		return
	}
	s.render(w, "budgets", data)
}

func (s *Server) loadBudgetPlan(ctx context.Context, data *PageData, spending []storage.CategorySpending) error {
	workspace := data.Workspace.View
	data.BudgetWorkspace = workspace
	if workspace == domain.WorkspaceAll {
		data.BudgetWorkspaceRequired = true
		return nil
	}
	currency := budgetCurrency(data.Accounts)
	data.BudgetCurrency = currency
	if s.budgetPlanner == nil {
		data.BudgetEmpty = true
		return nil
	}
	stored, err := s.budgetPlanner.Load(ctx, workspace, currency)
	if err != nil {
		return err
	}
	if !stored.Exists || len(stored.Allocations) == 0 {
		profile := stored.Profile
		if !stored.Exists || profile.Workspace == "" {
			profile = budgets.DefaultProfile(workspace, currency)
		}
		if err := s.budgetPlanner.Save(ctx, profile, budgets.DefaultAllocations(workspace)); err != nil {
			return err
		}
		stored, err = s.budgetPlanner.Load(ctx, workspace, currency)
		if err != nil {
			return err
		}
	}
	stored.Allocations = budgets.EnsureP2PWantsAllocation(stored.Profile, stored.Allocations)
	stored.Allocations = budgets.EnsureAccruingSavingsAllocation(stored.Profile, stored.Allocations, needsGenericAccruingSavingsBar(data.AccruingAccounts))
	if len(stored.Allocations) == 0 {
		data.BudgetEmpty = true
		return nil
	}
	now := s.now().UTC()
	monthStart := firstOfMonth(now)
	if !data.BudgetMonth.IsZero() {
		monthStart = firstOfMonth(data.BudgetMonth)
	}
	monthEnd := monthStart.AddDate(0, 1, 0)
	periodNow := now
	historical := monthStart.Before(firstOfMonth(now))
	if historical {
		periodNow = monthEnd.Add(-time.Second)
	}
	cfg := budgets.NormalizeAdvisoryConfig(s.budgetAdvisory)
	cfg.Enabled = true
	windowStart := monthStart.AddDate(0, -(cfg.HeuristicWindowMonths - 1), 0)
	transactions, err := s.store.ListTransactionsBetween(ctx, windowStart, monthEnd)
	if err != nil {
		return err
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(data.Accounts, workspace), workspace)
	if err := s.loadHousingSetup(ctx, data, workspace, currency, transactions, periodNow); err != nil {
		return err
	}
	if err := s.loadRecurringBills(ctx, data, workspace, currency, transactions, periodNow); err != nil {
		return err
	}
	if data.Recurring != nil && len(data.Recurring.Rules) > 0 {
		monthTransactions := transactions[:0]
		for _, tx := range transactions {
			if !tx.BookingDate.Before(monthStart) && tx.BookingDate.Before(monthStart.AddDate(0, 1, 0)) {
				monthTransactions = append(monthTransactions, tx)
			}
		}
		spending = budgets.NetRecurringBillSpending(spending, monthTransactions, data.Recurring.Rules)
	}
	taxProvision := make(map[string]int64)
	if workspace == domain.WorkspaceBusiness {
		for _, shield := range data.TaxShields {
			taxProvision[strings.ToUpper(shield.Currency)] += shield.TaxReserveMinor / 3
		}
	}
	advisories := budgets.ComputeAdvisory(transactions, nil, cfg, taxProvision, periodNow)
	baseline, tax, netAfterTax := int64(0), int64(0), int64(0)
	for _, advisory := range advisories {
		if advisory.Currency != currency {
			continue
		}
		if workspace == domain.WorkspaceBusiness {
			baseline = advisory.MonthlyInflowMinor
			tax = advisory.TaxProvisionMinor
			netAfterTax = advisory.MonthlyNetIncomeMinor
		} else {
			baseline = advisory.MonthlyNetIncomeMinor
			tax = advisory.TaxProvisionMinor
			netAfterTax = baseline
		}
		break
	}
	if historical {
		monthlyIncome := budgets.MonthlyIncomeMinor(transactions, currency, monthStart, monthEnd)
		if monthlyIncome > 0 {
			baseline = monthlyIncome
			tax = 0
			netAfterTax = monthlyIncome
		}
	}
	nominal := false
	if baseline <= 0 {
		baseline, nominal = nominalBudgetBaseline(0)
		tax, netAfterTax = 0, baseline
	}
	if workspace == domain.WorkspaceBusiness {
		for _, allocation := range stored.Allocations {
			category := strings.ToLower(allocation.Category)
			if strings.Contains(category, "tax") && strings.Contains(category, "reserve") {
				tax = baseline * int64(allocation.PercentBPS) / 10000
				netAfterTax = baseline - tax
				break
			}
		}
	}
	plan := budgets.BuildPlan(stored.Profile, stored.Allocations, spending, baseline, tax, netAfterTax, periodNow)
	view := &BudgetPlanView{
		Plan:               plan,
		AllocatedPercent:   roundedBPSPercent(plan.AllocatedBPS),
		UnallocatedPercent: roundedBPSPercent(plan.UnallocatedBPS),
		Nominal:            nominal,
	}
	switch {
	case nominal && workspace == domain.WorkspaceBusiness:
		view.BaselineLabel = "Nominal operating revenue"
		view.BaselineSubtitle = fmt.Sprintf("Awaiting confirmed income · provisional %s baseline",
			money.FormatGrouped(baseline, currency))
	case nominal:
		view.BaselineLabel = "Nominal monthly baseline"
		view.BaselineSubtitle = fmt.Sprintf("Awaiting confirmed income · provisional %s baseline",
			money.FormatGrouped(baseline, currency))
	case workspace == domain.WorkspaceBusiness:
		view.BaselineLabel = "Operating revenue"
		view.BaselineSubtitle = fmt.Sprintf("Planned tax reserve %s · net after reserve %s",
			money.FormatGrouped(tax, currency), money.FormatGrouped(netAfterTax, currency))
	default:
		view.BaselineLabel = "Total net monthly income"
		view.BaselineSubtitle = "Confirmed recurring inflows, net of transfers and internal movements"
	}
	if historical {
		view.BaselineLabel = "Income in " + monthStart.Format("January 2006")
		view.BaselineSubtitle = "Historical baseline and spending are scoped to the selected month"
	}
	chartNow := now
	if historical {
		chartNow = periodNow
	}
	view.Donut = buildBudgetDonutAt(plan, chartNow)
	data.BudgetPlan = view
	return nil
}

func (s *Server) budgetWorkspaceAllowed(workspace string) bool {
	switch s.profile {
	case domain.WorkspacePersonal, domain.WorkspaceBusiness:
		return s.profile == workspace
	default:
		return true
	}
}

func nominalBudgetBaseline(baseline int64) (int64, bool) {
	if baseline > 0 {
		return baseline, false
	}
	return 200_000, true
}

func budgetCurrency(accounts []domain.Account) string {
	fallback := ""
	for _, account := range accounts {
		currency := strings.ToUpper(strings.TrimSpace(account.Currency))
		if currency == "" {
			continue
		}
		if currency == "EUR" {
			return currency
		}
		if fallback == "" {
			fallback = currency
		}
	}
	if fallback != "" {
		return fallback
	}
	return "EUR"
}

func (s *Server) handleBudgetCustomize(w http.ResponseWriter, r *http.Request) {
	if s.budgetPlanner == nil {
		http.Error(w, "Budget persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	workspace := s.workspaceState(r).View
	if workspace == domain.WorkspaceAll {
		http.Error(w, "Select Personal or Business to customize a budget", http.StatusBadRequest)
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("currency")))
	if currency == "" {
		accounts, err := s.workspaceAccounts(r.Context(), workspace)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		currency = budgetCurrency(accounts)
	}
	stored, err := s.budgetPlanner.Load(r.Context(), workspace, currency)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !stored.Exists || len(stored.Allocations) == 0 {
		if !stored.Exists {
			stored.Profile = budgets.DefaultProfile(workspace, currency)
		}
		stored.Allocations = budgets.DefaultAllocations(workspace)
	}
	stored.Allocations = budgets.EnsureP2PWantsAllocation(stored.Profile, stored.Allocations)
	accruing, accruingErr := s.listAccruingAccounts(r.Context(), workspace)
	if accruingErr != nil {
		s.fail(w, r, accruingErr)
		return
	}
	stored.Allocations = budgets.EnsureAccruingSavingsAllocation(stored.Profile, stored.Allocations, len(accruing) > 0)
	editor := BudgetEditorView{
		Workspace: workspace, Currency: currency,
		Profile: stored.Profile, Allocations: stored.Allocations,
	}
	s.renderPartialData(w, "budget_customize_form", editor)
}

func (s *Server) handleBudgetCustomizeSave(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.budgetPlanner == nil {
		http.Error(w, "Budget persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	workspace := strings.ToLower(strings.TrimSpace(r.FormValue("workspace")))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if currency == "" {
		currency = "EUR"
	}
	if workspace != domain.WorkspacePersonal && workspace != domain.WorkspaceBusiness {
		http.Error(w, "A workspace is required", http.StatusBadRequest)
		return
	}
	if !s.budgetWorkspaceAllowed(workspace) {
		http.Error(w, "This workspace is locked by configuration", http.StatusForbidden)
		return
	}
	existing, err := s.budgetPlanner.Load(r.Context(), workspace, currency)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	profile, allocations, parseErr := parseBudgetEditor(r, workspace, currency, existing)
	if parseErr != nil {
		editor := BudgetEditorView{
			Workspace: workspace, Currency: currency, Profile: profile,
			Allocations: allocations, Error: parseErr.Error(),
		}
		s.renderPartialData(w, "budget_customize_form", editor)
		return
	}
	if existing.Exists {
		profile.AdvisoryEnabled = existing.Profile.AdvisoryEnabled
	} else {
		profile.AdvisoryEnabled = true
	}
	if err := s.budgetPlanner.Save(r.Context(), profile, allocations); err != nil {
		editor := BudgetEditorView{
			Workspace: workspace, Currency: currency, Profile: profile,
			Allocations: allocations, Error: err.Error(),
		}
		s.renderPartialData(w, "budget_customize_form", editor)
		return
	}
	s.redirectBudget(w, r, "/budgets?workspace="+workspace)
}

func (s *Server) handleBudgetAdvisoryToggle(w http.ResponseWriter, r *http.Request) {
	if !validSyncRequest(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if s.budgetPlanner == nil {
		http.Error(w, "Budget persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	workspace := strings.ToLower(strings.TrimSpace(r.FormValue("workspace")))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if workspace != domain.WorkspacePersonal && workspace != domain.WorkspaceBusiness {
		http.Error(w, "A workspace is required", http.StatusBadRequest)
		return
	}
	if !s.budgetWorkspaceAllowed(workspace) {
		http.Error(w, "This workspace is locked by configuration", http.StatusForbidden)
		return
	}
	if currency == "" {
		currency = "EUR"
	}
	raw := strings.ToLower(strings.TrimSpace(r.FormValue("enabled")))
	enabled := raw == "true" || raw == "on" || raw == "1"
	if err := s.budgetPlanner.SetAdvisoryEnabled(r.Context(), workspace, currency, enabled); err != nil {
		s.logger.Error("toggle budget advisory", "error", err)
		http.Error(w, "Could not update the budget advisory", http.StatusInternalServerError)
		return
	}
	s.redirectBudget(w, r, "/budgets?workspace="+workspace)
}

func parseBudgetEditor(r *http.Request, workspace, currency string, existing budgets.StoredPlan) (budgets.Profile, []budgets.Allocation, error) {
	needs, err := parseBPS(r.FormValue("needs_percent"))
	if err != nil {
		return budgets.Profile{}, nil, fmt.Errorf("needs target: %w", err)
	}
	wants, err := parseBPS(r.FormValue("wants_percent"))
	if err != nil {
		return budgets.Profile{}, nil, fmt.Errorf("wants target: %w", err)
	}
	savings, err := parseBPS(r.FormValue("savings_percent"))
	if err != nil {
		return budgets.Profile{}, nil, fmt.Errorf("savings target: %w", err)
	}
	if needs+wants+savings > 10000 {
		return budgets.Profile{}, nil, errors.New("pillar targets cannot exceed 100% in total")
	}
	profile := budgets.Profile{
		Workspace: workspace, Currency: currency,
		NeedsBPS: needs, WantsBPS: wants, SavingsBPS: savings,
		AdvisoryEnabled: existing.Profile.AdvisoryEnabled,
	}
	customByCategory := make(map[string]bool, len(existing.Allocations))
	notifyByCategory := make(map[string][]int, len(existing.Allocations))
	for _, allocation := range existing.Allocations {
		customByCategory[strings.ToLower(allocation.Category)] = allocation.Custom
		notifyByCategory[strings.ToLower(allocation.Category)] = allocation.NotifyAtPercent
	}
	deleteSet := make(map[string]struct{}, len(r.Form["delete"]))
	for _, category := range r.Form["delete"] {
		deleteSet[strings.TrimSpace(category)] = struct{}{}
	}
	categories := r.Form["category"]
	pillars := r.Form["pillar"]
	percents := r.Form["percent"]
	if len(categories) != len(pillars) || len(categories) != len(percents) {
		return profile, nil, errors.New("malformed allocation rows")
	}
	allocations := make([]budgets.Allocation, 0, len(categories)+1)
	seen := make(map[string]struct{}, len(categories)+1)
	for index, rawCategory := range categories {
		category := strings.TrimSpace(rawCategory)
		if category == "" {
			continue
		}
		if _, deleted := deleteSet[category]; deleted {
			continue
		}
		key := strings.ToLower(category)
		if _, duplicate := seen[key]; duplicate {
			return profile, allocations, fmt.Errorf("category %q is listed more than once", category)
		}
		seen[key] = struct{}{}
		percent, err := parseBPS(percents[index])
		if err != nil {
			return profile, allocations, fmt.Errorf("%s: %w", category, err)
		}
		pillar := budgets.Pillar(strings.ToLower(strings.TrimSpace(pillars[index])))
		if pillar != budgets.PillarNeeds && pillar != budgets.PillarWants && pillar != budgets.PillarSavings {
			return profile, allocations, fmt.Errorf("%s has an invalid pillar", category)
		}
		allocations = append(allocations, budgets.Allocation{
			Category: category, Pillar: pillar, PercentBPS: percent,
			NotifyAtPercent: notifyByCategory[key], SortOrder: index,
			Custom: customByCategory[key],
		})
	}
	if newCategory := strings.TrimSpace(r.FormValue("new_category")); newCategory != "" {
		key := strings.ToLower(newCategory)
		if _, duplicate := seen[key]; duplicate {
			return profile, allocations, fmt.Errorf("category %q already exists", newCategory)
		}
		percent, err := parseBPS(r.FormValue("new_percent"))
		if err != nil {
			return profile, allocations, fmt.Errorf("%s: %w", newCategory, err)
		}
		pillar := budgets.Pillar(strings.ToLower(strings.TrimSpace(r.FormValue("new_pillar"))))
		if pillar != budgets.PillarNeeds && pillar != budgets.PillarWants && pillar != budgets.PillarSavings {
			return profile, allocations, fmt.Errorf("%s has an invalid pillar", newCategory)
		}
		allocations = append(allocations, budgets.Allocation{
			Category: newCategory, Pillar: pillar, PercentBPS: percent,
			NotifyAtPercent: []int{80, 100}, SortOrder: len(allocations), Custom: true,
		})
	}
	if len(allocations) == 0 {
		// A 50/30/20 save without hand-picked categories must still produce a
		// fully active budget instead of an empty allocation list.
		allocations = budgets.DefaultAllocations(workspace)
	}
	return profile, allocations, nil
}

func parseBPS(raw string) (int, error) {
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	value = strings.ReplaceAll(value, ",", ".")
	if value == "" {
		return 0, errors.New("percentage is required")
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, errors.New("percentage must be a number")
	}
	if parsed < 0 || parsed > 100 {
		return 0, errors.New("percentage must be between 0 and 100")
	}
	return int(math.Round(parsed * 100)), nil
}

func bpsPercent(bps int) string {
	if bps%100 == 0 {
		return fmt.Sprintf("%d%%", bps/100)
	}
	return fmt.Sprintf("%.1f%%", float64(bps)/100)
}

func bpsInput(bps int) string {
	value := fmt.Sprintf("%.2f", float64(bps)/100)
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")
	if value == "" {
		return "0"
	}
	return value
}

func moneyInput(minor int64, currency string) string {
	return money.Format(minor, currency)
}

func roundedBPSPercent(bps int) int64 {
	if bps < 0 {
		return int64((bps - 50) / 100)
	}
	return int64((bps + 50) / 100)
}

func budgetDrilldownURL(month, category, workspace string) string {
	values := url.Values{}
	if month != "" {
		values.Set("month", month)
	}
	if category != "" {
		values.Set("category", category)
	}
	if workspace != "" && workspace != domain.WorkspaceAll {
		values.Set("workspace", workspace)
	}
	if len(values) == 0 {
		return "/transactions"
	}
	return "/transactions?" + values.Encode()
}

func (s *Server) redirectBudget(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// #nosec G710 -- target is a fixed /budgets path with a validated workspace.
	http.Redirect(w, r, target, http.StatusSeeOther)
}
