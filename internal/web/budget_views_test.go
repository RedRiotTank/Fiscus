package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestBudgetChartDatasetsForDonutAndBars(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	plan := budgets.Plan{
		Currency: "EUR", BaselineMinor: 300_000, AllocatedMinor: 270_000,
		UnallocatedMinor: 30_000,
		Categories: []budgets.CategoryPlan{
			{Category: "Utilities & Telecom", Pillar: budgets.PillarNeeds, LimitMinor: 24_000, SpentMinor: 12_000},
			{Category: "Restaurants", Pillar: budgets.PillarWants, LimitMinor: 36_000, SpentMinor: 40_000},
		},
		Pillars: []budgets.PillarPlan{
			{Pillar: budgets.PillarNeeds, Label: "Needs", AllocatedMinor: 240_000, SpentMinor: 120_000, TargetMinor: 150_000},
			{Pillar: budgets.PillarWants, Label: "Wants", AllocatedMinor: 30_000, SpentMinor: 40_000, TargetMinor: 90_000},
			{Pillar: budgets.PillarSavings, Label: "Savings & Investment", AllocatedMinor: 0, TargetMinor: 60_000},
		},
	}
	view := buildBudgetDonutAt(plan, now)
	if view == nil {
		t.Fatal("expected chart data")
	}
	var payload budgetDonutPayload
	if err := json.Unmarshal([]byte(view.Data), &payload); err != nil {
		t.Fatalf("decode chart payload: %v", err)
	}
	if payload.Currency != "EUR" || payload.BaselineMinor != 300_000 ||
		payload.DayOfMonth != 10 || payload.DaysInMonth != 31 || payload.PacePercent != 32 {
		t.Fatalf("chart metadata = %+v", payload)
	}
	if len(payload.Pillars) != 3 || payload.Pillars[0].Label != "Needs" ||
		payload.Pillars[0].Allocated != 240_000 || payload.Pillars[0].Color != "#60a5fa" {
		t.Fatalf("macro donut pillars = %+v", payload.Pillars)
	}
	if len(payload.Categories) != 2 ||
		payload.Categories[0].Label != "Utilities & Telecom" ||
		payload.Categories[0].Budget != 24_000 || payload.Categories[0].Spent != 12_000 ||
		payload.Categories[1].Pillar != string(budgets.PillarWants) {
		t.Fatalf("grouped bar categories = %+v", payload.Categories)
	}
	if len(payload.Labels) != 3 || payload.Labels[2] != "Unallocated" {
		t.Fatalf("legacy category labels = %+v", payload.Labels)
	}
}

func TestBudgetHistoricalMonthScopesSpending(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	anchor := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	f.server.now = func() time.Time { return anchor }
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	ctx := context.Background()
	if err := f.server.budgetPlanner.Save(ctx,
		budgets.DefaultProfile(domain.WorkspacePersonal, "EUR"),
		budgets.DefaultAllocations(domain.WorkspacePersonal)); err != nil {
		t.Fatalf("Save budget plan: %v", err)
	}
	if _, err := f.store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: domain.WorkspacePersonal, Currency: "EUR", LandlordCounterparty: "Landlord",
		PersonalNetShareCents: 35_000, CalculationMode: domain.HousingModeFixedShare,
	}); err != nil {
		t.Fatalf("SaveHousingRule: %v", err)
	}
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "sep-salary", AmountMinor: 300_000, Currency: "EUR", BookingDate: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), Counterparty: "Employer", Category: "Payroll", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "sep-rent", AmountMinor: -105_000, Currency: "EUR", BookingDate: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), Counterparty: "Landlord", Category: "Uncategorized", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "sep-groceries", AmountMinor: -5_000, Currency: "EUR", BookingDate: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC), Counterparty: "Mercadona", Category: "Groceries", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "oct-salary", AmountMinor: 300_000, Currency: "EUR", BookingDate: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Counterparty: "Employer", Category: "Payroll", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "oct-rent", AmountMinor: -105_000, Currency: "EUR", BookingDate: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), Counterparty: "Landlord", Category: "Uncategorized", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "oct-groceries", AmountMinor: -9_000, Currency: "EUR", BookingDate: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), Counterparty: "Mercadona", Category: "Groceries", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	page := f.request(t, http.MethodGet, "/budgets?workspace=personal&month=2026-09", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("historical budgets status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{"September 2026", "Income in September 2026", "Current Month", "€350.00 of", "€50.00 of", "category=Groceries&amp;month=2026-09"} {
		if !strings.Contains(body, want) {
			t.Errorf("historical budget page missing %q", want)
		}
	}
	if strings.Contains(body, "€90.00 of") {
		t.Fatal("historical budget page leaked October spend")
	}
	partial := f.request(t, http.MethodGet, "/budgets?workspace=personal&month=2026-09", true, map[string]string{"HX-Request": "true"})
	if partial.Code != http.StatusOK || strings.Contains(partial.Body.String(), "<html") ||
		!strings.Contains(partial.Body.String(), `id="budgets-container"`) {
		t.Fatalf("budget HTMX partial = %d %s", partial.Code, partial.Body.String())
	}
}

