package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

func TestRepairRecurringBillCategoriesMovesFixedBillOutOfSubscriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "recurring-repair.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{
		RequisitionID: "req-recurring", InstitutionID: "INST", InstitutionName: "Bank", Status: "LN",
	}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	account := domain.Account{
		ID: "acc-recurring", ConnectionID: connection.ID, Name: "Checking",
		Currency: "EUR", Type: domain.AccountTypePersonal,
	}
	if err := store.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if _, err := store.SaveRecurringBillRule(ctx, domain.RecurringBillRule{
		Workspace: "personal", Currency: "EUR", Counterparty: "Alberto Avidad Fernández",
		Category: "Utilities & Telecom", Pillar: "needs", Recurrence: domain.RecurringMonthly,
		SplitMode: domain.RecurringSplitShared, SplitPeople: 3,
		GrossMinor: 599, PersonalShareMinor: 533, Active: true,
	}); err != nil {
		t.Fatalf("SaveRecurringBillRule: %v", err)
	}
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: account.ID, ExternalID: "alberto", AmountMinor: -599, Currency: "EUR", BookingDate: now, Counterparty: "Alberto Avidad Fernández", Description: "Bizum internet", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "amazon", AmountMinor: -499, Currency: "EUR", BookingDate: now, Counterparty: "Amazon Prime", Description: "Amazon Prime", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "google", AmountMinor: -2199, Currency: "EUR", BookingDate: now, Counterparty: "Google", Description: "Google One", Category: "Subscriptions", Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}
	if err := repairRecurringBillCategories(ctx, store); err != nil {
		t.Fatalf("repairRecurringBillCategories: %v", err)
	}
	for _, tx := range transactions {
		loaded, err := store.GetTransaction(ctx, tx.ID)
		if err != nil {
			t.Fatalf("GetTransaction %s: %v", tx.ExternalID, err)
		}
		switch loaded.ExternalID {
		case "alberto":
			if loaded.Category != "Utilities & Telecom" {
				t.Errorf("Alberto category = %q, want Utilities & Telecom", loaded.Category)
			}
		case "amazon", "google":
			if loaded.Category != "Subscriptions" {
				t.Errorf("%s category = %q, want Subscriptions", loaded.ExternalID, loaded.Category)
			}
		}
	}
	learned, err := store.ListLearnedRules(ctx)
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	found := false
	for _, item := range learned {
		if item.Category == "Utilities & Telecom" && item.Source == "recurring:personal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("learned recurring rule missing: %+v", learned)
	}
}
