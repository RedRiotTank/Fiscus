package alerts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeFinder struct {
	candidates []domain.Transaction
	err        error
	calls      int
	from       time.Time
	to         time.Time
}

func (f *fakeFinder) FindDuplicateCandidates(_ context.Context, _, _ string, _ int64, from, to time.Time, _ int64) ([]domain.Transaction, error) {
	f.calls++
	f.from = from
	f.to = to
	return f.candidates, f.err
}

type fakeStats struct {
	total int64
	count int
	err   error
	calls int
	from  time.Time
	to    time.Time
}

func (f *fakeStats) DebitStats(_ context.Context, _ string, from, to time.Time, _ int64) (int64, int, error) {
	f.calls++
	f.from = from
	f.to = to
	return f.total, f.count, f.err
}

func testAccount() domain.Account {
	return domain.Account{ID: "acc-1", Name: "Everyday", Currency: "EUR"}
}

func TestDuplicateRule(t *testing.T) {
	t.Parallel()
	booking := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	finder := &fakeFinder{candidates: []domain.Transaction{{
		ID: 5, AccountID: "acc-1", AmountMinor: -4200, Currency: "EUR",
		Counterparty: "Power Co", BookingDate: booking.Add(-2 * time.Hour),
	}}}
	rule, err := NewDuplicateRule(finder, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewDuplicateRule: %v", err)
	}
	if rule.Name() != RuleDuplicate {
		t.Fatalf("name = %q", rule.Name())
	}
	tx := domain.Transaction{
		ID: 10, AccountID: "acc-1", AmountMinor: -4200, Currency: "EUR",
		Counterparty: "  Power Co ", BookingDate: booking,
	}
	alerts, err := rule.Evaluate(context.Background(), testAccount(), tx)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Rule != RuleDuplicate || a.Severity != domain.SeverityWarning {
		t.Fatalf("unexpected alert: %+v", a)
	}
	if a.DedupKey != "duplicate:acc-1:5:10" {
		t.Fatalf("dedup key = %q", a.DedupKey)
	}
	if a.TransactionID == nil || *a.TransactionID != 10 {
		t.Fatalf("transaction ID = %v", a.TransactionID)
	}
	if !strings.Contains(a.Message, "Power Co") || !strings.Contains(a.Message, "42.00") {
		t.Fatalf("message = %q", a.Message)
	}
	if !finder.from.Equal(booking.Add(-24*time.Hour)) || !finder.to.Equal(booking.Add(24*time.Hour)) {
		t.Fatalf("window = %v..%v", finder.from, finder.to)
	}

	// The same pair must produce a stable key regardless of evaluation order.
	reverse, err := NewDuplicateRule(&fakeFinder{candidates: []domain.Transaction{{
		ID: 10, AmountMinor: -4200, Currency: "EUR", BookingDate: booking,
	}}}, 24*time.Hour)
	if err != nil {
		t.Fatalf("NewDuplicateRule: %v", err)
	}
	revAlerts, err := reverse.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 5, AccountID: "acc-1", AmountMinor: -4200, Currency: "EUR",
		Counterparty: "Power Co", BookingDate: booking,
	})
	if err != nil {
		t.Fatalf("Evaluate reverse: %v", err)
	}
	if len(revAlerts) != 1 || revAlerts[0].DedupKey != a.DedupKey {
		t.Fatalf("reverse dedup key = %+v", revAlerts)
	}
}

func TestDuplicateRuleSkipsIrrelevantTransactions(t *testing.T) {
	t.Parallel()
	finder := &fakeFinder{candidates: []domain.Transaction{{ID: 1}}}
	rule, err := NewDuplicateRule(finder, 0)
	if err != nil {
		t.Fatalf("NewDuplicateRule: %v", err)
	}
	if rule.window != 24*time.Hour {
		t.Fatalf("default window = %v", rule.window)
	}
	cases := []domain.Transaction{
		{ID: 2, AmountMinor: 4200, Counterparty: "Shop"}, // credit
		{ID: 3, AmountMinor: -4200, Counterparty: "   "}, // missing counterparty
		{ID: 4, AmountMinor: 0, Counterparty: "Shop"},    // no amount
	}
	for _, tx := range cases {
		alerts, err := rule.Evaluate(context.Background(), testAccount(), tx)
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if len(alerts) != 0 {
			t.Fatalf("unexpected alerts for %+v: %+v", tx, alerts)
		}
	}
	if finder.calls != 0 {
		t.Fatalf("finder calls = %d, want 0", finder.calls)
	}

	// No candidates and repository errors are handled.
	ruleNoDup, _ := NewDuplicateRule(&fakeFinder{}, time.Hour)
	alerts, err := ruleNoDup.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 1, AmountMinor: -1, Currency: "EUR", Counterparty: "Shop",
	})
	if err != nil || len(alerts) != 0 {
		t.Fatalf("no duplicates: alerts=%v err=%v", alerts, err)
	}
	ruleErr, _ := NewDuplicateRule(&fakeFinder{err: errors.New("db down")}, time.Hour)
	if _, err := ruleErr.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 1, AmountMinor: -1, Currency: "EUR", Counterparty: "Shop",
	}); err == nil {
		t.Fatal("expected repository error")
	}
	if _, err := NewDuplicateRule(nil, time.Hour); err == nil {
		t.Fatal("expected nil finder error")
	}
}

