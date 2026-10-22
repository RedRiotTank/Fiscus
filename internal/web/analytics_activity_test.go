package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/analytics"
)

func TestAnalyticsActivityAPI(t *testing.T) {
	t.Parallel()
	f := newWebFixture(t)

	page := f.request(t, http.MethodGet, "/analytics", true, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Activity matrix") {
		t.Fatalf("analytics page missing activity: %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "/static/analytics-activity.js") {
		t.Fatal("analytics page missing activity script")
	}

	activity := f.request(t, http.MethodGet, "/api/analytics/activity", true, nil)
	if activity.Code != http.StatusOK {
		t.Fatalf("activity status = %d body=%s", activity.Code, activity.Body.String())
	}
	var payload analytics.Activity
	if err := json.Unmarshal(activity.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode activity: %v", err)
	}
	if len(payload.Buckets) != 5 || payload.Transactions != 1 {
		t.Fatalf("activity payload = %#v", payload)
	}
}
