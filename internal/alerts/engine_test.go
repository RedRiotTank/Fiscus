package alerts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeAlertStore struct {
	mu          sync.Mutex
	byKey       map[string]domain.Alert
	nextID      int64
	delivered   map[int64]time.Time
	failed      map[int64]string
	createErr   error
	markErr     error
	createCalls int
}

func newFakeAlertStore() *fakeAlertStore {
	return &fakeAlertStore{
		byKey:     make(map[string]domain.Alert),
		nextID:    1,
		delivered: make(map[int64]time.Time),
		failed:    make(map[int64]string),
	}
}

func (f *fakeAlertStore) CreateAlert(_ context.Context, alert *domain.Alert) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.createErr != nil {
		return false, f.createErr
	}
	if _, exists := f.byKey[alert.DedupKey]; exists {
		return false, nil
	}
	alert.ID = f.nextID
	f.nextID++
	f.byKey[alert.DedupKey] = *alert
	return true, nil
}

func (f *fakeAlertStore) MarkAlertDelivered(_ context.Context, id int64, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.delivered[id] = at
	delete(f.failed, id)
	return nil
}

func (f *fakeAlertStore) MarkAlertDeliveryFailed(_ context.Context, id int64, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.failed[id] = msg
	return nil
}

func (f *fakeAlertStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.byKey)
}

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []domain.Alert
	err   error
}

func (f *fakeDispatcher) Dispatch(_ context.Context, alert domain.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, alert)
	return f.err
}

func (f *fakeDispatcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeEngineObserver struct {
	mu         sync.Mutex
	alerts     int
	deliveries int
	lastErr    error
}

func (f *fakeEngineObserver) ObserveAlert(domain.Alert) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts++
}

func (f *fakeEngineObserver) ObserveDelivery(_ domain.Alert, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveries++
	f.lastErr = err
}

type stubTxRule struct {
	name   string
	alerts []domain.Alert
	err    error
}

func (s stubTxRule) Name() string { return s.name }
func (s stubTxRule) Evaluate(context.Context, domain.Account, domain.Transaction) ([]domain.Alert, error) {
	return s.alerts, s.err
}

type stubBalanceRule struct {
	name   string
	alerts []domain.Alert
	err    error
}

func (s stubBalanceRule) Name() string { return s.name }
func (s stubBalanceRule) Evaluate(context.Context, domain.Account) ([]domain.Alert, error) {
	return s.alerts, s.err
}

