package monitoring

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeReader struct {
	transactions []domain.Transaction
	err          error
	from         time.Time
	to           time.Time
	calls        int
}

func (f *fakeReader) ListTransactionsBetween(_ context.Context, from, to time.Time) ([]domain.Transaction, error) {
	f.calls++
	f.from = from
	f.to = to
	return f.transactions, f.err
}

func TestInflowRuleRaisesMissingAlert(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
	reader := &fakeReader{}
	rule, err := NewInflowRule(reader, []Inflow{{
		Name: "Payroll", AmountMin: 200000, Currency: "EUR", ExpectedDays: []int{1}, GraceDays: 2,
	}}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewInflowRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
	got := alerts[0]
	if got.Rule != RuleExpectedInflow || got.Severity != domain.SeverityWarning {
		t.Fatalf("alert = %+v", got)
	}
	if got.DedupKey != "inflow:payroll:EUR:2026-04" {
		t.Fatalf("dedup key = %q", got.DedupKey)
	}
	if !strings.Contains(got.Message, "2,000.00 EUR") || !strings.Contains(got.Message, "2-day grace") {
		t.Fatalf("message = %q", got.Message)
	}
	if reader.calls != 1 || !reader.from.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("query window = %v -> %v calls=%d", reader.from, reader.to, reader.calls)
	}
	if !reader.to.Equal(time.Date(2026, 4, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window end = %v", reader.to)
	}
}

func TestInflowRuleFindsMatchingCredit(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{transactions: []domain.Transaction{{
		Counterparty: "ACME PAYROLL", Description: "Salary April", AmountMinor: 200000,
		Currency: "EUR", Status: domain.TransactionStatusBooked,
	}}}
	rule, err := NewInflowRule(reader, []Inflow{{
		Name: "Payroll", AmountMin: 200000, ExpectedDays: []int{1}, GraceDays: 2,
	}}, func() time.Time { return time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("NewInflowRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestInflowRuleWaitsForGracePeriod(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{}
	rule, err := NewInflowRule(reader, []Inflow{{
		Name: "Payroll", AmountMin: 100, ExpectedDays: []int{1}, GraceDays: 2,
	}}, func() time.Time { return time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("NewInflowRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 0 || reader.calls != 0 {
		t.Fatalf("alerts = %+v calls=%d", alerts, reader.calls)
	}
}

func TestInflowRuleRejectsSmallOrWrongCurrencyCredits(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{transactions: []domain.Transaction{
		{Counterparty: "Payroll", AmountMinor: 99999, Currency: "EUR", Status: domain.TransactionStatusBooked},
		{Counterparty: "Payroll", AmountMinor: 200000, Currency: "USD", Status: domain.TransactionStatusBooked},
		{Counterparty: "Payroll", AmountMinor: 200000, Currency: "EUR", Status: domain.TransactionStatusPending},
	}}
	rule, err := NewInflowRule(reader, []Inflow{{
		Name: "Payroll", AmountMin: 100000, Currency: "EUR", ExpectedDays: []int{1}, GraceDays: 2,
	}}, func() time.Time { return time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("NewInflowRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestInflowRulePropagatesReaderErrors(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{err: errors.New("database unavailable")}
	rule, err := NewInflowRule(reader, []Inflow{{
		Name: "Payroll", AmountMin: 100, ExpectedDays: []int{1}, GraceDays: 2,
	}}, func() time.Time { return time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatalf("NewInflowRule: %v", err)
	}
	if _, err := rule.Evaluate(context.Background()); err == nil {
		t.Fatal("expected reader error")
	}
}

type fakeSnapshotProvider struct {
	snapshot analysis.Snapshot
	err      error
}

func (f *fakeSnapshotProvider) Snapshot(context.Context) (analysis.Snapshot, error) {
	return f.snapshot, f.err
}

func TestGhostSubscriptionRuleNotifiesInactive(t *testing.T) {
	t.Parallel()
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	provider := &fakeSnapshotProvider{snapshot: analysis.Snapshot{Subscriptions: []analysis.Subscription{
		{
			AccountID: "acc-1", Counterparty: "Old Gym", Currency: "EUR", Frequency: analysis.Monthly,
			AmountMinor: 3000, LastCharge: last, Active: false,
		},
		{
			AccountID: "acc-1", Counterparty: "Streaming", Currency: "EUR", Frequency: analysis.Monthly,
			AmountMinor: 999, LastCharge: last, Active: true,
		},
	}}}
	rule, err := NewGhostSubscriptionRule(provider, func() time.Time {
		return time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewGhostSubscriptionRule: %v", err)
	}
	alerts, err := rule.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
	got := alerts[0]
	if got.Rule != RuleGhostSubscription || !strings.Contains(got.Title, "Old Gym") {
		t.Fatalf("alert = %+v", got)
	}
	if !strings.Contains(got.Message, "Potential Ghost") || !strings.Contains(got.Message, "90 days") {
		t.Fatalf("message = %q", got.Message)
	}
	if got.DedupKey != "subscription-ghost:acc-1:old gym:2026-01-01" {
		t.Fatalf("dedup key = %q", got.DedupKey)
	}
}

func TestGhostSubscriptionRulePropagatesErrors(t *testing.T) {
	t.Parallel()
	rule, err := NewGhostSubscriptionRule(&fakeSnapshotProvider{err: errors.New("analysis failed")}, nil)
	if err != nil {
		t.Fatalf("NewGhostSubscriptionRule: %v", err)
	}
	if _, err := rule.Evaluate(context.Background()); err == nil {
		t.Fatal("expected snapshot error")
	}
}
