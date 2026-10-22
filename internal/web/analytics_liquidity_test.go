package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsLiquidityAPI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Institution distribution") {
		t.Fatalf("analytics page missing liquidity: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "/static/analytics-liquidity.js") {
		t.Fatal("analytics page missing liquidity script")
	}

	liquidity := f.request(t, http.MethodGet, "/api/analytics/liquidity?days=90", true, nil)
	if liquidity.Code != http.StatusOK {
		t.Fatalf("liquidity status = %d body=%s", liquidity.Code, liquidity.Body.String())
	}
	var payload analytics.Liquidity
	if err := json.Unmarshal(liquidity.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode liquidity: %v", err)
	}
	if len(payload.Labels) != 90 || len(payload.Series) != 1 {
		t.Fatalf("liquidity payload = %#v", payload)
	}
	if payload.Series[0].Institution != "Dev Bank" {
		t.Fatalf("liquidity institution = %q", payload.Series[0].Institution)
	}

	invalid := f.request(t, http.MethodGet, "/api/analytics/liquidity?days=30", true, nil)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid liquidity days status = %d", invalid.Code)
	}
}
