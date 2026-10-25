package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/pkg/recurring"
)

const (
	recurringRuleSource  = "recurring"
	defaultRecurringCat  = "Utilities & Telecom"
	defaultRecurringPill = "needs"
)

func recurringRuleSourceFor(workspace string) string {
	return recurringRuleSource + ":" + strings.ToLower(strings.TrimSpace(workspace))
}

func (s *Server) loadRecurringBills(ctx context.Context, data *PageData, workspace, currency string, transactions []domain.Transaction, now time.Time) error {
	view, err := s.buildRecurringView(ctx, workspace, currency, transactions, now)
	if err != nil {
		return err
	}
	data.Recurring = view
	return nil
}

func (s *Server) buildRecurringView(ctx context.Context, workspace, currency string, transactions []domain.Transaction, now time.Time) (*RecurringBillsView, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	if workspace == "" || workspace == domain.WorkspaceAll {
		return nil, nil
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	now = now.UTC()
	if transactions == nil {
		accounts, err := s.workspaceAccounts(ctx, workspace)
		if err != nil {
			return nil, err
		}
		transactions, err = s.store.ListTransactionsBetween(ctx, now.AddDate(0, -7, 0), now.Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		transactions = filterTransactions(transactions, allowedAccountIDs(accounts, workspace), workspace)
	}
	rules, err := s.store.ListRecurringBillRules(ctx, workspace, currency)
	if err != nil {
		return nil, err
	}
	configured := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		configured[strings.ToLower(strings.TrimSpace(rule.Counterparty))] = struct{}{}
	}
	view := &RecurringBillsView{Workspace: workspace, Currency: currency, Rules: rules}
	for _, candidate := range recurring.Detect(transactions, now) {
		if !strings.EqualFold(candidate.Currency, currency) {
			continue
		}
		if _, exists := configured[strings.ToLower(candidate.Counterparty)]; exists {
			continue
		}
		view.Candidates = append(view.Candidates, candidate)
	}
	return view, nil
}

func (s *Server) handleRecurringBillSetup(w http.ResponseWriter, r *http.Request) {
	if s.budgetPlanner == nil {
		http.Error(w, "Budget persistence is not configured", http.StatusServiceUnavailable)
		return
	}
	workspace := s.workspaceState(r).View
	if workspace == domain.WorkspaceAll {
		http.Error(w, "Select Personal or Business to configure recurring bills", http.StatusBadRequest)
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
	counterparty := strings.TrimSpace(r.URL.Query().Get("counterparty"))
	editor, err := s.buildRecurringEditor(r.Context(), workspace, currency, counterparty, "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.renderPartialData(w, "budget_recurring_form", editor)
}

func (s *Server) handleRecurringBillSetupSave(w http.ResponseWriter, r *http.Request) {
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
	counterparty := strings.TrimSpace(r.FormValue("counterparty"))
	if err := s.saveRecurringBillRule(r.Context(), r, workspace, currency, counterparty); err != nil {
		editor, buildErr := s.buildRecurringEditor(r.Context(), workspace, currency, counterparty, err.Error())
		if buildErr != nil {
			s.fail(w, r, buildErr)
			return
		}
		editor.Category = strings.TrimSpace(r.FormValue("category"))
		editor.Pillar = strings.ToLower(strings.TrimSpace(r.FormValue("pillar")))
		editor.Recurrence = strings.ToLower(strings.TrimSpace(r.FormValue("recurrence")))
		editor.SplitMode = strings.ToLower(strings.TrimSpace(r.FormValue("split_mode")))
		editor.SplitPeople = parsePositiveInt(r.FormValue("split_people"), 1)
		editor.PersonalShareMinor = parseHousingShareOrZero(r.FormValue("personal_share"), currency)
		s.renderPartialData(w, "budget_recurring_form", editor)
		return
	}
	s.redirectBudget(w, r, "/budgets?workspace="+workspace)
}

func (s *Server) saveRecurringBillRule(ctx context.Context, r *http.Request, workspace, currency, counterparty string) error {
	if counterparty == "" {
		return fmt.Errorf("a recurring counterparty is required")
	}
	candidate, ok, err := s.recurringCandidate(ctx, workspace, currency, counterparty)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("the detected recurring payment could not be found")
	}
	category := strings.TrimSpace(r.FormValue("category"))
	if category == "" {
		return fmt.Errorf("choose a budget category")
	}
	pillar := strings.ToLower(strings.TrimSpace(r.FormValue("pillar")))
	switch pillar {
	case "needs", "wants", "savings":
	default:
		return fmt.Errorf("choose a valid budget pillar")
	}
	recurrence := strings.ToLower(strings.TrimSpace(r.FormValue("recurrence")))
	switch recurrence {
	case domain.RecurringMonthly, domain.RecurringQuarterly, domain.RecurringYearly:
	case "":
		recurrence = domain.RecurringMonthly
	default:
		return fmt.Errorf("choose a valid recurrence")
	}
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("split_mode")))
	if mode != domain.RecurringSplitSolo && mode != domain.RecurringSplitShared {
		return fmt.Errorf("choose solo or shared split mode")
	}
	people := parsePositiveInt(r.FormValue("split_people"), 1)
	share := candidate.AmountMinor
	if mode == domain.RecurringSplitShared {
		if raw := strings.TrimSpace(r.FormValue("personal_share")); raw != "" {
			parsed, parseErr := money.Parse(raw, currency)
			if parseErr != nil || parsed <= 0 {
				return fmt.Errorf("your personal share must be a positive amount")
			}
			share = parsed
		} else {
			if people < 2 {
				return fmt.Errorf("choose at least two people for a shared bill")
			}
			share = recurring.PersonalShare(candidate.AmountMinor, people)
		}
		if share > candidate.AmountMinor {
			return fmt.Errorf("your personal share cannot exceed the gross bill")
		}
	}
	rule, err := s.store.SaveRecurringBillRule(ctx, domain.RecurringBillRule{
		Workspace: workspace, Currency: currency, Counterparty: candidate.Counterparty,
		Category: category, Pillar: pillar, Recurrence: recurrence,
		SplitMode: mode, SplitPeople: people,
		GrossMinor: candidate.AmountMinor, PersonalShareMinor: share, Active: true,
	})
	if err != nil {
		return err
	}
	if err := s.store.DeleteLearnedRulesBySource(ctx, recurringRuleSourceFor(workspace)); err != nil {
		return err
	}
	if _, err := s.store.UpsertLearnedRule(ctx, learnedPattern(rule.Counterparty), rule.Category, recurringRuleSourceFor(workspace)); err != nil {
		return err
	}
	if _, err := s.store.Recategorize(ctx, func(tx domain.Transaction) string {
		if tx.AmountMinor < 0 && recurring.MatchesCounterparty(tx, rule.Counterparty) {
			return rule.Category
		}
		return ""
	}, true); err != nil {
		return err
	}
	if reloader, ok := s.classifier.(interface{ Reload(context.Context) error }); ok {
		if err := reloader.Reload(ctx); err != nil {
			s.logger.Error("reload recurring bill category rules", "error", err)
		}
	}
	return nil
}

