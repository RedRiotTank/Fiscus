package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

const housingRuleSource = "housing"

func housingRuleSourceFor(workspace string) string {
	return housingRuleSource + ":" + strings.ToLower(strings.TrimSpace(workspace))
}

func (s *Server) loadHousingSetup(ctx context.Context, data *PageData, workspace, currency string, transactions []domain.Transaction, now time.Time) error {
	detection := housing.Detect(transactions, now)
	view := &HousingSetupView{
		Workspace: workspace, Currency: currency, Detection: detection,
	}
	rule, exists, err := s.store.GetHousingRule(ctx, workspace, currency)
	if err != nil {
		return err
	}
	if exists {
		view.Rule = &rule
	}
	data.Housing = view
	return nil
}

func (s *Server) handleHousingSetup(w http.ResponseWriter, r *http.Request) {
	if s.budgetPlanner == nil {
		http.Error(w, "Budget persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	workspace := s.workspaceState(r).View
	if workspace == domain.WorkspaceAll {
		http.Error(w, "Select Personal or Business to configure housing", http.StatusBadRequest)
		return
	}
	if !s.budgetWorkspaceAllowed(workspace) {
		http.Error(w, "This workspace is locked by configuration", http.StatusForbidden)
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
	editor, err := s.buildHousingEditor(r.Context(), workspace, currency, "", "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderPartialData(w, "budget_housing_form", editor)
}

func (s *Server) handleHousingSetupSave(w http.ResponseWriter, r *http.Request) {
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
	if err := s.saveHousingRule(r.Context(), r, workspace, currency); err != nil {
		editor, buildErr := s.buildHousingEditor(r.Context(), workspace, currency, r.FormValue("landlord_counterparty"), err.Error())
		if buildErr != nil {
			s.fail(w, r, buildErr)
			return
		}
		editor.PersonalNetShareMinor = parseHousingShareOrZero(r.FormValue("personal_net_share"), currency)
		editor.CalculationMode = strings.ToLower(strings.TrimSpace(r.FormValue("calculation_mode")))
		if editor.CalculationMode == "" {
			editor.CalculationMode = domain.HousingModeFixedShare
		}
		s.renderPartialData(w, "budget_housing_form", editor)
		return
	}
	s.redirectBudget(w, r, "/budgets?workspace="+workspace)
}

func (s *Server) saveHousingRule(ctx context.Context, r *http.Request, workspace, currency string) error {
	landlord := strings.TrimSpace(r.FormValue("landlord_counterparty"))
	if landlord == "" {
		return fmt.Errorf("a landlord or master payment counterparty is required")
	}
	share, err := money.Parse(r.FormValue("personal_net_share"), currency)
	if err != nil || share < 0 {
		return fmt.Errorf("your net monthly share must be a positive amount")
	}
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("calculation_mode")))
	if mode != domain.HousingModeNetBalance && mode != domain.HousingModeFixedShare {
		return fmt.Errorf("choose a valid housing calculation mode")
	}
	flatmates := parseHousingFlatmates(r)
	rule, err := s.store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: workspace, Currency: currency, LandlordCounterparty: landlord,
		PersonalNetShareCents: share, CalculationMode: mode,
		FlatmateCounterparties: flatmates,
	})
	if err != nil {
		return err
	}
	if err := s.store.DeleteLearnedRulesBySource(ctx, housingRuleSourceFor(workspace)); err != nil {
		return err
	}
	counterparties := append([]string{rule.LandlordCounterparty}, rule.FlatmateCounterparties...)
	for _, counterparty := range counterparties {
		counterparty = strings.TrimSpace(counterparty)
		if len([]rune(counterparty)) < 3 {
			continue
		}
		if _, err := s.store.UpsertLearnedRule(ctx, learnedPattern(counterparty), housing.Category, housingRuleSourceFor(workspace)); err != nil {
			return err
		}
	}
	if _, err := s.store.Recategorize(ctx, func(tx domain.Transaction) string {
		return housing.RuleCategory(tx, rule)
	}, true); err != nil {
		return err
	}
	if reloader, ok := s.classifier.(interface{ Reload(context.Context) error }); ok {
		if err := reloader.Reload(ctx); err != nil {
			s.logger.Error("reload housing category rules", "error", err)
		}
	}
	return nil
}

