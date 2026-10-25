package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestCategoryStylesAreDeterministic(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Groceries":             "cat-groceries",
		"Utilities":             "cat-utilities",
		"Restaurants":           "cat-dining",
		"Software":              "cat-software",
		"Rent":                  "cat-housing",
		"Housing / Rent":        "cat-housing",
		"Transfers / P2P":       "cat-bizum",
		"Health":                "cat-health",
		"Taxi":                  "cat-travel",
		"Professional Services": "cat-services",
		"Uncategorized":         "cat-uncategorized",
	}
	for category, want := range tests {
		if got := categoryClass(category); got != want {
			t.Errorf("categoryClass(%q) = %q, want %q", category, got, want)
		}
	}
	first := categoryClass("Mystery Vendor")
	if first == "" || first != categoryClass("Mystery Vendor") {
		t.Fatalf("fallback category class is not deterministic: %q", first)
	}
	if categoryColor("Groceries") != "#34d399" {
		t.Fatalf("groceries color = %q", categoryColor("Groceries"))
	}
	if categoryColor("Emergency Fund") == categoryColor("Investments") {
		t.Fatal("savings categories should have distinct chart colors")
	}
}

func TestBuildBudgetDonut(t *testing.T) {
	t.Parallel()
	plan := budgets.Plan{
		Currency: "EUR", BaselineMinor: 280_000,
		Categories: []budgets.CategoryPlan{
			{Category: "Restaurants", LimitMinor: 45_000, SpentMinor: 45_522},
			{Category: "Groceries", LimitMinor: 24_000, SpentMinor: 21_756},
		},
	}
	view := buildBudgetDonut(plan)
	if view == nil {
		t.Fatal("expected donut view")
	}
	var payload budgetDonutPayload
	if err := json.Unmarshal([]byte(view.Data), &payload); err != nil {
		t.Fatalf("decode donut payload: %v", err)
	}
	if payload.Currency != "EUR" || len(payload.Labels) != 2 ||
		payload.Labels[0] != "Restaurants" || payload.Colors[0] != "#a78bfa" {
		t.Fatalf("donut payload = %+v", payload)
	}
	plan.UnallocatedMinor = 100_000
	view = buildBudgetDonut(plan)
	if view == nil {
		t.Fatal("expected donut view with unallocated buffer")
	}
	if err := json.Unmarshal([]byte(view.Data), &payload); err != nil {
		t.Fatalf("decode donut payload: %v", err)
	}
	if len(payload.Labels) != 3 || payload.Labels[2] != "Unallocated" || payload.Colors[2] != "#475569" {
		t.Fatalf("unallocated payload = %+v", payload)
	}
}

func seedPersonalBudgetPlan(t *testing.T, f *webFixture) {
	t.Helper()
	f.server.budgetPlanner = budgets.NewPlanner(f.store, f.server.now)
	now := f.server.now().UTC()
	err := f.store.SaveBudgetPlan(context.Background(), domain.BudgetProfile{
		Workspace: domain.WorkspacePersonal, Currency: "EUR",
		NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
		AdvisoryEnabled: true, UpdatedAt: now,
	}, []domain.BudgetAllocation{
		{Workspace: domain.WorkspacePersonal, Currency: "EUR", Category: "Dining", Pillar: "wants",
			PercentBPS: 1000, NotifyAtPercent: []int{80, 100}, SortOrder: 0, UpdatedAt: now},
	})
	if err != nil {
		t.Fatalf("SaveBudgetPlan: %v", err)
	}
}

func TestBudgetsPageRendersIncomeDrivenPlan(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	seedPersonalBudgetPlan(t, f)
	page := f.request(t, http.MethodGet, "/budgets?workspace=personal", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`id="budget-donut"`,
		`data-budget-donut=`,
		`budget-donut-center`,
		`Advisory Active`,
		`Customize Allocation`,
		`Needs`,
		`Wants`,
		`Savings`,
		`/static/budget-donut.js`,
		`/static/budget-customize.js`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("budgets page missing %q", want)
		}
	}
	if !strings.Contains(body, "cat-dining") {
		t.Error("budget category badge is not using the deterministic category color")
	}
}

func TestTransactionsTableErgonomics(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	page := f.request(t, http.MethodGet, "/transactions", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("transactions status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`class="panel transactions-history"`,
		`class="table-wrap transactions-table-wrap"`,
		`class="transactions-table"`,
		`<th scope="col">Transaction</th>`,
		`class="tx-description"`,
		`class="tx-counterparty" title="Coffee Shop">Coffee Shop`,
		`title="Flat white"`,
		`badge category cat-dining`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("transactions page missing %q", want)
		}
	}
	if strings.Contains(body, `<th scope="col">Counterparty</th>`) {
		t.Error("transactions table still has a separate Counterparty column")
	}
}
