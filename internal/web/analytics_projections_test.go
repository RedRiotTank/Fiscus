package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsProjectionsAPI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Wealth &amp; savings projection") {
		t.Fatalf("analytics page missing projections: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "/static/analytics-projections.js") {
		t.Fatal("analytics page missing projections script")
	}

	projection := f.request(t, http.MethodGet, "/api/analytics/projections?yield_bp=300&extra_monthly_minor=1000", true, nil)
	if projection.Code != http.StatusOK {
		t.Fatalf("projection status = %d body=%s", projection.Code, projection.Body.String())
	}
	var payload analytics.Projections
	if err := json.Unmarshal(projection.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	if payload.CurrentBalanceMinor != 175050 || payload.ExtraMonthlyMinor != 1000 {
		t.Fatalf("projection payload = %#v", payload)
	}
	for _, horizon := range []string{"1y", "5y", "10y", "15y"} {
		if len(payload.Horizons[horizon].Baseline) == 0 {
			t.Fatalf("missing horizon %s", horizon)
		}
	}

	invalid := f.request(t, http.MethodGet, "/api/analytics/projections?yield_bp=nope", true, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid projection status = %d", invalid.Code)
	}
}