func TestAnomalyRule(t *testing.T) {
	t.Parallel()
	booking := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	stats := &fakeStats{total: 3000, count: 3} // average 10.00
	rule, err := NewAnomalyRule(stats, AnomalyConfig{Threshold: 0.30, LookbackDays: 90, MinHistory: 3})
	if err != nil {
		t.Fatalf("NewAnomalyRule: %v", err)
	}
	if rule.Name() != RuleAnomaly {
		t.Fatalf("name = %q", rule.Name())
	}
	tx := domain.Transaction{
		ID: 20, AccountID: "acc-1", AmountMinor: -1400, Currency: "EUR", BookingDate: booking,
	}
	alerts, err := rule.Evaluate(context.Background(), testAccount(), tx)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	if alerts[0].DedupKey != "anomaly:acc-1:20" {
		t.Fatalf("dedup key = %q", alerts[0].DedupKey)
	}
	if !strings.Contains(alerts[0].Message, "40% above") {
		t.Fatalf("message = %q", alerts[0].Message)
	}
	if !stats.from.Equal(booking.AddDate(0, 0, -90)) || !stats.to.Equal(booking) {
		t.Fatalf("lookback = %v..%v", stats.from, stats.to)
	}

	// Exactly 30% is not "more than 30%".
	stats.total, stats.count = 3000, 3
	exact, _ := NewAnomalyRule(stats, AnomalyConfig{Threshold: 0.30, MinHistory: 3})
	alerts, err = exact.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 21, AccountID: "acc-1", AmountMinor: -1300, Currency: "EUR", BookingDate: booking,
	})
	if err != nil || len(alerts) != 0 {
		t.Fatalf("exact threshold: alerts=%v err=%v", alerts, err)
	}

	// Not enough history.
	small, _ := NewAnomalyRule(&fakeStats{total: 2000, count: 2}, AnomalyConfig{MinHistory: 3})
	alerts, _ = small.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 22, AmountMinor: -9000, Currency: "EUR", BookingDate: booking,
	})
	if len(alerts) != 0 {
		t.Fatalf("insufficient history produced alerts: %+v", alerts)
	}

	// Credits are ignored without touching the repository.
	ignored := &fakeStats{total: 1, count: 100}
	creditRule, _ := NewAnomalyRule(ignored, AnomalyConfig{})
	alerts, _ = creditRule.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 23, AmountMinor: 9000, Currency: "EUR", BookingDate: booking,
	})
	if len(alerts) != 0 || ignored.calls != 0 {
		t.Fatalf("credit evaluated: alerts=%v calls=%d", alerts, ignored.calls)
	}

	// Repository errors propagate.
	failing, _ := NewAnomalyRule(&fakeStats{err: errors.New("db down")}, AnomalyConfig{})
	if _, err := failing.Evaluate(context.Background(), testAccount(), tx); err == nil {
		t.Fatal("expected repository error")
	}
	if _, err := NewAnomalyRule(nil, AnomalyConfig{}); err == nil {
		t.Fatal("expected nil stats error")
	}
}

func TestAnomalyRuleDefaultsAndLargeAmounts(t *testing.T) {
	t.Parallel()
	rule, err := NewAnomalyRule(&fakeStats{total: 1, count: 1}, AnomalyConfig{})
	if err != nil {
		t.Fatalf("NewAnomalyRule: %v", err)
	}
	if rule.thresholdBPS != 3000 || rule.lookbackDays != 90 || rule.minHistory != 3 {
		t.Fatalf("defaults = bps:%d lookback:%d min:%d", rule.thresholdBPS, rule.lookbackDays, rule.minHistory)
	}

	large := &fakeStats{total: 100, count: 100} // average 1
	r, err := NewAnomalyRule(large, AnomalyConfig{Threshold: 0.30, MinHistory: 1})
	if err != nil {
		t.Fatalf("NewAnomalyRule: %v", err)
	}
	alerts, err := r.Evaluate(context.Background(), testAccount(), domain.Transaction{
		ID: 1, AmountMinor: -1 << 62, Currency: "EUR",
		BookingDate: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
	})
	if err != nil || len(alerts) != 1 {
		t.Fatalf("large amount: alerts=%v err=%v", alerts, err)
	}
}

func TestLiquidityRule(t *testing.T) {
	t.Parallel()
	rule := NewLiquidityRule(map[string]int64{"EUR": 50000, "usd": 10000, "JPY": 0})
	if rule.Name() != RuleLiquidity {
		t.Fatalf("name = %q", rule.Name())
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	account := testAccount()
	account.BalanceMinor = 25000
	account.UpdatedAt = now

	alerts, err := rule.Evaluate(context.Background(), account)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Severity != domain.SeverityCritical || a.Rule != RuleLiquidity {
		t.Fatalf("unexpected alert: %+v", a)
	}
	if a.DedupKey != "liquidity:acc-1:2026-03-10" {
		t.Fatalf("dedup key = %q", a.DedupKey)
	}
	if !strings.Contains(a.Message, "250.00 EUR") || !strings.Contains(a.Message, "500.00 EUR") {
		t.Fatalf("message = %q", a.Message)
	}

	account.BalanceMinor = 50000
	alerts, _ = rule.Evaluate(context.Background(), account)
	if len(alerts) != 0 {
		t.Fatalf("threshold equality produced alert: %+v", alerts)
	}
	account.BalanceMinor = 1
	account.Currency = "GBP"
	alerts, _ = rule.Evaluate(context.Background(), account)
	if len(alerts) != 0 {
		t.Fatalf("unknown currency produced alert: %+v", alerts)
	}
	account.Currency = "EUR"
	account.UpdatedAt = time.Time{}
	alerts, _ = rule.Evaluate(context.Background(), account)
	if len(alerts) != 0 {
		t.Fatalf("zero UpdatedAt produced alert: %+v", alerts)
	}

	empty := NewLiquidityRule(nil)
	alerts, _ = empty.Evaluate(context.Background(), account)
	if len(alerts) != 0 {
		t.Fatalf("empty rule produced alert: %+v", alerts)
	}
}
