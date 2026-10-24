package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
)

func TestCategoryStylesAreDeterministic(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"Groceries":             "cat-groceries",
		"Utilities":             "cat-utilities",
		"Restaurants":           "cat-dining",
		"Software":              "cat-software",
		"Rent":                  "cat-housing",
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
}

func TestBuildBudgetDonut(t *testing.T) {
	t.Parallel()
	view := buildBudgetDonut([]budgets.Status{
		{Category: "Restaurants", Currency: "EUR", LimitMinor: 45_000, SpentMinor: 45_522},
		{Category: "Groceries", Currency: "EUR", LimitMinor: 24_000, SpentMinor: 21_756},
		{Category: "Travel", Currency: "USD", LimitMinor: 10_000, SpentMinor: 2_000},
	})
	if view == nil {
		t.Fatal("expected donut view")
	}
	if view.Currency != "EUR" || view.TotalBudgetMinor != 69_000 || view.TotalSpentMinor != 67_278 {
		t.Fatalf("donut totals = %+v", view)
	}
	if view.Percent != 98 || len(view.Slices) != 2 {
		t.Fatalf("donut summary = %+v", view)
	}
	if !view.Slices[0].Over || view.Slices[0].Percent != 101 {
		t.Fatalf("restaurants slice = %+v", view.Slices[0])
	}
	var payload budgetDonutPayload
	if err := json.Unmarshal([]byte(view.Data), &payload); err != nil {
		t.Fatalf("decode donut payload: %v", err)
	}
	if payload.Currency != "EUR" || len(payload.Labels) != 2 ||
		payload.Labels[0] != "Restaurants" || payload.Colors[0] != "#a78bfa" {
		t.Fatalf("donut payload = %+v", payload)
	}
}

func TestBudgetsPageRendersDonut(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	page := f.request(t, http.MethodGet, "/budgets", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets status = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{
		`id="budget-donut"`,
		`data-budget-donut=`,
		`budget-donut-center`,
		`Planned budget`,
		`Month-to-date spent`,
		`/static/budget-donut.js`,
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