func TestBudgetDrilldownURL(t *testing.T) {
	t.Parallel()
	got := budgetDrilldownURL("2026-10", "Restaurants & Delivery", "personal")
	for _, want := range []string{
		"/transactions?", "month=2026-10", "category=Restaurants+%26+Delivery", "workspace=personal",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("drilldown URL = %q missing %q", got, want)
		}
	}
}

func TestBudgetPageRendersChartSwitcher(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`data-budget-view-toggle`, `id="budget-donut"`, `id="budget-bars"`,
		`data-budget-view-panel="bars"`, `of net income committed`, `unallocated surplus`,
		`/static/budget-donut.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("budgets page missing %q", want)
		}
	}
	if strings.Contains(body, "ring-inner") {
		t.Fatal("concentric dual-ring legend should be gone")
	}
}

func TestRecurringBillWizardPersistsPersonalShare(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	ctx := context.Background()
	now := f.server.now().UTC()
	transactions := []domain.Transaction{
		{AccountID: "acc-1", ExternalID: "bizum-fiber-1", AmountMinor: -1600, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-2, 1, 8, 0, 0, 0, time.UTC), Counterparty: "Alberto Avidad Fernández", Description: "Bizum fibra", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "bizum-fiber-2", AmountMinor: -1600, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month()-1, 1, 8, 0, 0, 0, time.UTC), Counterparty: "Alberto Avidad Fernández", Description: "Bizum fibra", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: "acc-1", ExternalID: "bizum-fiber-3", AmountMinor: -1600, Currency: "EUR", BookingDate: time.Date(now.Year(), now.Month(), 1, 8, 0, 0, 0, time.UTC), Counterparty: "Alberto Avidad Fernández", Description: "Bizum fibra", Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := f.store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{"Recurring payment detected", "Alberto Avidad Fernández", "€16.00", "/api/budgets/recurring/setup", "recurring-prompts my-6 space-y-4"} {
		if !strings.Contains(body, want) {
			t.Errorf("recurring prompt missing %q", want)
		}
	}

	setup := f.request(t, http.MethodGet, "/api/budgets/recurring/setup?workspace=personal&currency=EUR&counterparty="+url.QueryEscape("Alberto Avidad Fernández"), true, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("setup status = %d body=%s", setup.Code, setup.Body.String())
	}
	for _, want := range []string{"Shared Bill", "Split by people", "Save Recurring Rule", `<select name="category" aria-label="Budget category" data-recurring-category`, `data-recurring-options=`, `<option value="Utilities &amp; Telecom"`, `name="recurrence"`, "Monthly"} {
		if !strings.Contains(setup.Body.String(), want) {
			t.Errorf("recurring setup missing %q", want)
		}
	}

	form := url.Values{
		"workspace":      {"personal"},
		"currency":       {"EUR"},
		"counterparty":   {"Alberto Avidad Fernández"},
		"category":       {"Utilities & Telecom"},
		"pillar":         {"needs"},
		"recurrence":     {"monthly"},
		"split_mode":     {"shared"},
		"split_people":   {"3"},
		"personal_share": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/budgets/recurring/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d body=%s", rec.Code, rec.Body.String())
	}
	rule, exists, err := f.store.GetRecurringBillRule(ctx, "personal", "EUR", "Alberto Avidad Fernández")
	if err != nil || !exists {
		t.Fatalf("rule exists=%v err=%v", exists, err)
	}
	if rule.SplitMode != domain.RecurringSplitShared || rule.SplitPeople != 3 ||
		rule.PersonalShareMinor != 533 || rule.GrossMinor != 1600 || rule.Recurrence != domain.RecurringMonthly {
		t.Fatalf("saved rule = %+v", rule)
	}
	after := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if after.Code != http.StatusOK {
		t.Fatalf("budgets after save = %d", after.Code)
	}
	if !strings.Contains(after.Body.String(), "€5.33 of") {
		t.Fatalf("budget page does not show the personal share: %s", after.Body.String())
	}
}
