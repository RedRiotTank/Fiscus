package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeConsentStore struct {
	connections []domain.Connection
	err         error
}

func (f *fakeConsentStore) ListConnections(context.Context) ([]domain.Connection, error) {
	return f.connections, f.err
}

func TestConsentRuleThresholds(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	expires := func(days int) *time.Time {
		value := now.AddDate(0, 0, days)
		return &value
	}
	store := &fakeConsentStore{connections: []domain.Connection{
		{ID: 1, RequisitionID: "session-14", InstitutionName: "Bank 14", Status: "LN", ConsentExpiresAt: expires(13)},
		{ID: 2, RequisitionID: "session-3", InstitutionName: "Bank 3", Status: "LN", ConsentExpiresAt: expires(2)},
		{ID: 3, RequisitionID: "session-far", InstitutionName: "Bank far", Status: "LN", ConsentExpiresAt: expires(15)},
		{ID: 4, RequisitionID: "session-expired", InstitutionName: "Bank expired", Status: "LN", ConsentExpiresAt: expires(-1)},
		{ID: 5, RequisitionID: "session-pending", InstitutionName: "Bank pending", Status: "PENDING_AUTHORIZATION", ConsentExpiresAt: expires(2)},
		{ID: 6, RequisitionID: "session-unknown", InstitutionName: "Bank unknown", Status: "LN"},
	}}
	rule, err := NewConsentRule(store, func() time.Time { return now }, func(domain.Connection) string {
		return "https://fiscus.example/connections"
	})
	if err != nil {
		t.Fatal(err)
	}

	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("alerts = %d, want 2: %#v", len(alerts), alerts)
	}
	byKey := make(map[string]domain.Alert, len(alerts))
	for _, alert := range alerts {
		byKey[alert.DedupKey] = alert
		if alert.Rule != RuleConsentExpiration {
			t.Errorf("rule = %q", alert.Rule)
		}
		if alert.Severity != domain.SeverityWarning {
			t.Errorf("severity = %q", alert.Severity)
		}
		if !strings.Contains(alert.Message, "https://fiscus.example/connections") {
			t.Errorf("message missing renewal URL: %q", alert.Message)
		}
	}
	if _, ok := byKey[consentDedupKey(store.connections[0], expires(13).UTC(), consentWarningDays)]; !ok {
		t.Errorf("missing 14-day alert: %#v", alerts)
	}
	if _, ok := byKey[consentDedupKey(store.connections[1], expires(2).UTC(), consentUrgentDays)]; !ok {
		t.Errorf("missing 3-day alert: %#v", alerts)
	}
}

func TestEngineConsentCheckPersistsAndDeduplicates(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := now.AddDate(0, 0, 2)
	store := &fakeConsentStore{connections: []domain.Connection{{
		ID: 1, RequisitionID: "session-1", InstitutionName: "Bank", Status: "LN", ConsentExpiresAt: &expiresAt,
	}}}
	alertStore := newFakeAlertStore()
	dispatcher := &fakeDispatcher{}
	rule, err := NewConsentRule(store, func() time.Time { return now }, func(domain.Connection) string {
		return "https://fiscus.example/connections"
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(EngineConfig{
		Store:        alertStore,
		Dispatcher:   dispatcher,
		ConsentRules: []ConsentRule{rule},
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := engine.OnConsentCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("first alerts = %d, want 1", len(first))
	}
	second, err := engine.OnConsentCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("second alerts = %d, want 1", len(second))
	}
	if alertStore.count() != 1 {
		t.Fatalf("persisted alerts = %d, want 1", alertStore.count())
	}
	if dispatcher.count() != 1 {
		t.Fatalf("dispatched alerts = %d, want 1", dispatcher.count())
	}
}
