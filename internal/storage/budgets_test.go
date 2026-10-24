package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestBudgetOverridesAndSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "budgets.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.UpsertBudgetOverride(ctx, domain.BudgetOverride{
		Category: "Restaurants", LimitMinor: 45_000, Currency: "EUR", NotifyAtPercent: []int{80, 100},
	}); err != nil {
		t.Fatalf("UpsertBudgetOverride: %v", err)
	}
	if err := store.UpsertBudgetOverride(ctx, domain.BudgetOverride{
		Category: "restaurants", LimitMinor: 28_000, Currency: "EUR", NotifyAtPercent: []int{90, 100},
	}); err != nil {
		t.Fatalf("UpsertBudgetOverride update: %v", err)
	}
	overrides, err := store.ListBudgetOverrides(ctx)
	if err != nil {
		t.Fatalf("ListBudgetOverrides: %v", err)
	}
	if len(overrides) != 1 || overrides[0].LimitMinor != 28_000 ||
		len(overrides[0].NotifyAtPercent) != 2 || overrides[0].NotifyAtPercent[0] != 90 {
		t.Fatalf("overrides = %+v", overrides)
	}
	if err := store.UpsertBudgetOverride(ctx, domain.BudgetOverride{Category: "Rent", LimitMinor: 0, Currency: "EUR"}); err == nil {
		t.Fatal("expected invalid limit error")
	}

	if _, ok, err := store.GetSetting(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing setting = ok=%v err=%v", ok, err)
	}
	if err := store.SetSetting(ctx, "budget_advisory_enabled", "false"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	value, ok, err := store.GetSetting(ctx, "budget_advisory_enabled")
	if err != nil || !ok || value != "false" {
		t.Fatalf("setting = %q ok=%v err=%v", value, ok, err)
	}
	if err := store.SetSetting(ctx, "", "x"); err == nil {
		t.Fatal("expected empty key error")
	}
}

func TestBudgetOverrideNotFoundSafety(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "budgets.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	overrides, err := store.ListBudgetOverrides(ctx)
	if err != nil || len(overrides) != 0 {
		t.Fatalf("empty overrides = %+v err=%v", overrides, err)
	}
	if err := store.UpsertBudgetOverride(ctx, domain.BudgetOverride{}); err == nil {
		t.Fatal("expected missing category error")
	}
	if _, ok, err := store.GetSetting(ctx, "x"); err != nil || ok {
		t.Fatalf("missing setting = ok=%v err=%v", ok, err)
	}
}
