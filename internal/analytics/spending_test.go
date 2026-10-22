package analytics

import (
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestComputeSpendingBreakdownTrendAndWaterfall(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	accounts := []domain.Account{
		{ID: "acc-1", Currency: "EUR", BalanceMinor: 100000},
		{ID: "acc-2", Currency: "EUR", BalanceMinor: 50000},
	}
	transactions := []domain.Transaction{
		testTx(1, "acc-1", -1000, time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(2, "acc-1", -2000, time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC), "Groceries", domain.TransactionStatusBooked),
		testTx(3, "acc-1", 5000, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), "Payroll", domain.TransactionStatusBooked),
		testTx(4, "acc-1", -4000, time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC), "", domain.TransactionStatusBooked),
		testTx(5, "acc-2", 4000, time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC), "", domain.TransactionStatusBooked),
		testTx(6, "acc-1", -999, time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusPending),
		testTx(7, "acc-1", -3000, time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC), "Dining", domain.TransactionStatusBooked),
		testTx(8, "acc-1", 10000, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), "Payroll", domain.TransactionStatusBooked),
	}

	month := ComputeSpending(transactions, accounts, now, "EUR", PeriodMonth)
	if month.TotalMinor != 3000 {
		t.Fatalf("month total = %d, want 3000", month.TotalMinor)
	}
	if len(month.Categories) != 2 {
		t.Fatalf("categories = %#v", month.Categories)
	}
	if month.Categories[0].Category != "Groceries" || month.Categories[0].Minor != 2000 {
		t.Fatalf("top category = %#v", month.Categories[0])
	}
	if month.Categories[0].ShareBP+month.Categories[1].ShareBP > 10000 {
		t.Fatalf("shares exceed 100%%: %#v", month.Categories)
	}
	if len(month.Trend) != 6 || month.Trend[5].Month != "2026-05" {
		t.Fatalf("trend = %#v", month.Trend)
	}
	if month.Trend[4].IncomeMinor != 10000 || month.Trend[4].ExpenseMinor != 3000 {
		t.Fatalf("April trend = %#v", month.Trend[4])
	}
	if month.Trend[5].IncomeMinor != 5000 || month.Trend[5].ExpenseMinor != 3000 {
		t.Fatalf("May trend = %#v", month.Trend[5])
	}
	w := month.Waterfall
	if w.OpeningMinor != 148000 || w.InflowMinor != 5000 || w.ClosingMinor != 150000 {
		t.Fatalf("waterfall = %#v", w)
	}
	if len(w.Outflows) != 2 || w.Outflows[0].Label != "Groceries" {
		t.Fatalf("waterfall outflows = %#v", w.Outflows)
	}

	quarter := ComputeSpending(transactions, accounts, now, "EUR", PeriodQuarter)
	if quarter.From != "2026-04-01" || quarter.TotalMinor != 6000 {
		t.Fatalf("quarter = %#v", quarter)
	}
	ytd := ComputeSpending(transactions, accounts, now, "EUR", PeriodYTD)
	if ytd.From != "2026-01-01" || ytd.TotalMinor != 6000 {
		t.Fatalf("ytd = %#v", ytd)
	}
}
