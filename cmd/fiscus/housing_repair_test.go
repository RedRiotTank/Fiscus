package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/categories"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
	"github.com/RedRiotTank/Fiscus/pkg/housing"
)

func TestRepairLegacyHousingCategories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "housing-repair.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection := domain.Connection{
		RequisitionID: "req-repair", InstitutionID: "INST", InstitutionName: "Bank", Status: "LN",
	}
	if err := store.UpsertConnection(ctx, &connection); err != nil {
		t.Fatalf("UpsertConnection: %v", err)
	}
	account := domain.Account{
		ID: "acc-repair", ConnectionID: connection.ID, Name: "Checking",
		Currency: "EUR", Type: domain.AccountTypePersonal,
	}
	if err := store.UpsertAccount(ctx, &account); err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	rule, err := store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: "personal", Currency: "EUR", LandlordCounterparty: "Sonia Reyes",
		PersonalNetShareCents: 35_000, CalculationMode: domain.HousingModeFixedShare,
		FlatmateCounterparties: []string{"Ismael Díaz"},
	})
	if err != nil {
		t.Fatalf("SaveHousingRule: %v", err)
	}
	if _, err := store.UpsertLearnedRule(ctx, categories.LearnedPattern("Ismael Díaz"), housing.Category, "housing:personal"); err != nil {
		t.Fatalf("UpsertLearnedRule: %v", err)
	}
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	transactions := []domain.Transaction{
		{AccountID: account.ID, ExternalID: "micro", AmountMinor: 170, Currency: "EUR", BookingDate: now, Counterparty: "Ismael Díaz Díaz", Description: "From Ismael Díaz Díaz", Category: housing.Category, Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "rent", AmountMinor: 35_000, Currency: "EUR", BookingDate: now, Counterparty: "Ismael D", Description: "Piso octubre | From Ismael D", Category: housing.Category, Status: domain.TransactionStatusBooked},
		{AccountID: account.ID, ExternalID: "dia", AmountMinor: 1_515, Currency: "EUR", BookingDate: now, Counterparty: "Ismael D", Description: "Dia | From Ismael D", Category: housing.Category, Status: domain.TransactionStatusBooked},
	}
	for i := range transactions {
		if _, _, err := store.UpsertTransaction(ctx, &transactions[i]); err != nil {
			t.Fatalf("UpsertTransaction: %v", err)
		}
	}

	if err := repairLegacyHousingCategories(ctx, store); err != nil {
		t.Fatalf("repairLegacyHousingCategories: %v", err)
	}
	byExternal := make(map[string]domain.Transaction, len(transactions))
	for _, tx := range transactions {
		loaded, err := store.GetTransaction(ctx, tx.ID)
		if err != nil {
			t.Fatalf("GetTransaction %s: %v", tx.ExternalID, err)
		}
		byExternal[loaded.ExternalID] = loaded
	}
	if got := byExternal["micro"].Category; got != categories.CategoryP2P {
		t.Errorf("micro category = %q, want %q", got, categories.CategoryP2P)
	}
	if got := byExternal["rent"].Category; got != housing.Category {
		t.Errorf("rent category = %q, want %q", got, housing.Category)
	}
	if got := byExternal["dia"].Category; got != "Groceries" {
		t.Errorf("dia category = %q, want Groceries", got)
	}
	learned, err := store.ListLearnedRules(ctx)
	if err != nil {
		t.Fatalf("ListLearnedRules: %v", err)
	}
	for _, item := range learned {
		if strings.EqualFold(item.Category, housing.Category) && strings.Contains(item.Pattern, "Ismael") {
			t.Fatalf("broad flatmate rule survived repair: %+v", item)
		}
	}
	broadPattern := categories.LearnedPattern("Ismael Díaz")
	foundLandlord := false
	for _, item := range learned {
		if item.Category == housing.Category && item.Source == "housing:personal" && item.Pattern != broadPattern {
			foundLandlord = true
		}
	}
	if !foundLandlord {
		t.Fatalf("landlord housing rule was not restored: %+v", learned)
	}
	if rule.LandlordCounterparty != "Sonia Reyes" {
		t.Fatalf("unexpected rule = %+v", rule)
	}
}
