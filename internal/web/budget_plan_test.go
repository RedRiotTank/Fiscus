package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestBudgetCustomizeEndpoints(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)

	edit := f.request(t, http.MethodGet, "/api/budgets/customize?workspace=personal&currency=EUR", true, nil)
	if edit.Code != http.StatusOK {
		t.Fatalf("customize GET = %d %s", edit.Code, edit.Body.String())
	}
	for _, want := range []string{`hx-post="/api/budgets/customize"`, `name="needs_percent"`, `Dining`, `Add custom category`} {
		if !strings.Contains(edit.Body.String(), want) {
			t.Errorf("customize form missing %q", want)
		}
	}

	form := url.Values{
		"workspace":       {"personal"},
		"currency":        {"EUR"},
		"needs_percent":   {"60"},
		"wants_percent":   {"20"},
		"savings_percent": {"20"},
		"category":        {"Dining"},
		"pillar":          {"needs"},
		"percent":         {"15"},
		"new_category":    {"Travel"},
		"new_pillar":      {"wants"},
		"new_percent":     {"5"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/budgets/customize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/budgets?workspace=personal" {
		t.Fatalf("customize POST = %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	profile, ok, err := f.store.GetBudgetProfile(context.Background(), "personal", "EUR")
	if err != nil || !ok {
		t.Fatalf("profile = ok=%v err=%v", ok, err)
	}
	if profile.NeedsBPS != 6000 || profile.WantsBPS != 2000 || profile.SavingsBPS != 2000 {
		t.Fatalf("profile targets = %+v", profile)
	}
	allocations, err := f.store.ListBudgetAllocations(context.Background(), "personal", "EUR")
	if err != nil {
		t.Fatalf("allocations: %v", err)
	}
	byCategory := make(map[string]domain.BudgetAllocation, len(allocations))
	for _, allocation := range allocations {
		byCategory[allocation.Category] = allocation
	}
	if len(allocations) != 2 || byCategory["Dining"].Pillar != "needs" ||
		byCategory["Dining"].PercentBPS != 1500 || byCategory["Travel"].PercentBPS != 500 ||
		byCategory["Travel"].Pillar != "wants" {
		t.Fatalf("allocations = %+v", allocations)
	}

	deleteForm := url.Values{
		"workspace":       {"personal"},
		"currency":        {"EUR"},
		"needs_percent":   {"60"},
		"wants_percent":   {"20"},
		"savings_percent": {"20"},
		"category":        {"Dining", "Travel"},
		"pillar":          {"needs", "wants"},
		"percent":         {"15", "5"},
		"delete":          {"Dining", "Travel"},
	}
	deleteReq := httptest.NewRequest(http.MethodPost, "/api/budgets/customize", strings.NewReader(deleteForm.Encode()))
	deleteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deleteReq.Header.Set(syncRequestHeader, "1")
	deleteReq.SetBasicAuth("admin", "secret")
	deleteRec := httptest.NewRecorder()
	f.handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusSeeOther {
		t.Fatalf("delete POST = %d %s", deleteRec.Code, deleteRec.Body.String())
	}
	allocations, err = f.store.ListBudgetAllocations(context.Background(), "personal", "EUR")
	if err != nil {
		t.Fatalf("allocations after delete: %v", err)
	}
	if len(allocations) != 0 {
		t.Fatalf("allocations after delete = %+v", allocations)
	}
	emptyPage := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if !strings.Contains(emptyPage.Body.String(), "No budget configured for this workspace") {
		t.Fatal("deleting all categories should show the empty state")
	}
}

func TestBudgetAdvisoryToggle(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	form := url.Values{
		"workspace": {"personal"},
		"currency":  {"EUR"},
		"enabled":   {"false"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/budgets/advisory/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d %s", rec.Code, rec.Body.String())
	}
	profile, ok, err := f.store.GetBudgetProfile(context.Background(), "personal", "EUR")
	if err != nil || !ok || profile.AdvisoryEnabled {
		t.Fatalf("profile = %+v ok=%v err=%v", profile, ok, err)
	}
	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if !strings.Contains(page.Body.String(), "Advisory Paused") {
		t.Fatal("page does not show paused advisory state")
	}
}

func TestBudgetWorkspaceIsolationAndEmptyState(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	now := f.server.now().UTC()
	if err := f.store.SaveBudgetPlan(context.Background(), domain.BudgetProfile{
		Workspace: domain.WorkspaceBusiness, Currency: "EUR",
		NeedsBPS: 10000, WantsBPS: 0, SavingsBPS: 0,
		AdvisoryEnabled: true, UpdatedAt: now,
	}, []domain.BudgetAllocation{{
		Workspace: domain.WorkspaceBusiness, Currency: "EUR",
		Category: "Software & Cloud", Pillar: "needs", PercentBPS: 1000,
		NotifyAtPercent: []int{80, 100}, SortOrder: 0, UpdatedAt: now,
	}}); err != nil {
		t.Fatalf("SaveBudgetPlan business: %v", err)
	}
	personal := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if !strings.Contains(personal.Body.String(), "Dining") ||
		strings.Contains(personal.Body.String(), "Software &amp; Cloud") {
		t.Fatal("personal budget page leaked business categories")
	}
	business := f.request(t, http.MethodGet, "/budgets?workspace=business", true, nil)
	if !strings.Contains(business.Body.String(), "Software &amp; Cloud") ||
		strings.Contains(business.Body.String(), "Dining") {
		t.Fatal("business budget page leaked personal categories")
	}

	emptyFixture := newWebFixture(t)
	emptyFixture.server.budgetPlanner = budgets.NewPlanner(emptyFixture.store, emptyFixture.server.now)
	if err := emptyFixture.store.SaveBudgetPlan(context.Background(), domain.BudgetProfile{
		Workspace: domain.WorkspaceBusiness, Currency: "EUR",
		NeedsBPS: 2800, WantsBPS: 0, SavingsBPS: 2100,
		AdvisoryEnabled: true, UpdatedAt: emptyFixture.server.now().UTC(),
	}, nil); err != nil {
		t.Fatalf("empty plan: %v", err)
	}
	empty := emptyFixture.request(t, http.MethodGet, "/budgets?workspace=business", true, nil)
	if !strings.Contains(empty.Body.String(), "No budget configured for this workspace") {
		t.Fatal("empty workspace state missing")
	}
	if strings.Contains(empty.Body.String(), `id="budget-donut"`) {
		t.Fatal("empty workspace rendered a donut")
	}
	all := emptyFixture.request(t, http.MethodGet, "/budgets", true, nil)
	if !strings.Contains(all.Body.String(), "Choose Personal or Business") {
		t.Fatal("all-workspace budget prompt missing")
	}
	if strings.Contains(all.Body.String(), `id="budget-donut"`) {
		t.Fatal("all-workspace view rendered a donut")
	}
}

func TestBudgetDonutData(t *testing.T) {
	t.Parallel()
	plan := budgets.Plan{
		Currency: "EUR", BaselineMinor: 280_000,
		Categories: []budgets.CategoryPlan{
			{Category: "Rent", LimitMinor: 85_000, SpentMinor: 85_000},
			{Category: "Groceries", LimitMinor: 35_000, SpentMinor: 24_717},
		},
	}
	view := buildBudgetDonut(plan)
	if view == nil {
		t.Fatal("expected donut data")
	}
}

func TestDefaultBudgetTemplateAutoSeed(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{"Rent", "Groceries", "Utilities &amp; Telecom", "Restaurants &amp; Delivery", "Subscriptions", "Leisure &amp; Shopping", "Emergency Fund"} {
		if !strings.Contains(body, want) {
			t.Errorf("default budget page missing %q", want)
		}
	}
	if strings.Contains(body, "No budget configured for this workspace") {
		t.Fatal("default template should not render the empty state")
	}
	profile, ok, err := f.store.GetBudgetProfile(context.Background(), "personal", "EUR")
	if err != nil || !ok {
		t.Fatalf("profile = ok=%v err=%v", ok, err)
	}
	if profile.NeedsBPS != 5000 || profile.WantsBPS != 3000 || profile.SavingsBPS != 2000 {
		t.Fatalf("profile = %+v", profile)
	}
	allocations, err := f.store.ListBudgetAllocations(context.Background(), "personal", "EUR")
	if err != nil {
		t.Fatalf("allocations: %v", err)
	}
	total := 0
	for _, allocation := range allocations {
		total += allocation.PercentBPS
	}
	if len(allocations) != 7 || total != 10000 {
		t.Fatalf("allocations = %+v total=%d", allocations, total)
	}

	businessPage := f.request(t, http.MethodGet, "/budgets?workspace=business", true, nil)
	if businessPage.Code != http.StatusOK {
		t.Fatalf("business budgets status = %d", businessPage.Code)
	}
	businessBody := businessPage.Body.String()
	for _, want := range []string{"Software &amp; Cloud", "Gestoría / Legal", "Office &amp; Equipment", "Freelance Subcontractors", "Tax Reserve", "Nominal operating revenue"} {
		if !strings.Contains(businessBody, want) {
			t.Errorf("default business budget page missing %q", want)
		}
	}
	if strings.Contains(businessBody, "Groceries") || strings.Contains(businessBody, "Restaurants &amp; Delivery") {
		t.Fatal("personal categories leaked into default business budget")
	}
	businessAllocations, err := f.store.ListBudgetAllocations(context.Background(), "business", "EUR")
	if err != nil {
		t.Fatalf("business allocations: %v", err)
	}
	if len(businessAllocations) != 5 {
		t.Fatalf("business allocations = %+v", businessAllocations)
	}
}

func TestNominalBudgetBaseline(t *testing.T) {
	t.Parallel()
	if baseline, nominal := nominalBudgetBaseline(0); baseline != 200_000 || !nominal {
		t.Fatalf("zero baseline = %d/%v", baseline, nominal)
	}
	if baseline, nominal := nominalBudgetBaseline(280_000); baseline != 280_000 || nominal {
		t.Fatalf("income baseline = %d/%v", baseline, nominal)
	}
}
