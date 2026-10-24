package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
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
	if err := s.loadOverview(r.Context(), &data); err != nil {
		s.fail(w, r, err)
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
		data.BudgetEmpty = true
		return nil
	}
	now := s.now().UTC()
	cfg := budgets.NormalizeAdvisoryConfig(s.budgetAdvisory)
	cfg.Enabled = true
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -(cfg.HeuristicWindowMonths - 1), 0)
	transactions, err := s.store.ListTransactionsBetween(ctx, windowStart, now.Add(24*time.Hour))
	if err != nil {
		return err
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(data.Accounts, workspace), workspace)
	taxProvision := make(map[string]int64)
	if workspace == domain.WorkspaceBusiness {
		for _, shield := range data.TaxShields {
			taxProvision[strings.ToUpper(shield.Currency)] += shield.TaxReserveMinor / 3
		}
	}
	advisories := budgets.ComputeAdvisory(transactions, nil, cfg, taxProvision, now)
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
	plan := budgets.BuildPlan(stored.Profile, stored.Allocations, spending, baseline, tax, netAfterTax, now)
	view := &BudgetPlanView{
		Plan:               plan,
		AllocatedPercent:   roundedBPSPercent(plan.AllocatedBPS),
		UnallocatedPercent: roundedBPSPercent(plan.UnallocatedBPS),
	}
	if workspace == domain.WorkspaceBusiness {
		view.BaselineLabel = "Operating revenue"
		view.BaselineSubtitle = fmt.Sprintf("Planned tax reserve %s · net after reserve %s",
			money.FormatGrouped(tax, currency), money.FormatGrouped(netAfterTax, currency))
	} else {
		view.BaselineLabel = "Total net monthly income"
		view.BaselineSubtitle = "Confirmed recurring inflows, net of transfers and internal movements"
	}
	view.Donut = buildBudgetDonut(plan)
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

func roundedBPSPercent(bps int) int64 {
	if bps < 0 {
		return int64((bps - 50) / 100)
	}
	return int64((bps + 50) / 100)
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
