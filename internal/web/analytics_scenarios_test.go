package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsScenarioSimulation(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("analytics page status = %d", page.Code)
	}
	for _, want := range []string{"Scenario simulator", "analytics-scenarios.js", "data-scenario-open"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("analytics page missing %q", want)
		}
	}

	payload := `{"months":12,"income_delta_minor":10000,"expense_delta_minor":2000,"one_off_minor":-50000,"one_off_month":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/analytics/scenarios/simulate", strings.NewReader(payload))
	req.SetBasicAuth("admin", "secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scenario status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result analytics.Scenario
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode scenario: %v", err)
	}
	if result.Currency != "EUR" || result.Months != 12 || len(result.Baseline) != 13 || len(result.Simulated) != 13 {
		t.Fatalf("scenario result = %#v", result)
	}
	if result.EndingDeltaMinor != 46000 {
		t.Fatalf("ending delta = %d, want 46000", result.EndingDeltaMinor)
	}
	if result.Simulated[0] != result.CurrentBalanceMinor || result.Baseline[0] != result.CurrentBalanceMinor {
		t.Fatalf("series start = %d/%d current=%d", result.Baseline[0], result.Simulated[0], result.CurrentBalanceMinor)
	}
}

func TestAnalyticsScenarioValidationAndAuth(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	unauth := f.request(t, http.MethodPost, "/api/analytics/scenarios/simulate", false, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated scenario status = %d", unauth.Code)
	}
	for _, body := range []string{
		`{"months":181}`,
		`{"months":12,"one_off_month":13}`,
		`{"months":"twelve"}`,
		`{"unknown_field":true}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/analytics/scenarios/simulate", strings.NewReader(body))
		req.SetBasicAuth("admin", "secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("payload %s status = %d, want 400", body, rec.Code)
		}
	}
}
