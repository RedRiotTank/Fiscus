package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputeLiquidity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "acc-1", ConnectionID: 1, Name: "Everyday", Currency: "EUR", BalanceMinor: 10000},
		{ID: "acc-2", ConnectionID: 1, Name: "Savings", Currency: "EUR", BalanceMinor: 5000},
	}
	connections := []domain.Connection{{ID: 1, InstitutionName: "Santander"}}
	transactions := []domain.Transaction{
		testTx(1, "acc-1", -1000, time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC), "", domain.TransactionStatusBooked),
		testTx(2, "acc-1", -500, time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC), "", domain.TransactionStatusPending),
	}
	liquidity := ComputeLiquidity(accounts, connections, transactions, now, "EUR", 3)
	if len(liquidity.Labels) != 3 || liquidity.From != "2026-05-08" || liquidity.To != "2026-05-10" {
		t.Fatalf("liquidity labels = %#v", liquidity)
	}
	if len(liquidity.Series) != 1 || liquidity.Series[0].Institution != "Santander" {
		t.Fatalf("series = %#v", liquidity.Series)
	}
	want := []int64{16000, 15000, 15000}
	for i, value := range want {
		if liquidity.Series[0].Points[i] != value {
			t.Fatalf("point %d = %d, want %d", i, liquidity.Series[0].Points[i], value)
		}
	}
}
