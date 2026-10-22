package budgets

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/alerts"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestComputeBudgetStatuses(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	spending := []storage.CategorySpending{
		{Category: "Restaurants", Currency: "EUR", Minor: 2000},
		{Category: "Transport", Currency: "EUR", Minor: 2500},
		{Category: "Restaurants", Currency: "USD", Minor: 9000},
	}
	budgets := []Budget{
		{Category: "Restaurants", LimitMinor: 2500, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Transport", LimitMinor: 10000, Currency: "EUR"},
	}
	statuses := Compute(spending, budgets, now)
	if len(statuses) != 2 {
		t.Fatalf("statuses = %+v", statuses)
	}
	if statuses[0].Percent != 80 || statuses[0].Tone != ToneWarning || statuses[0].BarWidth != 80 {
		t.Fatalf("restaurants = %+v", statuses[0])
	}
	if statuses[1].Percent != 25 || statuses[1].Tone != ToneHealthy {
		t.Fatalf("transport = %+v", statuses[1])
	}
	if statuses[0].MonthElapsedPercent != 32 {
		t.Fatalf("month elapsed = %d, want 32", statuses[0].MonthElapsedPercent)
	}
}

func TestComputeCriticalAndOverBudget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	statuses := Compute([]storage.CategorySpending{{Category: "Dining", Currency: "EUR", Minor: 12500}},
		[]Budget{{Category: "Dining", LimitMinor: 10000, Currency: "EUR"}}, now)
	if len(statuses) != 1 {
		t.Fatalf("statuses = %+v", statuses)
	}
	got := statuses[0]
	if got.Percent != 125 || got.BarWidth != 100 || got.Tone != ToneCritical || got.RemainingMinor != -2500 {
		t.Fatalf("status = %+v", got)
	}
	if got.MonthElapsedPercent != 100 {
		t.Fatalf("month elapsed = %d, want 100", got.MonthElapsedPercent)
	}
}

func TestNormalizeBudget(t *testing.T) {
	t.Parallel()
	got := Normalize(Budget{Category: "  Dining ", NotifyAtPercent: []int{100, 80, 80}})
	if got.Category != "Dining" || got.Currency != "EUR" {
		t.Fatalf("normalized = %+v", got)
	}
	if len(got.NotifyAtPercent) != 2 || got.NotifyAtPercent[0] != 80 || got.NotifyAtPercent[1] != 100 {
		t.Fatalf("thresholds = %+v", got.NotifyAtPercent)
	}
}

type fakeSpender struct {
	spent int64
	err   error
}

func (f *fakeSpender) SpendingByCategory(context.Context, time.Time, time.Time) ([]storage.CategorySpending, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []storage.CategorySpending{{Category: "Restaurants", Currency: "EUR", Minor: f.spent}}, nil
}

type fakeAlertStore struct {
	alerts map[string]domain.Alert
	next   int64
}

func newFakeAlertStore() *fakeAlertStore {
	return &fakeAlertStore{alerts: make(map[string]domain.Alert), next: 1}
}

func (f *fakeAlertStore) CreateAlert(_ context.Context, alert *domain.Alert) (bool, error) {
	if _, ok := f.alerts[alert.DedupKey]; ok {
		return false, nil
	}
	alert.ID = f.next
	f.next++
	f.alerts[alert.DedupKey] = *alert
	return true, nil
}

func (f *fakeAlertStore) MarkAlertDelivered(context.Context, int64, time.Time) error { return nil }
func (f *fakeAlertStore) MarkAlertDeliveryFailed(context.Context, int64, string) error {
	return nil
}

type fakeDispatcher struct {
	alerts []domain.Alert
}

func (f *fakeDispatcher) Dispatch(_ context.Context, alert domain.Alert) error {
	f.alerts = append(f.alerts, alert)
	return nil
}

func TestBudgetRuleTransitionsAndDeduplication(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	spender := &fakeSpender{spent: 7900}
	rule, err := NewRule(spender, []Budget{{
		Category: "Restaurants", LimitMinor: 10000, Currency: "EUR", NotifyAtPercent: []int{80, 100},
	}}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewRule: %v", err)
	}
	alertStore := newFakeAlertStore()
	dispatcher := &fakeDispatcher{}
	engine, err := alerts.NewEngine(alerts.EngineConfig{
		Store:         alertStore,
		Dispatcher:    dispatcher,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now },
		AnalysisRules: []alerts.AnalysisRule{rule},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if _, err := engine.OnAnalysis(context.Background()); err != nil {
		t.Fatalf("OnAnalysis 79: %v", err)
	}
	if len(dispatcher.alerts) != 0 {
		t.Fatalf("alerts at 79%% = %+v", dispatcher.alerts)
	}

	spender.spent = 8000
	if _, err := engine.OnAnalysis(context.Background()); err != nil {
		t.Fatalf("OnAnalysis 80: %v", err)
	}
	if len(dispatcher.alerts) != 1 || dispatcher.alerts[0].Severity != domain.SeverityWarning {
		t.Fatalf("alerts at 80%% = %+v", dispatcher.alerts)
	}
	if _, err := engine.OnAnalysis(context.Background()); err != nil {
		t.Fatalf("OnAnalysis 80 repeat: %v", err)
	}
	if len(dispatcher.alerts) != 1 {
		t.Fatalf("deduplication failed: %+v", dispatcher.alerts)
	}

	spender.spent = 10000
	if _, err := engine.OnAnalysis(context.Background()); err != nil {
		t.Fatalf("OnAnalysis 100: %v", err)
	}
	if len(dispatcher.alerts) != 2 || dispatcher.alerts[1].Severity != domain.SeverityCritical {
		t.Fatalf("alerts at 100%% = %+v", dispatcher.alerts)
	}
	if _, err := engine.OnAnalysis(context.Background()); err != nil {
		t.Fatalf("OnAnalysis 100 repeat: %v", err)
	}
	if len(dispatcher.alerts) != 2 {
		t.Fatalf("critical deduplication failed: %+v", dispatcher.alerts)
	}
}
