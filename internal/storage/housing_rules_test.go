package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestHousingRulePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "housing.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, exists, err := store.GetHousingRule(ctx, "personal", "EUR"); err != nil || exists {
		t.Fatalf("initial rule exists=%v err=%v", exists, err)
	}
	saved, err := store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: "personal", Currency: "EUR", LandlordCounterparty: "Sonia Reyes Álvarez",
		PersonalNetShareCents: 35_000, CalculationMode: domain.HousingModeFixedShare,
		FlatmateCounterparties: []string{"Ismael Díaz", "Jose Hinojos", "Ismael Díaz"},
	})
	if err != nil {
		t.Fatalf("SaveHousingRule: %v", err)
	}
	if saved.ID == 0 || saved.LandlordCounterparty != "Sonia Reyes Álvarez" ||
		saved.PersonalNetShareCents != 35_000 || saved.CalculationMode != domain.HousingModeFixedShare {
		t.Fatalf("saved = %+v", saved)
	}
	if len(saved.FlatmateCounterparties) != 2 {
		t.Fatalf("flatmates = %+v", saved.FlatmateCounterparties)
	}
	loaded, exists, err := store.GetHousingRule(ctx, "PERSONAL", "eur")
	if err != nil || !exists {
		t.Fatalf("GetHousingRule exists=%v err=%v", exists, err)
	}
	if loaded.ID != saved.ID || loaded.UpdatedAt.IsZero() || loaded.CreatedAt.IsZero() {
		t.Fatalf("loaded = %+v", loaded)
	}
	updated, err := store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: "personal", Currency: "EUR", LandlordCounterparty: "Sonia Reyes Álvarez",
		PersonalNetShareCents: 40_000, CalculationMode: domain.HousingModeNetBalance,
		FlatmateCounterparties: []string{"Ismael Díaz"},
	})
	if err != nil {
		t.Fatalf("update housing rule: %v", err)
	}
	if updated.ID != saved.ID || updated.PersonalNetShareCents != 40_000 ||
		updated.CalculationMode != domain.HousingModeNetBalance {
		t.Fatalf("updated = %+v", updated)
	}
	if err := store.DeleteHousingRule(ctx, "personal", "EUR"); err != nil {
		t.Fatalf("DeleteHousingRule: %v", err)
	}
	if _, exists, err := store.GetHousingRule(ctx, "personal", "EUR"); err != nil || exists {
		t.Fatalf("after delete exists=%v err=%v", exists, err)
	}
	if _, err := store.SaveHousingRule(ctx, domain.HousingRule{
		Workspace: "personal", Currency: "EUR", LandlordCounterparty: "Sonia",
		CalculationMode: "invalid",
	}); err == nil {
		t.Fatal("expected invalid calculation mode error")
	}
}
