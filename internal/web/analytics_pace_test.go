package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsPaceAPI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Burn-up spending curve") {
		t.Fatalf("analytics page missing pace: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "/static/analytics-pace.js") {
		t.Fatal("analytics page missing pace script")
	}

	pace := f.request(t, http.MethodGet, "/api/analytics/pace", true, nil)
	if pace.Code != http.StatusOK {
		t.Fatalf("pace status = %d body=%s", pace.Code, pace.Body.String())
	}
	var payload analytics.Pace
	if err := json.Unmarshal(pace.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode pace: %v", err)
	}
	if len(payload.Current) != 31 || len(payload.Baseline) != 6 {
		t.Fatalf("pace payload = %#v", payload)
	}
	if payload.SpentMinor != 1250 {
		t.Fatalf("pace spent = %d", payload.SpentMinor)
	}
}
