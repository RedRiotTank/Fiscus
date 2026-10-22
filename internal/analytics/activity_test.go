package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputeActivity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		testTx(1, "acc-1", -500, time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(2, "acc-1", -2000, time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(3, "acc-1", -5000, time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(4, "acc-1", -10000, time.Date(2026, 5, 13, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(5, "acc-1", -25000, time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(6, "acc-1", -1000, time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusPending),
		testTx(7, "acc-1", 1000, time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC), "Payroll", domain.TransactionStatusBooked),
	}
	activity := ComputeActivity(transactions, now, "EUR")
	if activity.Transactions != 5 {
		t.Fatalf("transactions = %d", activity.Transactions)
	}
	if activity.Buckets[0].Counts[0] != 1 {
		t.Fatalf("small Monday count = %d", activity.Buckets[0].Counts[0])
	}
	if activity.Buckets[1].Counts[0] != 1 {
		t.Fatalf("10-30 Monday count = %d", activity.Buckets[1].Counts[0])
	}
	if activity.Buckets[2].Counts[1] != 1 {
		t.Fatalf("30-75 Tuesday count = %d", activity.Buckets[2].Counts[1])
	}
	if activity.Buckets[3].Counts[2] != 1 {
		t.Fatalf("75-200 Wednesday count = %d", activity.Buckets[3].Counts[2])
	}
	if activity.Buckets[4].Counts[3] != 1 {
		t.Fatalf(">200 Thursday count = %d", activity.Buckets[4].Counts[3])
	}
}
