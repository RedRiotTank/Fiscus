package transfers

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func transferTx(id int64, account string, amount int64, date, counterparty, description string) domain.Transaction {
	parsed, err := time.Parse("2006-01-02 15:04", date)
	if err != nil {
		panic(err)
	}
	return domain.Transaction{
		ID: id, AccountID: account, AmountMinor: amount, Currency: "EUR",
		BookingDate: parsed, Counterparty: counterparty, Description: description,
		Status: domain.TransactionStatusBooked,
	}
}

func TestDetectExactMirrorPairSameDay(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		transferTx(1, "acc-1", 58_556, "2026-10-05 09:00", "Cuenta Remunerada", "Transferencia interna"),
		transferTx(2, "acc-1", -58_556, "2026-10-05 09:05", "Ismael Díaz Díaz", "To Ismael Díaz Díaz"),
	}
	matches := Detect(transactions, 3)
	if len(matches) != 1 {
		t.Fatalf("matches = %+v, want 1", matches)
	}
	if matches[0].Outflow.ID != 2 || matches[0].Inflow.ID != 1 {
		t.Fatalf("match = %+v", matches[0])
	}
}

func TestDetectWindowAndAmountTolerance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		outDate     string
		outAmount   int64
		wantMatches int
	}{
		{name: "same day", outDate: "2026-10-05 18:00", outAmount: -58_556, wantMatches: 1},
		{name: "48 hours later", outDate: "2026-10-07 09:00", outAmount: -58_556, wantMatches: 1},
		{name: "72 hours later", outDate: "2026-10-08 09:00", outAmount: -58_556, wantMatches: 1},
		{name: "outside window", outDate: "2026-10-09 09:00", outAmount: -58_556, wantMatches: 0},
		{name: "amount mismatch", outDate: "2026-10-05 18:00", outAmount: -58_000, wantMatches: 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transactions := []domain.Transaction{
				transferTx(1, "acc-2", 58_556, "2026-10-05 09:00", "Cuenta Remunerada", "Savings bridge"),
				transferTx(2, "acc-1", test.outAmount, test.outDate, "Ismael Díaz Díaz", "To Ismael Díaz Díaz"),
			}
			if matches := Detect(transactions, 3); len(matches) != test.wantMatches {
				t.Fatalf("matches = %+v, want %d", matches, test.wantMatches)
			}
		})
	}
}

func TestDetectIgnoresNonTransferAmountCoincidences(t *testing.T) {
	t.Parallel()
	transactions := []domain.Transaction{
		transferTx(1, "acc-1", 2_500, "2026-10-05 09:00", "Employer", "Payroll"),
		transferTx(2, "acc-1", -2_500, "2026-10-05 18:00", "Coffee Shop", "Coffee"),
	}
	if matches := Detect(transactions, 3); len(matches) != 0 {
		t.Fatalf("non-transfer coincidence paired: %+v", matches)
	}
}

func TestDetectHonoursSuppressionAndExistingPairing(t *testing.T) {
	t.Parallel()
	suppressed := transferTx(1, "acc-1", 58_556, "2026-10-05 09:00", "Cuenta Remunerada", "Savings")
	suppressed.PassThroughSuppressed = true
	pairedID := int64(9)
	alreadyPaired := transferTx(2, "acc-1", -58_556, "2026-10-05 09:05", "Ismael", "To Ismael")
	alreadyPaired.PairedTransactionID = &pairedID
	if matches := Detect([]domain.Transaction{
		suppressed,
		alreadyPaired,
		transferTx(3, "acc-1", -58_556, "2026-10-05 09:05", "Ismael", "To Ismael"),
	}, 3); len(matches) != 0 {
		t.Fatalf("suppressed/already-paired transaction was reused: %+v", matches)
	}
}

func TestIsPassThroughCategoryAliases(t *testing.T) {
	t.Parallel()
	for _, category := range []string{"Transfers / Pass-through", "pass through", "Transfers / Internal Offset"} {
		if !IsPassThroughCategory(category) {
			t.Errorf("IsPassThroughCategory(%q) = false", category)
		}
	}
	if IsPassThroughCategory("Transfers / P2P") {
		t.Fatal("P2P category incorrectly treated as pass-through")
	}
}
