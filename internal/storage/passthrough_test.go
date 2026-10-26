package storage

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

func TestNeutralizePassThroughsAndManualUnlink(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	connection := seedConnection(t, store)
	account := seedAccount(t, store, connection.ID)
	day := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{
			AccountID: account.ID, ExternalID: "mirror-in", AmountMinor: 58_556, Currency: "EUR",
			BookingDate: day, Counterparty: "Cuenta Remunerada", Description: "Transferencia interna",
			Category: "Transfers / P2P", Status: domain.TransactionStatusBooked,
		},
		{
			AccountID: account.ID, ExternalID: "mirror-out", AmountMinor: -58_556, Currency: "EUR",
			BookingDate: day.Add(5 * time.Minute), Counterparty: "Ismael Díaz Díaz",
			Description: "To Ismael Díaz Díaz", Category: "Transfers / P2P",
			Status: domain.TransactionStatusBooked,
		},
	}
	for index := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[index]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	paired, err := store.NeutralizePassThroughs(ctx, transfers.DefaultWindowDays)
	if err != nil {
		t.Fatalf("NeutralizePassThroughs: %v", err)
	}
	if paired != 1 {
		t.Fatalf("paired = %d, want 1", paired)
	}
	inflow, err := store.GetTransaction(ctx, transactions[0].ID)
	if err != nil {
		t.Fatalf("GetTransaction inflow: %v", err)
	}
	outflow, err := store.GetTransaction(ctx, transactions[1].ID)
	if err != nil {
		t.Fatalf("GetTransaction outflow: %v", err)
	}
	if inflow.Category != transfers.CategoryPassThrough || outflow.Category != transfers.CategoryPassThrough {
		t.Fatalf("categories = %q/%q", inflow.Category, outflow.Category)
	}
	if !inflow.IsBudgetExcluded || !outflow.IsBudgetExcluded {
		t.Fatalf("budget exclusion flags = %v/%v", inflow.IsBudgetExcluded, outflow.IsBudgetExcluded)
	}
	if inflow.PairedTransactionID == nil || *inflow.PairedTransactionID != outflow.ID ||
		outflow.PairedTransactionID == nil || *outflow.PairedTransactionID != inflow.ID {
		t.Fatalf("pair links = %+v/%+v", inflow.PairedTransactionID, outflow.PairedTransactionID)
	}

	resync := transactions[0]
	resync.Category = "Transfers / P2P"
	if _, _, err := store.UpsertTransaction(ctx, &resync); err != nil {
		t.Fatalf("UpsertTransaction resync: %v", err)
	}
	resynced, err := store.GetTransaction(ctx, inflow.ID)
	if err != nil {
		t.Fatalf("GetTransaction resynced: %v", err)
	}
	if resynced.Category != transfers.CategoryPassThrough || !resynced.IsBudgetExcluded {
		t.Fatalf("resync cleared neutralization: %+v", resynced)
	}

	spending, err := store.SpendingByCategory(ctx, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("SpendingByCategory: %v", err)
	}
	if len(spending) != 0 {
		t.Fatalf("neutralized pass-through leaked into spending: %+v", spending)
	}

	if err := store.UpdateTransactionCategory(ctx, outflow.ID, "Groceries"); err != nil {
		t.Fatalf("UpdateTransactionCategory: %v", err)
	}
	unlinkedOutflow, err := store.GetTransaction(ctx, outflow.ID)
	if err != nil {
		t.Fatalf("GetTransaction unlinked outflow: %v", err)
	}
	unlinkedInflow, err := store.GetTransaction(ctx, inflow.ID)
	if err != nil {
		t.Fatalf("GetTransaction unlinked inflow: %v", err)
	}
	if unlinkedOutflow.Category != "Groceries" || unlinkedOutflow.IsBudgetExcluded ||
		unlinkedOutflow.PairedTransactionID != nil {
		t.Fatalf("unlinked outflow = %+v", unlinkedOutflow)
	}
	if unlinkedInflow.Category != "Transfers / P2P" || unlinkedInflow.IsBudgetExcluded ||
		unlinkedInflow.PairedTransactionID != nil {
		t.Fatalf("unlinked inflow = %+v", unlinkedInflow)
	}
	if !unlinkedOutflow.PassThroughSuppressed || !unlinkedInflow.PassThroughSuppressed {
		t.Fatal("manual unlink did not suppress automatic re-pairing")
	}
	repaired, err := store.NeutralizePassThroughs(ctx, transfers.DefaultWindowDays)
	if err != nil {
		t.Fatalf("NeutralizePassThroughs after unlink: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("manual unlink was automatically re-paired: %d", repaired)
	}

	spending, err = store.SpendingByCategory(ctx, day, day.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("SpendingByCategory after unlink: %v", err)
	}
	if len(spending) != 1 || spending[0].Category != "Groceries" || spending[0].Minor != 58_556 {
		t.Fatalf("spending after unlink = %+v", spending)
	}

	linked, err := store.GetTransactionsByIDs(ctx, []int64{outflow.ID, inflow.ID})
	if err != nil || len(linked) != 2 {
		t.Fatalf("GetTransactionsByIDs = %+v err=%v", linked, err)
	}
}
