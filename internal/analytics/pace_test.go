package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputePace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		testTx(1, "acc-1", -1000, time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(2, "acc-1", -500, time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(3, "acc-1", -1500, time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(4, "acc-1", -9999, time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusPending),
	}
	previousMonths := []time.Time{
		time.Date(2025, 11, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 12, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
	}
	for i, date := range previousMonths {
		transactions = append(transactions, testTx(int64(10+i), "acc-1", -1000, date, "Dining", domain.TransactionStatusBooked))
	}
	pace := ComputePace(transactions, now, "EUR")
	if pace.SpentMinor != 3000 || pace.Today != 10 {
		t.Fatalf("pace = %#v", pace)
	}
	if pace.Current[9] == nil || *pace.Current[9] != 3000 {
		t.Fatalf("current day 10 = %#v", pace.Current[9])
	}
	if pace.Current[10] != nil {
		t.Fatal("future day should be nil")
	}
	if len(pace.Baseline) != 6 {
		t.Fatalf("baselines = %d", len(pace.Baseline))
	}
	if pace.Average[9] == nil || *pace.Average[9] != 1000 {
		t.Fatalf("average day 10 = %#v", pace.Average[9])
	}
	if pace.Velocity != "above" || pace.DeltaBP != 20000 {
		t.Fatalf("velocity = %s delta = %d", pace.Velocity, pace.DeltaBP)
	}
}
