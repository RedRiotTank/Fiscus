package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputeProjections(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000}}
	var transactions []domain.Transaction
	id := int64(1)
	for month := 1; month <= 12; month++ {
		start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -month, 0)
		transactions = append(transactions,
			testTx(id, "acc-1", 10000, start.AddDate(0, 0, 1), "Payroll", domain.TransactionStatusBooked))
		id++
		transactions = append(transactions,
			testTx(id, "acc-1", -5000, start.AddDate(0, 0, 2), "Living", domain.TransactionStatusBooked))
		id++
	}
	projection := ComputeProjections(transactions, accounts, now, "EUR", 0, 0)
	if projection.MuMinor != 5000 || projection.SigmaMinor != 0 {
		t.Fatalf("mean/sigma = %d/%d", projection.MuMinor, projection.SigmaMinor)
	}
	if len(projection.History) != 13 || projection.History[12].BalanceMinor != 100000 {
		t.Fatalf("history = %#v", projection.History)
	}
	oneYear := projection.Horizons["1y"]
	if oneYear.Months != 12 || len(oneYear.Baseline) != 13 {
		t.Fatalf("1y = %#v", oneYear)
	}
	if oneYear.Baseline[0] != 100000 || oneYear.Baseline[12] != 160000 {
		t.Fatalf("1y baseline = %#v", oneYear.Baseline)
	}
	if oneYear.Optimistic[12] != 160000 || oneYear.Conservative[12] != 160000 {
		t.Fatalf("zero sigma band changed: %#v %#v", oneYear.Optimistic, oneYear.Conservative)
	}
	adjusted := ComputeProjections(transactions, accounts, now, "EUR", 1000, 0)
	if adjusted.EffectiveMuMinor != 6000 || adjusted.Horizons["1y"].Baseline[12] != 172000 {
		t.Fatalf("adjusted projection = %#v", adjusted.Horizons["1y"])
	}
	withYield := ComputeProjections(transactions, accounts, now, "EUR", 0, 1200)
	if withYield.Horizons["1y"].Baseline[12] <= 100000 {
		t.Fatalf("yield did not compound: %#v", withYield.Horizons["1y"].Baseline)
	}
}

func TestComputeProjectionsVolatilityBand(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: 0}}
	var transactions []domain.Transaction
	// Two historical months with net savings of 1000 and 3000 inside the
	// twelve-month window: the sample standard deviation is 887 minor units.
	for month, amount := range map[int]int64{4: 1000, 3: 3000} {
		transactions = append(transactions, testTx(int64(month), "acc-1", amount, time.Date(2026, time.Month(month), 2, 0, 0, 0, 0, time.UTC), "Payroll", domain.TransactionStatusBooked))
	}
	projection := ComputeProjections(transactions, accounts, now, "EUR", 0, 0)
	if projection.SigmaMinor != 887 {
		t.Fatalf("sigma = %d", projection.SigmaMinor)
	}
	oneYear := projection.Horizons["1y"]
	if oneYear.Optimistic[12] <= oneYear.Baseline[12] || oneYear.Conservative[12] >= oneYear.Baseline[12] {
		t.Fatalf("volatility band not applied: %#v", oneYear)
	}
}