func (s *Server) recurringCandidate(ctx context.Context, workspace, currency, counterparty string) (recurring.Candidate, bool, error) {
	view, err := s.buildRecurringView(ctx, workspace, currency, nil, s.now().UTC())
	if err != nil {
		return recurring.Candidate{}, false, err
	}
	if view == nil {
		return recurring.Candidate{}, false, nil
	}
	for _, candidate := range view.Candidates {
		if strings.EqualFold(candidate.Counterparty, counterparty) {
			return candidate, true, nil
		}
	}
	// An existing rule may be edited even though detection no longer lists it.
	rule, exists, err := s.store.GetRecurringBillRule(ctx, workspace, currency, counterparty)
	if err != nil {
		return recurring.Candidate{}, false, err
	}
	if exists {
		return recurring.Candidate{
			Counterparty: rule.Counterparty, AmountMinor: rule.GrossMinor,
			Currency: rule.Currency,
		}, true, nil
	}
	return recurring.Candidate{}, false, nil
}

func (s *Server) buildRecurringEditor(ctx context.Context, workspace, currency, counterparty, message string) (RecurringBillEditorView, error) {
	view, err := s.buildRecurringView(ctx, workspace, currency, nil, s.now().UTC())
	if err != nil {
		return RecurringBillEditorView{}, err
	}
	editor := RecurringBillEditorView{
		Workspace: workspace, Currency: currency,
		Category: defaultRecurringCat, Pillar: defaultRecurringPill,
		Recurrence: domain.RecurringMonthly,
		SplitMode:  domain.RecurringSplitSolo, SplitPeople: 2, Error: message,
	}
	rule, exists, err := s.store.GetRecurringBillRule(ctx, workspace, currency, counterparty)
	if err != nil {
		return RecurringBillEditorView{}, err
	}
	if exists {
		editor.Counterparty = rule.Counterparty
		editor.Category = rule.Category
		editor.Pillar = rule.Pillar
		editor.Recurrence = rule.Recurrence
		editor.SplitMode = rule.SplitMode
		editor.SplitPeople = rule.SplitPeople
		editor.GrossMinor = rule.GrossMinor
		editor.PersonalShareMinor = rule.PersonalShareMinor
	} else {
		matched := false
		if view != nil {
			for _, candidate := range view.Candidates {
				if counterparty != "" && !strings.EqualFold(candidate.Counterparty, counterparty) {
					continue
				}
				editor.Counterparty = candidate.Counterparty
				editor.GrossMinor = candidate.AmountMinor
				// Leave the custom-share field blank for new candidates so the
				// people selector drives the division by default.
				editor.PersonalShareMinor = 0
				editor.Occurrences = candidate.Occurrences
				editor.TypicalDay = candidate.TypicalDay
				editor.Detected = true
				if candidate.Category != "" && !strings.EqualFold(candidate.Category, "Uncategorized") {
					editor.Category = candidate.Category
				}
				matched = true
				break
			}
		}
		if !matched && counterparty != "" {
			editor.Counterparty = counterparty
		}
	}
	options, encoded, err := s.recurringCategoryOptions(ctx, workspace, currency, editor.Category, editor.Pillar)
	if err != nil {
		return RecurringBillEditorView{}, err
	}
	editor.Categories = options
	editor.CategoryOptions = encoded
	if editor.Recurrence == "" {
		editor.Recurrence = domain.RecurringMonthly
	}
	return editor, nil
}

func (s *Server) recurringCategoryOptions(ctx context.Context, workspace, currency, currentCategory, currentPillar string) ([]RecurringCategoryOption, string, error) {
	allocations, err := s.workspaceBudgetAllocations(ctx, workspace, currency)
	if err != nil {
		return nil, "", err
	}
	seen := make(map[string]struct{}, len(allocations)+1)
	options := make([]RecurringCategoryOption, 0, len(allocations)+1)
	for _, allocation := range allocations {
		name := strings.TrimSpace(allocation.Category)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		options = append(options, RecurringCategoryOption{
			Name: name, Pillar: strings.ToLower(strings.TrimSpace(string(allocation.Pillar))),
		})
	}
	currentCategory = strings.TrimSpace(currentCategory)
	if currentCategory != "" {
		if _, exists := seen[strings.ToLower(currentCategory)]; !exists {
			options = append(options, RecurringCategoryOption{
				Name: currentCategory, Pillar: strings.ToLower(strings.TrimSpace(currentPillar)),
			})
		}
	}
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, "", err
	}
	return options, string(encoded), nil
}

func parsePositiveInt(raw string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < 1 {
		return fallback
	}
	if parsed > 100 {
		return 100
	}
	return parsed
}
