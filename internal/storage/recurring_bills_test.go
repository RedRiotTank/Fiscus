package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestRecurringBillRulePersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "recurring.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if version, err := store.SchemaVersion(ctx); err != nil || version < 16 {
		t.Fatalf("schema version = %d err=%v", version, err)
	}
	if _, exists, err := store.GetRecurringBillRule(ctx, "personal", "EUR", "Alberto Avidad Fernández"); err != nil || exists {
		t.Fatalf("initial rule exists=%v err=%v", exists, err)
	}

	saved, err := store.SaveRecurringBillRule(ctx, domain.RecurringBillRule{
		Workspace: "personal", Currency: "EUR",
		Counterparty: "Alberto Avidad Fernández", Category: "Utilities & Telecom",
		Pillar: "needs", SplitMode: domain.RecurringSplitShared, SplitPeople: 3,
		GrossMinor: 1600, PersonalShareMinor: 533, Active: true,
	})
	if err != nil {
		t.Fatalf("SaveRecurringBillRule: %v", err)
	}
	if saved.ID == 0 || saved.PersonalShareMinor != 533 || saved.SplitPeople != 3 ||
		saved.Category != "Utilities & Telecom" || saved.CreatedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}

	rules, err := store.ListRecurringBillRules(ctx, "PERSONAL", "eur")
	if err != nil {
		t.Fatalf("ListRecurringBillRules: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != saved.ID {
		t.Fatalf("rules = %+v", rules)
	}

	updated, err := store.SaveRecurringBillRule(ctx, domain.RecurringBillRule{
		Workspace: "personal", Currency: "EUR",
		Counterparty: "Alberto Avidad Fernández", Category: "Internet",
		Pillar: "needs", SplitMode: domain.RecurringSplitSolo, SplitPeople: 1,
		GrossMinor: 1600, PersonalShareMinor: 1600, Active: true,
	})
	if err != nil {
		t.Fatalf("update recurring rule: %v", err)
	}
	if updated.ID != saved.ID || updated.Category != "Internet" || updated.SplitMode != domain.RecurringSplitSolo {
		t.Fatalf("updated = %+v", updated)
	}

	if err := store.DeleteRecurringBillRule(ctx, "personal", "EUR", "Alberto Avidad Fernández"); err != nil {
		t.Fatalf("DeleteRecurringBillRule: %v", err)
	}
	rules, err = store.ListRecurringBillRules(ctx, "personal", "EUR")
	if err != nil || len(rules) != 0 {
		t.Fatalf("after delete rules=%+v err=%v", rules, err)
	}
	if _, err := store.SaveRecurringBillRule(ctx, domain.RecurringBillRule{
		Workspace: "personal", Currency: "EUR", Counterparty: "Bad rule", SplitMode: "invalid",
	}); err == nil {
		t.Fatal("expected invalid split mode error")
	}
}
