package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func testTx(id int64, account string, amount int64, date time.Time, category, status string) domain.Transaction {
	return domain.Transaction{
		ID: id, AccountID: account, AmountMinor: amount, Currency: "EUR",
		BookingDate: date, Category: category, Status: status,
	}
}

func TestNormalizePeriod(t *testing.T) {
	t.Parallel()
	cases := map[string]string{"": PeriodMonth, "month": PeriodMonth, "quarter": PeriodQuarter, "YTD": PeriodYTD, "year": PeriodYTD}
	for input, want := range cases {
		got, ok := NormalizePeriod(input)
		if !ok || got != want {
			t.Errorf("NormalizePeriod(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	if _, ok := NormalizePeriod("week"); ok {
		t.Fatal("invalid period accepted")
	}
}
