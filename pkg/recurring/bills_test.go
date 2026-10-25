package recurring

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func bookedDebit(counterparty, description, category string, amount int64, date time.Time) domain.Transaction {
	return domain.Transaction{
		AccountID: "acc-1", ExternalID: date.Format("2006-01-02") + counterparty,
		AmountMinor: amount, Currency: "EUR", BookingDate: date,
		Counterparty: counterparty, Description: description, Category: category,
		Status: domain.TransactionStatusBooked,
	}
}

func TestDetectMonthlyBizumSharedBill(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		bookedDebit("Alberto Avidad Fernández", "Bizum enviado · Fibra internet", "Transfers / P2P", -1600, time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)),
		bookedDebit("Alberto Avidad Fernández", "Bizum enviado · Fibra internet", "Transfers / P2P", -1600, time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)),
		bookedDebit("Alberto Avidad Fernández", "Bizum enviado · Fibra internet", "Transfers / P2P", -1600, time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)),
	}
	candidates := Detect(transactions, now)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	candidate := candidates[0]
	if candidate.Counterparty != "Alberto Avidad Fernández" || candidate.AmountMinor != 1600 ||
		candidate.Occurrences != 3 || candidate.Currency != "EUR" {
		t.Fatalf("candidate = %+v", candidate)
	}
	if candidate.TypicalDay != 1 {
		t.Fatalf("typical day = %d", candidate.TypicalDay)
	}
	share := PersonalShare(candidate.AmountMinor, 3)
	if share != 533 {
		t.Fatalf("personal share = %d, want 533", share)
	}
}

func TestDetectRejectsHousingAndSubscriptions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		bookedDebit("Sonia Reyes", "Alquiler piso", "Housing / Rent", -105000, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Sonia Reyes", "Alquiler piso", "Housing / Rent", -105000, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Netflix", "Netflix subscription", "Subscriptions", -1299, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Netflix", "Netflix subscription", "Subscriptions", -1299, time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Revolut Vault", "Monthly emergency-fund transfer", "Emergency Fund", -30000, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Revolut Vault", "Monthly emergency-fund transfer", "Emergency Fund", -30000, time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)),
	}
	if candidates := Detect(transactions, now); len(candidates) != 0 {
		t.Fatalf("excluded transactions produced candidates: %+v", candidates)
	}
}

func TestDetectRejectsUnstableAmountsAndMidMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		bookedDebit("Gym", "Monthly membership", "Leisure & Shopping", -2000, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Gym", "Monthly membership", "Leisure & Shopping", -3500, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Gym", "Monthly membership", "Leisure & Shopping", -2000, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Cleaner", "Weekly clean", "Housing / Rent", -3000, time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)),
		bookedDebit("Cleaner", "Weekly clean", "Housing / Rent", -3000, time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)),
	}
	if candidates := Detect(transactions, now); len(candidates) != 0 {
		t.Fatalf("unstable or mid-month transactions produced candidates: %+v", candidates)
	}
}

func TestPersonalShareRounding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		gross  int64
		people int
		want   int64
	}{
		{gross: 1600, people: 3, want: 533},
		{gross: 1600, people: 2, want: 800},
		{gross: 1001, people: 2, want: 501},
		{gross: 1000, people: 1, want: 1000},
		{gross: 0, people: 4, want: 0},
	}
	for _, test := range tests {
		if got := PersonalShare(test.gross, test.people); got != test.want {
			t.Errorf("PersonalShare(%d, %d) = %d, want %d", test.gross, test.people, got, test.want)
		}
	}
}

func TestMatchesCounterparty(t *testing.T) {
	t.Parallel()
	tx := domain.Transaction{Counterparty: "Bizum Alberto Avidad Fernández", Description: "Fibra"}
	if !MatchesCounterparty(tx, "Alberto Avidad Fernández") {
		t.Fatal("expected normalized counterparty match")
	}
	if MatchesCounterparty(tx, "Ana Ruiz") {
		t.Fatal("unexpected counterparty match")
	}
}
