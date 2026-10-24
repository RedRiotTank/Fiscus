package budgets

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// PlanStore persists income-driven allocation profiles.
type PlanStore interface {
	GetBudgetProfile(ctx context.Context, workspace, currency string) (domain.BudgetProfile, bool, error)
	ListBudgetAllocations(ctx context.Context, workspace, currency string) ([]domain.BudgetAllocation, error)
	SaveBudgetPlan(ctx context.Context, profile domain.BudgetProfile, allocations []domain.BudgetAllocation) error
	SetBudgetProfileAdvisory(ctx context.Context, workspace, currency string, enabled bool) error
}

// StoredPlan is the persisted allocation state for one workspace/currency.
type StoredPlan struct {
	Profile     Profile
	Allocations []Allocation
	Exists      bool
}

// Planner loads and saves income-driven allocation plans.
type Planner struct {
	store PlanStore
	now   func() time.Time
}

// NewPlanner constructs a planner backed by the given store.
func NewPlanner(store PlanStore, now func() time.Time) *Planner {
	if now == nil {
		now = time.Now
	}
	return &Planner{store: store, now: now}
}

// Load returns a stored plan or a default profile when none exists.
func (p *Planner) Load(ctx context.Context, workspace, currency string) (StoredPlan, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	out := StoredPlan{Profile: DefaultProfile(workspace, currency)}
	if p == nil || p.store == nil {
		return out, nil
	}
	profile, exists, err := p.store.GetBudgetProfile(ctx, workspace, currency)
	if err != nil {
		return StoredPlan{}, err
	}
	if !exists {
		return out, nil
	}
	out.Exists = true
	out.Profile = Profile{
		Workspace: profile.Workspace, Currency: profile.Currency,
		NeedsBPS: profile.NeedsBPS, WantsBPS: profile.WantsBPS, SavingsBPS: profile.SavingsBPS,
		AdvisoryEnabled: profile.AdvisoryEnabled,
	}
	storedAllocations, err := p.store.ListBudgetAllocations(ctx, workspace, currency)
	if err != nil {
		return StoredPlan{}, err
	}
	for _, allocation := range storedAllocations {
		out.Allocations = append(out.Allocations, Allocation{
			Category: allocation.Category, Pillar: Pillar(allocation.Pillar),
			PercentBPS: allocation.PercentBPS, NotifyAtPercent: allocation.NotifyAtPercent,
			SortOrder: allocation.SortOrder, Custom: allocation.Custom,
		})
	}
	out.Allocations = NormalizeAllocations(out.Allocations)
	return out, nil
}

// Save persists a normalized profile and its allocations.
func (p *Planner) Save(ctx context.Context, profile Profile, allocations []Allocation) error {
	if p == nil || p.store == nil {
		return errors.New("budgets: plan persistence is not configured")
	}
	if profile.NeedsBPS < 0 || profile.WantsBPS < 0 || profile.SavingsBPS < 0 ||
		profile.NeedsBPS+profile.WantsBPS+profile.SavingsBPS > 10000 {
		return errors.New("budgets: pillar targets cannot exceed 100%")
	}
	profile = NormalizeProfile(profile)
	if profile.Workspace == "" || profile.Currency == "" {
		return errors.New("budgets: workspace and currency are required")
	}
	allocations = NormalizeAllocations(allocations)
	domainAllocations := make([]domain.BudgetAllocation, 0, len(allocations))
	for _, allocation := range allocations {
		domainAllocations = append(domainAllocations, domain.BudgetAllocation{
			Workspace: profile.Workspace, Currency: profile.Currency,
			Category: allocation.Category, Pillar: string(allocation.Pillar),
			PercentBPS: allocation.PercentBPS, NotifyAtPercent: allocation.NotifyAtPercent,
			SortOrder: allocation.SortOrder, Custom: allocation.Custom, UpdatedAt: p.now().UTC(),
		})
	}
	return p.store.SaveBudgetPlan(ctx, domain.BudgetProfile{
		Workspace: profile.Workspace, Currency: profile.Currency,
		NeedsBPS: profile.NeedsBPS, WantsBPS: profile.WantsBPS, SavingsBPS: profile.SavingsBPS,
		AdvisoryEnabled: profile.AdvisoryEnabled, UpdatedAt: p.now().UTC(),
	}, domainAllocations)
}

// SetAdvisoryEnabled updates the advisory toggle for an existing profile.
func (p *Planner) SetAdvisoryEnabled(ctx context.Context, workspace, currency string, enabled bool) error {
	if p == nil || p.store == nil {
		return errors.New("budgets: plan persistence is not configured")
	}
	return p.store.SetBudgetProfileAdvisory(ctx, workspace, currency, enabled)
}
