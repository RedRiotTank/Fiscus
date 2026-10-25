package housing

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func rentTx(counterparty, description string, amount int64, date string) domain.Transaction {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return domain.Transaction{
		AccountID: "checking", ExternalID: counterparty + date, AmountMinor: amount, Currency: "EUR",
		BookingDate: parsed, Counterparty: counterparty, Description: description,
		Status: domain.TransactionStatusBooked,
	}
}

func TestDetectSharedRentPattern(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		rentTx("Sonia Reyes Álvarez", "Transferencia mensual", -105_000, "2026-09-01"),
		rentTx("Sonia Reyes Álvarez", "Transferencia mensual", -105_000, "2026-10-01"),
		rentTx("Ismael Díaz", "Piso septiembre | From Ismael", 35_000, "2026-09-02"),
		rentTx("Ismael Díaz", "Piso octubre | From Ismael", 35_000, "2026-10-02"),
		rentTx("Jose Hinojos", "Piso septiembre", 35_000, "2026-09-03"),
		rentTx("Jose Hinojos", "Piso octubre", 35_000, "2026-10-03"),
		rentTx("Employer", "Salary", 280_000, "2026-10-01"),
		rentTx("Netflix", "Subscription", -1_299, "2026-10-01"),
		rentTx("Occasional vendor", "One-off", -220_000, "2026-10-02"),
	}
	detection := Detect(transactions, now)
	if !detection.Found {
		t.Fatal("expected a shared-rent detection")
	}
	if detection.Landlord.Counterparty != "Sonia Reyes Álvarez" || detection.GrossRentMinor != 105_000 {
		t.Fatalf("landlord = %+v gross=%d", detection.Landlord, detection.GrossRentMinor)
	}
	if len(detection.Flatmates) != 2 || detection.FlatmateTotalMinor != 70_000 || detection.NetShareMinor != 35_000 {
		t.Fatalf("flatmates = %+v total=%d net=%d", detection.Flatmates, detection.FlatmateTotalMinor, detection.NetShareMinor)
	}
	if detection.Currency != "EUR" {
		t.Fatalf("currency = %q", detection.Currency)
	}
}

func TestDetectRepeatingFlatmateWithoutHousingNote(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		rentTx("Landlord", "Monthly payment", -90_000, "2026-09-01"),
		rentTx("Landlord", "Monthly payment", -90_000, "2026-10-01"),
		rentTx("Flatmate", "Transfer", 30_000, "2026-09-02"),
		rentTx("Flatmate", "Transfer", 30_000, "2026-10-02"),
	}
	detection := Detect(transactions, now)
	if !detection.Found || len(detection.Flatmates) != 1 || detection.NetShareMinor != 60_000 {
		t.Fatalf("detection = %+v", detection)
	}
}

func TestDetectIgnoresTransactionsOutsideMonthEdge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		rentTx("Sonia", "Rent", -105_000, "2026-09-15"),
		rentTx("Sonia", "Rent", -105_000, "2026-10-15"),
	}
	if detection := Detect(transactions, now); detection.Found {
		t.Fatalf("mid-month payments should not be detected: %+v", detection)
	}
}
