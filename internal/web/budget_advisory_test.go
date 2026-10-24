package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/budgets"
)

func TestBudgetAdvisoryPageAndJSON(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	manager := budgets.NewManager(f.store, []budgets.Budget{
		{Category: "Dining", LimitMinor: 50_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}, budgets.AdvisoryConfig{
		Enabled: true, Model: budgets.AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, f.server.now)
	f.server.budgetManager = manager

	page := f.request(t, http.MethodGet, "/budgets", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("budgets page = %d %s", page.Code, page.Body.String())
	}
	for _, want := range []string{"Budget Advisor", "Apply Recommended Budgets", "Over-allocated", "Dining"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("budgets page missing %q", want)
		}
	}

	api := f.request(t, http.MethodGet, "/api/budgets/advisory", true, nil)
	if api.Code != http.StatusOK {
		t.Fatalf("advisory API = %d %s", api.Code, api.Body.String())
	}
	var payload struct {
		Enabled    bool               `json:"enabled"`
		Model      string             `json:"model"`
		Advisories []budgets.Advisory `json:"advisories"`
	}
	if err := json.Unmarshal(api.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode advisory: %v", err)
	}
	if !payload.Enabled || payload.Model != budgets.AdvisoryModelHybrid || len(payload.Advisories) == 0 {
		t.Fatalf("payload = %+v", payload)
	}
	advisory := payload.Advisories[0]
	if advisory.Currency != "EUR" || advisory.MonthlyNetIncomeMinor != 300_000 ||
		len(advisory.Categories) == 0 || advisory.Categories[0].Category != "Dining" {
		t.Fatalf("advisory = %+v", advisory)
	}
	if advisory.Categories[0].Status != budgets.StatusOverAllocated {
		t.Fatalf("dining status = %+v", advisory.Categories[0])
	}
}

func TestBudgetAdvisoryApplyAndToggle(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	manager := budgets.NewManager(f.store, []budgets.Budget{
		{Category: "Dining", LimitMinor: 50_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}, budgets.AdvisoryConfig{
		Enabled: true, Model: budgets.AdvisoryModelHybrid,
		NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20,
		HeuristicWindowMonths: 6, OverbudgetWarningThreshold: 0.15, UnderbudgetNoticeThreshold: 0.30,
	}, f.server.now)
	f.server.budgetManager = manager

	apply := f.request(t, http.MethodPost, "/api/budgets/advisory/apply", true, map[string]string{syncRequestHeader: "1"})
	if apply.Code != http.StatusSeeOther || !strings.Contains(apply.Header().Get("Location"), "advisory=applied") {
		t.Fatalf("apply = %d %q", apply.Code, apply.Header().Get("Location"))
	}
	overrides, err := f.store.ListBudgetOverrides(t.Context())
	if err != nil {
		t.Fatalf("ListBudgetOverrides: %v", err)
	}
	if len(overrides) == 0 {
		t.Fatal("expected applied budget overrides")
	}
	foundDining := false
	for _, override := range overrides {
		if override.Category == "Dining" {
			foundDining = true
			if override.LimitMinor <= 0 {
				t.Fatalf("dining override = %+v", override)
			}
		}
	}
	if !foundDining {
		t.Fatalf("overrides = %+v", overrides)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/budgets/advisory/toggle", strings.NewReader("enabled=false"))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(syncRequestHeader, "1")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d %s", rec.Code, rec.Body.String())
	}
	advisory, err := manager.Advisory(t.Context())
	if err != nil {
		t.Fatalf("Advisory: %v", err)
	}
	if advisory.Enabled {
		t.Fatal("advisory should be paused")
	}
}
