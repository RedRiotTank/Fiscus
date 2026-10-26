package storage

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
)

func TestAccruingAccountPersistence(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	account := domain.AccruingAccount{
		ID: "accrual-1", WorkspaceID: domain.WorkspacePersonal, Name: "Revolut Cuenta Remunerada",
		Currency: "EUR", AnchorBalanceCents: 345_000,
		AnchorDate:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		APYRateBasisPoints: 310, CompoundingFrequency: domain.AccruingDaily,
		TaxWithholdingBPS: 1900, TransferMatchPattern: `(?i)cuenta\s*remunerada`, IsActive: true,
	}
	if err := store.SaveAccruingAccount(ctx, &account); err != nil {
		t.Fatalf("SaveAccruingAccount: %v", err)
	}
	loaded, err := store.GetAccruingAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("GetAccruingAccount: %v", err)
	}
	if loaded.AnchorBalanceCents != 345_000 || loaded.APYRateBasisPoints != 310 ||
		loaded.TaxWithholdingBPS != 1900 || !loaded.IsActive ||
		loaded.CompoundingFrequency != domain.AccruingDaily {
		t.Fatalf("loaded account = %+v", loaded)
	}
	if loaded.AnchorDate.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("anchor date = %v", loaded.AnchorDate)
	}
	all, err := store.ListAccruingAccounts(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAccruingAccounts = %+v err=%v", all, err)
	}

	account.AnchorBalanceCents = 350_000
	account.AnchorDate = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err := store.SaveAccruingAccount(ctx, &account); err != nil {
		t.Fatalf("reconcile SaveAccruingAccount: %v", err)
	}
	loaded, err = store.GetAccruingAccount(ctx, account.ID)
	if err != nil || loaded.AnchorBalanceCents != 350_000 ||
		loaded.AnchorDate.Format("2006-01-02") != "2026-10-20" {
		t.Fatalf("reconciled account = %+v err=%v", loaded, err)
	}
}

func TestCanonicalTransactionCategoryAliases(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Transfers / Bizum":   "Transfers / P2P",
		"Transfers / Bizums":  "Transfers / P2P",
		"bizum":               "Transfers / P2P",
		"Savings / Accruing":  "Savings & Investments",
		"Transfers / Savings": "Savings & Investments",
		"Groceries":           "Groceries",
	}
	for input, want := range cases {
		if got := canonicalTransactionCategory(input); got != want {
			t.Errorf("canonicalTransactionCategory(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLinkGenericSavingsTransfersWithoutAccruingAccount(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, store)
	checking := seedAccount(t, store, connection.ID)
	savings := domain.Account{
		ID: "acc-savings", ConnectionID: connection.ID, Name: "Revolut Savings",
		Currency: "EUR", Kind: domain.AccountKindSavings,
	}
	if err := store.UpsertAccount(ctx, &savings); err != nil {
		t.Fatalf("UpsertAccount savings: %v", err)
	}
	loadedSavings, err := store.GetAccount(ctx, savings.ID)
	if err != nil || loadedSavings.Kind != domain.AccountKindSavings {
		t.Fatalf("savings kind = %q err=%v", loadedSavings.Kind, err)
	}
	if err := store.SetAccountKind(ctx, savings.ID, domain.AccountKindInvestment); err != nil {
		t.Fatalf("SetAccountKind: %v", err)
	}
	loadedSavings, _ = store.GetAccount(ctx, savings.ID)
	if loadedSavings.Kind != domain.AccountKindInvestment {
		t.Fatalf("updated kind = %q", loadedSavings.Kind)
	}
	if err := store.SetAccountKind(ctx, savings.ID, domain.AccountKindSavings); err != nil {
		t.Fatalf("restore kind: %v", err)
	}

	day := time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: checking.ID, ExternalID: "generic-savings-out", AmountMinor: -100_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "To Cuenta Remunerada",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: checking.ID, ExternalID: "generic-savings-in", AmountMinor: 25_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "From Cuenta Remunerada",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: checking.ID, ExternalID: "account-name-out", AmountMinor: -50_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Bridge", Description: "Transfer",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: savings.ID, ExternalID: "savings-side-in", AmountMinor: 50_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Everyday", Description: "From Everyday",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: savings.ID, ExternalID: "savings-interest", AmountMinor: 100, Currency: "EUR",
			BookingDate: day, Counterparty: "Interest", Description: "Interest",
			Category: "Uncategorized", Status: domain.TransactionStatusBooked},
	}
	for index := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	linked, err := store.LinkAccruingTransfers(ctx)
	if err != nil {
		t.Fatalf("LinkAccruingTransfers: %v", err)
	}
	if linked != len(transactions) {
		t.Fatalf("linked = %d, want %d", linked, len(transactions))
	}
	for index, want := range []struct {
		category string
		excluded bool
	}{
		{accrual.CategorySavings, false},
		{accrual.CategoryWithdrawal, true},
		{accrual.CategorySavings, false},
		{accrual.CategoryWithdrawal, true},
		{accrual.CategoryWithdrawal, true},
	} {
		loaded, err := store.GetTransaction(ctx, transactions[index].ID)
		if err != nil {
			t.Fatalf("GetTransaction %d: %v", index, err)
		}
		if loaded.Category != want.category || loaded.IsBudgetExcluded != want.excluded {
			t.Errorf("transaction %d = %+v, want %s excluded=%v", index, loaded, want.category, want.excluded)
		}
	}
}

