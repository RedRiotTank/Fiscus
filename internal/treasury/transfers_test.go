package treasury

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func tx(id int64, account string, amount int64, currency, date string) domain.Transaction {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return domain.Transaction{
		ID: id, AccountID: account, AmountMinor: amount, Currency: currency,
		BookingDate: parsed, Status: domain.TransactionStatusBooked,
	}
}

func TestPairTransfers(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		tx(1, "acc-1", -10000, "EUR", "2026-03-01"),
		tx(2, "acc-2", 10000, "EUR", "2026-03-02"),
		tx(3, "acc-1", -5000, "EUR", "2026-03-10"),
		tx(4, "acc-2", 5000, "EUR", "2026-03-20"),
		tx(5, "acc-1", -7500, "EUR", "2026-03-05"),
		tx(6, "acc-1", 7500, "EUR", "2026-03-05"),
		tx(7, "acc-2", -2000, "USD", "2026-03-03"),
		tx(8, "acc-1", 2000, "EUR", "2026-03-03"),
		tx(9, "acc-2", -3000, "EUR", "2026-03-06"),
		tx(10, "acc-1", 3000, "EUR", "2026-03-08"),
	}
	pairs := PairTransfers(transactions, 3)
	if len(pairs) != 2 {
		t.Fatalf("pairs = %+v, want 2", pairs)
	}
	found := map[int64]int64{}
	for _, pair := range pairs {
		found[pair.Debit.ID] = pair.Credit.ID
	}
	if found[1] != 2 {
		t.Fatalf("debit 1 paired with %d, want 2", found[1])
	}
	if found[9] != 10 {
		t.Fatalf("debit 9 paired with %d, want 10", found[9])
	}
	if _, ok := found[3]; ok {
		t.Fatal("debit outside transfer window was paired")
	}
	if _, ok := found[5]; ok {
		t.Fatal("same-account transfer was paired")
	}
	if _, ok := found[7]; ok {
		t.Fatal("cross-currency transfer was paired")
	}

	filtered := ExcludeTransfers(transactions, pairs)
	if len(filtered) != 6 {
		t.Fatalf("filtered = %d, want 6", len(filtered))
	}
	for _, transaction := range filtered {
		if transaction.ID == 1 || transaction.ID == 2 || transaction.ID == 9 || transaction.ID == 10 {
			t.Fatalf("transfer transaction %d was not excluded", transaction.ID)
		}
	}
}

func TestPairTransfersPrefersClosestDate(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		tx(1, "acc-1", -10000, "EUR", "2026-03-03"),
		tx(2, "acc-2", 10000, "EUR", "2026-03-01"),
		tx(3, "acc-3", 10000, "EUR", "2026-03-03"),
	}
	pairs := PairTransfers(transactions, 3)
	if len(pairs) != 1 || pairs[0].Debit.ID != 1 || pairs[0].Credit.ID != 3 {
		t.Fatalf("pairs = %+v", pairs)
	}
}

func TestPairTransfersIgnoresPending(t *testing.T) {
	t.Parallel()
	debit := tx(1, "acc-1", -10000, "EUR", "2026-03-01")
	credit := tx(2, "acc-2", 10000, "EUR", "2026-03-01")
	credit.Status = domain.TransactionStatusPending
	if pairs := PairTransfers([]domain.Transaction{debit, credit}, 3); len(pairs) != 0 {
		t.Fatalf("pending transfer paired: %+v", pairs)
	}
}
