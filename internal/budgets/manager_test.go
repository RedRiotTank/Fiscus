package budgets

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type fakeBudgetStore struct {
	overrides []domain.BudgetOverride
	settings  map[string]string
}

func (f *fakeBudgetStore) ListBudgetOverrides(context.Context) ([]domain.BudgetOverride, error) {
	return f.overrides, nil
}

func (f *fakeBudgetStore) UpsertBudgetOverride(_ context.Context, override domain.BudgetOverride) error {
	for i := range f.overrides {
		if f.overrides[i].Category == override.Category && f.overrides[i].Currency == override.Currency {
			f.overrides[i] = override
			return nil
		}
	}
	f.overrides = append(f.overrides, override)
	return nil
}

func (f *fakeBudgetStore) GetSetting(_ context.Context, key string) (string, bool, error) {
	value, ok := f.settings[key]
	return value, ok, nil
}

func (f *fakeBudgetStore) SetSetting(_ context.Context, key, value string) error {
	if f.settings == nil {
		f.settings = make(map[string]string)
	}
	f.settings[key] = value
	return nil
}

func TestManagerMergesOverridesAndSettings(t *testing.T) {
	t.Parallel()
	store := &fakeBudgetStore{
		overrides: []domain.BudgetOverride{
			{Category: "Restaurants", LimitMinor: 45_000, Currency: "EUR", NotifyAtPercent: []int{90, 100}},
			{Category: "Travel", LimitMinor: 20_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		},
		settings: map[string]string{
			"budget_advisory_enabled": "false",
			"budget_advisory_model":   "normative",
		},
	}
	manager := NewManager(store, []Budget{
		{Category: "Restaurants", LimitMinor: 30_000, Currency: "EUR"},
	}, AdvisoryConfig{Enabled: true, Model: AdvisoryModelHybrid, NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20}, time.Now)
	active, err := manager.Active(context.Background())
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if len(active) != 2 || active[0].Category != "Restaurants" || active[0].LimitMinor != 45_000 ||
		active[1].Category != "Travel" {
		t.Fatalf("active = %+v", active)
	}
	advisory, err := manager.Advisory(context.Background())
	if err != nil {
		t.Fatalf("Advisory: %v", err)
	}
	if advisory.Enabled || advisory.Model != AdvisoryModelNormative {
		t.Fatalf("advisory = %+v", advisory)
	}
	if err := manager.SetAdvisoryEnabled(context.Background(), true); err != nil {
		t.Fatalf("SetAdvisoryEnabled: %v", err)
	}
	if err := manager.SetModel(context.Background(), "heuristic"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	advisory, err = manager.Advisory(context.Background())
	if err != nil {
		t.Fatalf("Advisory after update: %v", err)
	}
	if !advisory.Enabled || advisory.Model != AdvisoryModelHeuristic {
		t.Fatalf("updated advisory = %+v", advisory)
	}
}

func TestManagerApplyRecommendations(t *testing.T) {
	t.Parallel()
	store := &fakeBudgetStore{}
	manager := NewManager(store, []Budget{
		{Category: "Restaurants", LimitMinor: 30_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
		{Category: "Groceries", LimitMinor: 25_000, Currency: "EUR", NotifyAtPercent: []int{80, 100}},
	}, AdvisoryConfig{Enabled: true, Model: AdvisoryModelHybrid, NeedsPercent: 50, WantsPercent: 30, SavingsPercent: 20}, time.Now)
	advisories := []Advisory{{
		Currency: "EUR",
		Categories: []CategoryAdvisory{
			{Category: "Restaurants", RecommendedMinor: 28_000},
			{Category: "Groceries", RecommendedMinor: 22_000},
		},
	}}
	count, err := manager.Apply(context.Background(), advisories)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if count != 2 || len(store.overrides) != 2 {
		t.Fatalf("applied = %d overrides=%+v", count, store.overrides)
	}
	if store.overrides[0].Category != "Restaurants" || store.overrides[0].LimitMinor != 28_000 {
		t.Fatalf("restaurants override = %+v", store.overrides[0])
	}
	if len(store.overrides[0].NotifyAtPercent) != 2 {
		t.Fatalf("notify thresholds = %+v", store.overrides[0].NotifyAtPercent)
	}
}