func TestLinkAccruingTransfersCategoriesAndBudgetDirection(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, store)
	account := seedAccount(t, store, connection.ID)
	accruing := domain.AccruingAccount{
		ID: "accrual-link", WorkspaceID: domain.WorkspacePersonal, Name: "Revolut Cuenta Remunerada",
		Currency: "EUR", AnchorBalanceCents: 100_000,
		AnchorDate:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		APYRateBasisPoints: 310, CompoundingFrequency: domain.AccruingDaily,
		TransferMatchPattern: `(?i)cuenta\s*remunerada`, IsActive: true,
	}
	if err := store.SaveAccruingAccount(ctx, &accruing); err != nil {
		t.Fatalf("SaveAccruingAccount: %v", err)
	}
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: account.ID, ExternalID: "savings-in", AmountMinor: -50_000, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "To savings",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "savings-out", AmountMinor: 20_000, Currency: "EUR",
			BookingDate: day.Add(time.Hour), Counterparty: "Cuenta Remunerada", Description: "From savings",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "coffee", AmountMinor: -350, Currency: "EUR",
			BookingDate: day, Counterparty: "Coffee", Description: "Coffee",
			Category: "Restaurants & Delivery", Status: domain.TransactionStatusBooked},
	}
	for index := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	linked, err := store.LinkAccruingTransfers(ctx)
	if err != nil {
		t.Fatalf("LinkAccruingTransfers: %v", err)
	}
	if linked != 2 {
		t.Fatalf("linked = %d, want 2", linked)
	}
	outflow, err := store.GetTransaction(ctx, transactions[0].ID)
	if err != nil {
		t.Fatalf("GetTransaction outflow: %v", err)
	}
	inflow, err := store.GetTransaction(ctx, transactions[1].ID)
	if err != nil {
		t.Fatalf("GetTransaction inflow: %v", err)
	}
	if outflow.Category != accrual.CategorySavings || outflow.IsBudgetExcluded {
		t.Fatalf("savings outflow = %+v", outflow)
	}
	if inflow.Category != accrual.CategoryWithdrawal || !inflow.IsBudgetExcluded {
		t.Fatalf("savings inflow = %+v", inflow)
	}
	spending, err := store.SpendingByCategory(ctx, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("SpendingByCategory: %v", err)
	}
	var savingsSpend int64
	for _, item := range spending {
		if item.Category == accrual.CategorySavings {
			savingsSpend += item.Minor
		}
	}
	if savingsSpend != 50_000 {
		t.Fatalf("savings spend = %d, want 50000: %+v", savingsSpend, spending)
	}
	paired, err := store.NeutralizePassThroughs(ctx, 3)
	if err != nil {
		t.Fatalf("NeutralizePassThroughs: %v", err)
	}
	if paired != 0 {
		t.Fatalf("excluded savings inflow was paired: %d", paired)
	}
}
