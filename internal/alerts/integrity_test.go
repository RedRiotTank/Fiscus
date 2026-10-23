package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestIntegrityRuleFormatsAndDeduplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newFakeAlertStore()
	dispatcher := &fakeDispatcher{}
	engine, err := NewEngine(EngineConfig{
		Store:               store,
		Dispatcher:          dispatcher,
		ReconciliationRules: []ReconciliationRule{NewIntegrityRule()},
		Now:                 func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	start := time.Date(2026, 3, 9, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	transition := domain.ReconciliationTransition{
		Changed: true,
		Reconciliation: domain.Reconciliation{
			AccountID: "acc-1", Currency: "EUR", LastDeltaMinor: 1250,
			Status:           domain.ReconciliationStatusDiscrepancy,
			DiscrepancyStart: &start, DiscrepancyEnd: &end,
			UpdatedAt: end,
		},
	}
	alerts, err := engine.OnReconciliation(ctx, transition)
	if err != nil {
		t.Fatalf("OnReconciliation: %v", err)
	}
	if len(alerts) != 1 || store.count() != 1 || dispatcher.count() != 1 {
		t.Fatalf("alerts=%d stored=%d dispatched=%d", len(alerts), store.count(), dispatcher.count())
	}
	alert := alerts[0]
	if alert.Rule != RuleReconciliation || !strings.Contains(alert.Message, "12.50") ||
		!strings.Contains(alert.Message, "09 Mar 2026 08:00 UTC") {
		t.Fatalf("alert = %+v", alert)
	}
	if _, err := engine.OnReconciliation(ctx, transition); err != nil {
		t.Fatalf("duplicate OnReconciliation: %v", err)
	}
	if store.count() != 1 || dispatcher.count() != 1 {
		t.Fatal("unchanged transition was re-dispatched")
	}

	resolved := transition
	resolved.Reconciliation.Status = domain.ReconciliationStatusReconciled
	resolved.Reconciliation.LastDeltaMinor = 0
	alerts, err = engine.OnReconciliation(ctx, resolved)
	if err != nil {
		t.Fatalf("resolved OnReconciliation: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatal("resolution should not produce a discrepancy alert")
	}
}
