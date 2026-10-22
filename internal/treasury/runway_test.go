package treasury

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputeRunwaysExcludesInternalTransfers(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "acc-1", Currency: "EUR", BalanceMinor: 600000},
		{ID: "acc-2", Currency: "EUR", BalanceMinor: 400000},
	}
	transactions := []domain.Transaction{
		tx(1, "acc-1", -300000, "EUR", "2026-01-15"),
		tx(2, "acc-1", -300000, "EUR", "2026-02-15"),
		tx(3, "acc-1", -300000, "EUR", "2026-03-15"),
		tx(4, "acc-1", -100000, "EUR", "2026-03-20"),
		tx(5, "acc-2", 100000, "EUR", "2026-03-20"),
		tx(6, "acc-1", 500000, "EUR", "2026-03-25"),
		tx(7, "acc-1", -999999, "EUR", "2025-12-20"),
	}
	pending := tx(8, "acc-1", -999999, "EUR", "2026-03-30")
	pending.Status = domain.TransactionStatusPending
	transactions = append(transactions, pending)

	runways := ComputeRunways(accounts, transactions, now, DefaultBurnWindowDays)
	if len(runways) != 1 {
		t.Fatalf("runways = %+v, want 1", runways)
	}
	runway := runways[0]
	if runway.Currency != "EUR" {
		t.Fatalf("currency = %q", runway.Currency)
	}
	if runway.LiquidMinor != 1000000 {
		t.Fatalf("liquid = %d, want 1000000", runway.LiquidMinor)
	}
	if runway.MonthlyBurnMinor != 300000 {
		t.Fatalf("monthly burn = %d, want 300000 (internal transfer must be excluded)", runway.MonthlyBurnMinor)
	}
	if !runway.RunwayKnown || runway.RunwayTenths != 33 {
		t.Fatalf("runway = %+v, want 33 tenths", runway)
	}
}

func TestComputeRunwaysMultiCurrencyAndNoBurn(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000},
		{ID: "acc-2", Currency: "USD", BalanceMinor: 250000},
	}
	transactions := []domain.Transaction{
		tx(1, "acc-1", -50000, "EUR", "2026-03-10"),
		tx(2, "acc-2", -100000, "USD", "2026-03-11"),
	}
	runways := ComputeRunways(accounts, transactions, now, DefaultBurnWindowDays)
	if len(runways) != 2 {
		t.Fatalf("runways = %+v", runways)
	}
	if runways[0].Currency != "EUR" || runways[1].Currency != "USD" {
		t.Fatalf("currency order = %s, %s", runways[0].Currency, runways[1].Currency)
	}
	if runways[0].MonthlyBurnMinor != 16667 {
		t.Fatalf("EUR burn = %d", runways[0].MonthlyBurnMinor)
	}
	if runways[1].MonthlyBurnMinor != 33333 {
		t.Fatalf("USD burn = %d", runways[1].MonthlyBurnMinor)
	}

	noBurn := ComputeRunways(
		[]domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000}},
		nil, now, DefaultBurnWindowDays,
	)
	if len(noBurn) != 1 || noBurn[0].RunwayKnown || noBurn[0].MonthlyBurnMinor != 0 {
		t.Fatalf("no-burn runway = %+v", noBurn)
	}

	overdrawn := ComputeRunways(
		[]domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: -1000}},
		[]domain.Transaction{tx(1, "acc-1", -30000, "EUR", "2026-03-01")},
		now, DefaultBurnWindowDays,
	)
	if len(overdrawn) != 1 || !overdrawn[0].RunwayKnown || overdrawn[0].RunwayTenths != 0 {
		t.Fatalf("overdrawn runway = %+v", overdrawn)
	}
}

func TestComputeRunwaysWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	accounts := []domain.Account{{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000}}
	transactions := []domain.Transaction{
		tx(1, "acc-1", -30000, "EUR", "2026-01-02"),
		tx(2, "acc-1", -30000, "EUR", "2025-12-31"),
	}
	runways := ComputeRunways(accounts, transactions, now, 90)
	if len(runways) != 1 || runways[0].MonthlyBurnMinor != 10000 {
		t.Fatalf("runways = %+v", runways)
	}
}
