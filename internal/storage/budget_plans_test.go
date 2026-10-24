package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

func TestBudgetPlansPersistPerWorkspace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	personal := domain.BudgetProfile{
		Workspace: "personal", Currency: "EUR",
		NeedsBPS: 5000, WantsBPS: 3000, SavingsBPS: 2000,
		AdvisoryEnabled: true, UpdatedAt: now,
	}
	personalAllocations := []domain.BudgetAllocation{
		{Category: "Rent", Pillar: "needs", PercentBPS: 3036, SortOrder: 0, NotifyAtPercent: []int{80, 100}},
		{Category: "Groceries", Pillar: "needs", PercentBPS: 1250, SortOrder: 1, NotifyAtPercent: []int{80, 100}},
		{Category: "Restaurants", Pillar: "wants", PercentBPS: 1071, SortOrder: 2, NotifyAtPercent: []int{80, 100}},
		{Category: "Emergency Fund", Pillar: "savings", PercentBPS: 1071, SortOrder: 3, NotifyAtPercent: []int{80, 100}},
	}
	if err := store.SaveBudgetPlan(ctx, personal, personalAllocations); err != nil {
		t.Fatalf("SaveBudgetPlan personal: %v", err)
	}
	business := domain.BudgetProfile{
		Workspace: "business", Currency: "EUR",
		NeedsBPS: 2100, WantsBPS: 0, SavingsBPS: 2100,
		AdvisoryEnabled: true, UpdatedAt: now,
	}
	if err := store.SaveBudgetPlan(ctx, business, []domain.BudgetAllocation{
		{Category: "Software & Cloud", Pillar: "needs", PercentBPS: 818, SortOrder: 0},
		{Category: "Tax Reserve", Pillar: "savings", PercentBPS: 2100, SortOrder: 1},
	}); err != nil {
		t.Fatalf("SaveBudgetPlan business: %v", err)
	}

	profile, ok, err := store.GetBudgetProfile(ctx, "PERSONAL", "eur")
	if err != nil || !ok {
		t.Fatalf("GetBudgetProfile = ok=%v err=%v", ok, err)
	}
	if profile.NeedsBPS != 5000 || !profile.AdvisoryEnabled {
		t.Fatalf("profile = %+v", profile)
	}
	allocations, err := store.ListBudgetAllocations(ctx, "personal", "EUR")
	if err != nil {
		t.Fatalf("ListBudgetAllocations: %v", err)
	}
	if len(allocations) != 4 || allocations[0].Category != "Rent" || allocations[0].PercentBPS != 3036 {
		t.Fatalf("personal allocations = %+v", allocations)
	}
	businessAllocations, err := store.ListBudgetAllocations(ctx, "business", "EUR")
	if err != nil {
		t.Fatalf("ListBudgetAllocations business: %v", err)
	}
	if len(businessAllocations) != 2 {
		t.Fatalf("business allocations = %+v", businessAllocations)
	}
	if err := store.SetBudgetProfileAdvisory(ctx, "personal", "EUR", false); err != nil {
		t.Fatalf("SetBudgetProfileAdvisory: %v", err)
	}
	profile, ok, err = store.GetBudgetProfile(ctx, "personal", "EUR")
	if err != nil || !ok || profile.AdvisoryEnabled {
		t.Fatalf("advisory toggle = %+v ok=%v err=%v", profile, ok, err)
	}
	if err := store.SaveBudgetPlan(ctx, personal, []domain.BudgetAllocation{
		{Category: "Groceries", Pillar: "needs", PercentBPS: 1250},
	}); err != nil {
		t.Fatalf("SaveBudgetPlan replace: %v", err)
	}
	allocations, err = store.ListBudgetAllocations(ctx, "personal", "EUR")
	if err != nil || len(allocations) != 1 || allocations[0].Category != "Groceries" {
		t.Fatalf("replaced allocations = %+v err=%v", allocations, err)
	}
	if err := store.SaveBudgetPlan(ctx, domain.BudgetProfile{
		Workspace: "personal", Currency: "EUR", NeedsBPS: 5000, WantsBPS: 4000, SavingsBPS: 2000,
	}, nil); err == nil {
		t.Fatal("expected pillar sum over 100% error")
	}
}
