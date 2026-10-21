package observability

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestMetricsExposition(t *testing.T) {
	t.Parallel()
	metrics := New("test")
	metrics.ObserveSync("req-1", 1500*time.Millisecond, nil)
	metrics.ObserveSync("req-1", 2*time.Second, errors.New("boom"))
	metrics.ObserveTransactions("acc-1", 3)
	metrics.ObserveTransactions("acc-1", 0)
	metrics.ObserveBalance(domain.Account{ID: "acc-1", Currency: "EUR", BalanceMinor: 12345, BalanceType: "closingBooked"})
	metrics.ObserveAlert(domain.Alert{Rule: "duplicate", Severity: domain.SeverityWarning})
	metrics.ObserveDelivery(domain.Alert{Rule: "duplicate"}, nil)
	metrics.ObserveDelivery(domain.Alert{Rule: "duplicate"}, errors.New("failed"))
	metrics.DeliveryObserver().ObserveDelivery("telegram", true)
	metrics.DeliveryObserver().ObserveDelivery("discord", false)

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"fiscus_build_info{version=\"test\"} 1",
		"fiscus_sync_total{requisition=\"req-1\",result=\"success\"} 1",
		"fiscus_sync_total{requisition=\"req-1\",result=\"error\"} 1",
		"fiscus_transactions_ingested_total{account=\"acc-1\"} 3",
		"fiscus_account_balance_minor_units{account=\"acc-1\",currency=\"EUR\",type=\"closingBooked\"} 12345",
		"fiscus_alerts_total{rule=\"duplicate\",severity=\"warning\"} 1",
		"fiscus_alert_dispatch_total{result=\"success\",rule=\"duplicate\"} 1",
		"fiscus_alert_dispatch_total{result=\"error\",rule=\"duplicate\"} 1",
		"fiscus_alert_deliveries_total{channel=\"telegram\",result=\"success\"} 1",
		"fiscus_alert_deliveries_total{channel=\"discord\",result=\"error\"} 1",
		"fiscus_sync_duration_seconds_count{requisition=\"req-1\"} 2",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}