func testEngine(t *testing.T, store AlertStore, dispatcher AlertDispatcher, observer EngineObserver, txRules []TransactionRule, balanceRules []BalanceRule) *Engine {
	t.Helper()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e, err := NewEngine(EngineConfig{
		Store:        store,
		Dispatcher:   dispatcher,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:          func() time.Time { return now },
		TxRules:      txRules,
		BalanceRules: balanceRules,
		Observer:     observer,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func TestEngineTransactionLifecycle(t *testing.T) {
	t.Parallel()
	store := newFakeAlertStore()
	dispatcher := &fakeDispatcher{}
	observer := &fakeEngineObserver{}
	tx := domain.Transaction{ID: 7, AccountID: "acc-1", AmountMinor: -100, Currency: "EUR"}
	rule := stubTxRule{name: "stub", alerts: []domain.Alert{{
		Rule: "stub", Severity: domain.SeverityWarning, Title: "t", Message: "m",
		DedupKey: "stub:acc-1:7",
	}}}
	e := testEngine(t, store, dispatcher, observer, []TransactionRule{rule}, nil)

	alerts, err := e.OnTransaction(context.Background(), tx, testAccount())
	if err != nil {
		t.Fatalf("OnTransaction: %v", err)
	}
	if len(alerts) != 1 || alerts[0].ID == 0 {
		t.Fatalf("alerts = %+v", alerts)
	}
	if alerts[0].AccountID != "acc-1" || alerts[0].TransactionID == nil || *alerts[0].TransactionID != 7 {
		t.Fatalf("alert metadata = %+v", alerts[0])
	}
	if alerts[0].DeliveredAt == nil {
		t.Fatal("alert was not marked delivered")
	}
	if dispatcher.count() != 1 {
		t.Fatalf("dispatch calls = %d", dispatcher.count())
	}
	if observer.alerts != 1 || observer.deliveries != 1 || observer.lastErr != nil {
		t.Fatalf("observer = %+v", observer)
	}

	// A repeated evaluation is persisted once and not dispatched again.
	alerts, err = e.OnTransaction(context.Background(), tx, testAccount())
	if err != nil {
		t.Fatalf("second OnTransaction: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("second alerts = %+v", alerts)
	}
	if store.count() != 1 || dispatcher.count() != 1 {
		t.Fatalf("dedupe failed: store=%d dispatch=%d", store.count(), dispatcher.count())
	}
}

func TestEngineDeliveryFailure(t *testing.T) {
	t.Parallel()
	store := newFakeAlertStore()
	dispatcher := &fakeDispatcher{err: errors.New("telegram down")}
	observer := &fakeEngineObserver{}
	rule := stubTxRule{name: "stub", alerts: []domain.Alert{{
		Rule: "stub", DedupKey: "stub:1",
	}}}
	e := testEngine(t, store, dispatcher, observer, []TransactionRule{rule}, nil)

	_, err := e.OnTransaction(context.Background(), domain.Transaction{ID: 1, AccountID: "acc-1"}, testAccount())
	if err == nil {
		t.Fatal("expected delivery error")
	}
	if store.failed[1] == "" {
		t.Fatalf("delivery failure not recorded: %+v", store.failed)
	}
	if observer.lastErr == nil {
		t.Fatal("observer did not receive delivery error")
	}
}

func TestEngineBalanceRulesAndRuleErrors(t *testing.T) {
	t.Parallel()
	store := newFakeAlertStore()
	dispatcher := &fakeDispatcher{}
	observer := &fakeEngineObserver{}
	balanceAlert := domain.Alert{Rule: RuleLiquidity, Severity: domain.SeverityCritical, DedupKey: "liq-1"}
	e := testEngine(t, store, dispatcher, observer, []TransactionRule{
		stubTxRule{name: "broken", err: errors.New("boom")},
		stubTxRule{name: "working", alerts: []domain.Alert{{Rule: "working", DedupKey: "tx-1"}}},
	}, []BalanceRule{
		stubBalanceRule{name: "broken-balance", err: errors.New("nope")},
		stubBalanceRule{name: "liquidity", alerts: []domain.Alert{balanceAlert}},
	})

	alerts, err := e.OnTransaction(context.Background(), domain.Transaction{ID: 1, AccountID: "acc-1"}, testAccount())
	if err == nil {
		t.Fatal("expected rule error")
	}
	if len(alerts) != 1 {
		t.Fatalf("transaction alerts = %d", len(alerts))
	}

	balanceAlerts, err := e.OnBalance(context.Background(), testAccount())
	if err == nil {
		t.Fatal("expected balance rule error")
	}
	if len(balanceAlerts) != 1 || balanceAlerts[0].AccountID != "acc-1" {
		t.Fatalf("balance alerts = %+v", balanceAlerts)
	}
	if store.count() != 2 || dispatcher.count() != 2 {
		t.Fatalf("store=%d dispatch=%d, want 2/2", store.count(), dispatcher.count())
	}
}

func TestEnginePersistenceErrors(t *testing.T) {
	t.Parallel()
	store := newFakeAlertStore()
	store.createErr = errors.New("db full")
	rule := stubTxRule{name: "stub", alerts: []domain.Alert{{Rule: "stub", DedupKey: "x"}}}
	e := testEngine(t, store, nil, nil, []TransactionRule{rule}, nil)
	if _, err := e.OnTransaction(context.Background(), domain.Transaction{ID: 1}, testAccount()); err == nil {
		t.Fatal("expected persistence error")
	}

	badKey := stubTxRule{name: "bad", alerts: []domain.Alert{{Rule: "bad"}}}
	e = testEngine(t, newFakeAlertStore(), nil, nil, []TransactionRule{badKey}, nil)
	if _, err := e.OnTransaction(context.Background(), domain.Transaction{ID: 1}, testAccount()); err == nil {
		t.Fatal("expected missing dedup key error")
	}

	if _, err := NewEngine(EngineConfig{}); err == nil {
		t.Fatal("expected store validation error")
	}
}

func TestEngineWithoutDispatcher(t *testing.T) {
	t.Parallel()
	store := newFakeAlertStore()
	rule := stubTxRule{name: "stub", alerts: []domain.Alert{{Rule: "stub", DedupKey: "x"}}}
	e := testEngine(t, store, nil, nil, []TransactionRule{rule}, nil)
	alerts, err := e.OnTransaction(context.Background(), domain.Transaction{ID: 1}, testAccount())
	if err != nil {
		t.Fatalf("OnTransaction: %v", err)
	}
	if len(alerts) != 1 || alerts[0].DeliveredAt != nil {
		t.Fatalf("alerts = %+v", alerts)
	}
	if store.failed[1] != "" {
		t.Fatalf("unexpected failure state: %+v", store.failed)
	}
}
