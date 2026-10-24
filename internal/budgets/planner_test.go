package budgets

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakePlanStore struct {
	profile     domain.BudgetProfile
	exists      bool
	allocations []domain.BudgetAllocation
	advisory    *bool
}

func (f *fakePlanStore) GetBudgetProfile(context.Context, string, string) (domain.BudgetProfile, bool, error) {
	return f.profile, f.exists, nil
}

func (f *fakePlanStore) ListBudgetAllocations(context.Context, string, string) ([]domain.BudgetAllocation, error) {
	return f.allocations, nil
}

func (f *fakePlanStore) SaveBudgetPlan(_ context.Context, profile domain.BudgetProfile, allocations []domain.BudgetAllocation) error {
	f.profile, f.exists, f.allocations = profile, true, allocations
	return nil
}

func (f *fakePlanStore) SetBudgetProfileAdvisory(_ context.Context, _, _ string, enabled bool) error {
	f.advisory = &enabled
	f.profile.AdvisoryEnabled = enabled
	return nil
}

func TestPlannerLoadSaveAndAdvisory(t *testing.T) {
	t.Parallel()
	store := &fakePlanStore{}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	planner := NewPlanner(store, func() time.Time { return now })
	loaded, err := planner.Load(context.Background(), "personal", "EUR")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Exists || loaded.Profile.NeedsBPS != 5000 {
		t.Fatalf("default plan = %+v", loaded)
	}
	err = planner.Save(context.Background(), Profile{
		Workspace: "personal", Currency: "EUR",
		NeedsBPS: 6000, WantsBPS: 2000, SavingsBPS: 2000, AdvisoryEnabled: true,
	}, []Allocation{
		{Category: "Rent", Pillar: PillarNeeds, PercentBPS: 3000, NotifyAtPercent: []int{80, 100}},
		{Category: "Savings", Pillar: PillarSavings, PercentBPS: 2000},
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(store.allocations) != 2 || store.allocations[0].Category != "Rent" || !store.exists {
		t.Fatalf("saved = %+v", store)
	}
	loaded, err = planner.Load(context.Background(), "personal", "EUR")
	if err != nil {
		t.Fatalf("Load saved: %v", err)
	}
	if !loaded.Exists || loaded.Profile.NeedsBPS != 6000 || len(loaded.Allocations) != 2 {
		t.Fatalf("loaded saved = %+v", loaded)
	}
	if err := planner.SetAdvisoryEnabled(context.Background(), "personal", "EUR", false); err != nil {
		t.Fatalf("SetAdvisoryEnabled: %v", err)
	}
	if store.advisory == nil || *store.advisory {
		t.Fatalf("advisory = %+v", store.advisory)
	}
	if err := planner.Save(context.Background(), Profile{
		Workspace: "personal", Currency: "EUR", NeedsBPS: 7000, WantsBPS: 4000, SavingsBPS: 2000,
	}, nil); err == nil {
		t.Fatal("expected pillar over-allocation error")
	}
}
