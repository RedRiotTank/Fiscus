package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsPageAndSpendingAPI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("analytics page status = %d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{
		"Analytics workspace", "Spending intelligence", "Category breakdown",
		"/static/chart.umd.min.js", "/static/analytics-core.js", "/static/analytics-spending.js",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("analytics page missing %q", want)
		}
	}
	if !strings.Contains(body, `<a href="/analytics" class="active">Analytics</a>`) {
		t.Error("analytics navigation is not active")
	}

	unauth := f.request(t, http.MethodGet, "/api/analytics/spending", false, nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated analytics status = %d", unauth.Code)
	}

	spending := f.request(t, http.MethodGet, "/api/analytics/spending?period=month", true, nil)
	if spending.Code != http.StatusOK {
		t.Fatalf("spending status = %d body=%s", spending.Code, spending.Body.String())
	}
	if got := spending.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("spending content type = %q", got)
	}
	var spendingPayload analytics.Spending
	if err := json.Unmarshal(spending.Body.Bytes(), &spendingPayload); err != nil {
		t.Fatalf("decode spending: %v", err)
	}
	if spendingPayload.Currency != "EUR" || spendingPayload.Period != "month" {
		t.Fatalf("spending metadata = %#v", spendingPayload)
	}
	if len(spendingPayload.Categories) == 0 || spendingPayload.Categories[0].Category != "Dining" {
		t.Fatalf("spending categories = %#v", spendingPayload.Categories)
	}
	if spendingPayload.Categories[0].Minor != 1250 || spendingPayload.TotalMinor != 1250 {
		t.Fatalf("spending total = %d categories=%#v", spendingPayload.TotalMinor, spendingPayload.Categories)
	}
	if len(spendingPayload.Trend) != 6 || len(spendingPayload.Waterfall.Outflows) != 1 {
		t.Fatalf("spending trend/waterfall = %#v", spendingPayload)
	}

	invalidPeriod := f.request(t, http.MethodGet, "/api/analytics/spending?period=week", true, nil)
	if invalidPeriod.Code != http.StatusBadRequest {
		t.Fatalf("invalid period status = %d", invalidPeriod.Code)
	}
}

func TestAnalyticsStaticAssets(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)
	chart := f.request(t, http.MethodGet, "/static/chart.umd.min.js", true, nil)
	if chart.Code != http.StatusOK {
		t.Fatalf("chart.js status = %d", chart.Code)
	}
	if !strings.Contains(chart.Body.String(), "Chart") {
		t.Fatal("chart.js bundle looks invalid")
	}
	script := f.request(t, http.MethodGet, "/static/analytics-core.js", true, nil)
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "FiscusAnalytics") {
		t.Fatalf("analytics core status = %d", script.Code)
	}
	spending := f.request(t, http.MethodGet, "/static/analytics-spending.js", true, nil)
	if spending.Code != http.StatusOK || !strings.Contains(spending.Body.String(), "spending-donut") {
		t.Fatalf("spending script status = %d", spending.Code)
	}
}