func (s *Server) buildHousingEditor(ctx context.Context, workspace, currency, landlord, message string) (HousingEditorView, error) {
	detection, err := s.detectHousing(ctx, workspace, currency)
	if err != nil {
		return HousingEditorView{}, err
	}
	rule, exists, err := s.store.GetHousingRule(ctx, workspace, currency)
	if err != nil {
		return HousingEditorView{}, err
	}
	editor := HousingEditorView{
		Workspace: workspace, Currency: currency, CalculationMode: domain.HousingModeFixedShare,
		Error: message, Detected: detection.Found,
	}
	if landlord != "" {
		editor.Landlord = landlord
	}
	selected := make(map[string]bool)
	if exists {
		editor.Landlord = rule.LandlordCounterparty
		editor.PersonalNetShareMinor = rule.PersonalNetShareCents
		editor.CalculationMode = rule.CalculationMode
		for _, flatmate := range rule.FlatmateCounterparties {
			selected[strings.ToLower(flatmate)] = true
		}
		editor.MasterAmountMinor = detection.GrossRentMinor
	} else if detection.Found {
		editor.Landlord = detection.Landlord.Counterparty
		editor.MasterAmountMinor = detection.GrossRentMinor
		editor.PersonalNetShareMinor = detection.NetShareMinor
		for _, flatmate := range detection.Flatmates {
			selected[strings.ToLower(flatmate.Counterparty)] = true
		}
	}
	seen := make(map[string]struct{})
	if detection.Found {
		for _, candidate := range detection.Flatmates {
			key := strings.ToLower(candidate.Counterparty)
			seen[key] = struct{}{}
			editor.Flatmates = append(editor.Flatmates, HousingFlatmateView{
				Counterparty: candidate.Counterparty, AmountMinor: candidate.AmountMinor,
				Detected: true, Selected: selected[key],
			})
		}
	}
	if exists {
		for _, flatmate := range rule.FlatmateCounterparties {
			key := strings.ToLower(flatmate)
			if _, ok := seen[key]; ok {
				continue
			}
			editor.Flatmates = append(editor.Flatmates, HousingFlatmateView{
				Counterparty: flatmate, Selected: selected[key],
			})
		}
	}
	return editor, nil
}

func (s *Server) detectHousing(ctx context.Context, workspace, currency string) (housing.Detection, error) {
	accounts, err := s.workspaceAccounts(ctx, workspace)
	if err != nil {
		return housing.Detection{}, err
	}
	now := s.now().UTC()
	transactions, err := s.store.ListTransactionsBetween(ctx, now.AddDate(0, -7, 0), now.Add(24*time.Hour))
	if err != nil {
		return housing.Detection{}, err
	}
	transactions = filterTransactions(transactions, allowedAccountIDs(accounts, workspace), workspace)
	detection := housing.Detect(transactions, now)
	if detection.Found && detection.Currency != "" && detection.Currency != currency {
		detection = housing.Detection{}
	}
	return detection, nil
}

func parseHousingFlatmates(r *http.Request) []string {
	out := make([]string, 0, len(r.Form["flatmate"])+1)
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	for _, flatmate := range r.Form["flatmate"] {
		add(flatmate)
	}
	for _, flatmate := range strings.FieldsFunc(r.FormValue("new_flatmate"), func(r rune) bool {
		return r == ',' || r == ';'
	}) {
		add(flatmate)
	}
	return out
}

func parseHousingShareOrZero(value, currency string) int64 {
	share, err := money.Parse(value, currency)
	if err != nil || share < 0 {
		return 0
	}
	return share
}
