package accrual

import (
	"math"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func testAccount() domain.AccruingAccount {
	return domain.AccruingAccount{
		ID: "accrual-1", WorkspaceID: domain.WorkspacePersonal, Name: "Revolut Cuenta Remunerada",
		Currency: "EUR", AnchorBalanceCents: 1_000_000,
		AnchorDate:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		APYRateBasisPoints: 365, CompoundingFrequency: domain.AccruingDaily,
		IsActive: true,
	}
}

func TestDailyCompoundingMatchesClosedForm(t *testing.T) {
	t.Parallel()
	account := testAccount()
	report := Project(account, nil, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	want := 1_000_000 * math.Pow(1.0365, 30.0/365.0)
	if math.Abs(float64(report.BalanceMinor)-want) > 1 {
		t.Fatalf("balance = %d, want %.4f within one cent", report.BalanceMinor, want)
	}
	if report.DaysProjected != 30 {
		t.Fatalf("days = %d, want 30", report.DaysProjected)
	}
	if report.LifetimeInterestMinor != report.BalanceMinor-account.AnchorBalanceCents {
		t.Fatalf("lifetime interest = %d, balance delta = %d", report.LifetimeInterestMinor, report.BalanceMinor-account.AnchorBalanceCents)
	}
	if report.MonthInterestMinor != report.LifetimeInterestMinor {
		t.Fatalf("month interest = %d, lifetime = %d", report.MonthInterestMinor, report.LifetimeInterestMinor)
	}
}

func TestMidStreamTransferCompoundsForRemainingDays(t *testing.T) {
	t.Parallel()
	account := testAccount()
	asOf := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	without := Project(account, nil, asOf)
	with := Project(account, []Transfer{{
		Date: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC), AmountMinor: 100_000,
	}}, asOf)
	rate := NetDailyRate(account.APYRateBasisPoints, account.TaxWithholdingBPS)
	// The transfer lands after the October 15 yield, so it compounds on the
	// 15 remaining accrual days (October 16-30).
	wantDelta := 100_000 * math.Pow(1+rate, 15)
	gotDelta := float64(with.BalanceMinor - without.BalanceMinor)
	if math.Abs(gotDelta-wantDelta) > 1 {
		t.Fatalf("transfer delta = %.4f, want %.4f within one cent", gotDelta, wantDelta)
	}
}

func TestTaxWithholdingReducesNetYield(t *testing.T) {
	t.Parallel()
	account := testAccount()
	account.TaxWithholdingBPS = 1900
	report := Project(account, nil, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	netRate := DailyRate(account.APYRateBasisPoints) * (1 - 0.19)
	want := 1_000_000 * math.Pow(1+netRate, 30)
	if math.Abs(float64(report.BalanceMinor)-want) > 1 {
		t.Fatalf("taxed balance = %d, want %.4f", report.BalanceMinor, want)
	}
}

func TestReconcileAnchorRestartsTimeline(t *testing.T) {
	t.Parallel()
	account := testAccount()
	account.AnchorBalanceCents = 1_100_000
	account.AnchorDate = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	report := Project(account, []Transfer{{
		Date: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), AmountMinor: 500_000,
	}}, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	want := 1_100_000 * math.Pow(1.0365, 16.0/365.0)
	if math.Abs(float64(report.BalanceMinor)-want) > 1 {
		t.Fatalf("reconciled balance = %d, want %.4f", report.BalanceMinor, want)
	}
	if report.DaysProjected != 16 {
		t.Fatalf("days = %d, want 16", report.DaysProjected)
	}
}

func TestTransfersFromTransactionsUsesPatternAndDirection(t *testing.T) {
	t.Parallel()
	account := testAccount()
	account.TransferMatchPattern = `(?i)cuenta\s*remunerada`
	transactions := []domain.Transaction{
		{ID: 1, Currency: "EUR", AmountMinor: -50_000, BookingDate: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), Counterparty: "Cuenta Remunerada", Description: "To savings", Status: domain.TransactionStatusBooked},
		{ID: 2, Currency: "EUR", AmountMinor: 20_000, BookingDate: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), Counterparty: "Cuenta Remunerada", Description: "From savings", Status: domain.TransactionStatusBooked},
		{ID: 5, Currency: "EUR", AmountMinor: -30_000, BookingDate: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), Counterparty: "", Description: "To Cuenta\u00a0Remunerada", Status: domain.TransactionStatusBooked},
		{ID: 3, Currency: "EUR", AmountMinor: -999, BookingDate: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), Counterparty: "Coffee", Description: "Coffee", Status: domain.TransactionStatusBooked},
		{ID: 4, Currency: "EUR", AmountMinor: -10_000, BookingDate: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), Counterparty: "Cuenta Remunerada", Description: "Pending", Status: domain.TransactionStatusPending},
	}
	transfers, err := TransfersFromTransactions(account, transactions)
	if err != nil {
		t.Fatalf("TransfersFromTransactions: %v", err)
	}
	if len(transfers) != 3 || transfers[0].AmountMinor != 50_000 || transfers[1].AmountMinor != -20_000 || transfers[2].AmountMinor != 30_000 {
		t.Fatalf("transfers = %+v", transfers)
	}
}

func TestKeywordPattern(t *testing.T) {
	t.Parallel()
	pattern, err := KeywordPattern("Cuenta Remunerada")
	if err != nil {
		t.Fatalf("KeywordPattern: %v", err)
	}
	if pattern != `(?i)Cuenta\ Remunerada` && pattern != `(?i)Cuenta Remunerada` {
		t.Fatalf("pattern = %q", pattern)
	}
	raw, err := KeywordPattern(`(?i)cuenta\s*remunerada`)
	if err != nil || raw != `(?i)cuenta\s*remunerada` {
		t.Fatalf("raw pattern = %q err=%v", raw, err)
	}
}
