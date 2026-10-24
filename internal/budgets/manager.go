package budgets

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const (
	settingAdvisoryEnabled = "budget_advisory_enabled"
	settingAdvisoryModel   = "budget_advisory_model"
)

// BudgetStore persists user-adopted budgets and advisory settings.
type BudgetStore interface {
	ListBudgetOverrides(ctx context.Context) ([]domain.BudgetOverride, error)
	UpsertBudgetOverride(ctx context.Context, override domain.BudgetOverride) error
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Manager merges configured budgets with SQLite overrides and advisory
// settings. It is safe to use from HTTP and Telegram handlers.
type Manager struct {
	store    BudgetStore
	config   []Budget
	advisory AdvisoryConfig
	now      func() time.Time
}

// NewManager constructs a budget manager. A nil store keeps configured budgets
// and settings in memory only.
func NewManager(store BudgetStore, config []Budget, advisory AdvisoryConfig, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	normalized := make([]Budget, 0, len(config))
	for _, budget := range config {
		normalized = append(normalized, Normalize(budget))
	}
	return &Manager{
		store:    store,
		config:   normalized,
		advisory: NormalizeAdvisoryConfig(advisory),
		now:      now,
	}
}

// Active returns configured budgets with persisted overrides applied.
func (m *Manager) Active(ctx context.Context) ([]Budget, error) {
	byKey := make(map[string]Budget, len(m.config))
	order := make([]string, 0, len(m.config))
	for _, budget := range m.config {
		key := budgetKey(budget.Category, budget.Currency)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = budget
	}
	if m.store != nil {
		overrides, err := m.store.ListBudgetOverrides(ctx)
		if err != nil {
			return nil, err
		}
		for _, override := range overrides {
			budget := Normalize(Budget{
				Category:        override.Category,
				LimitMinor:      override.LimitMinor,
				Currency:        override.Currency,
				NotifyAtPercent: override.NotifyAtPercent,
			})
			key := budgetKey(budget.Category, budget.Currency)
			if _, exists := byKey[key]; !exists {
				order = append(order, key)
			}
			byKey[key] = budget
		}
	}
	out := make([]Budget, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out, nil
}

// Advisory returns the effective advisory configuration.
func (m *Manager) Advisory(ctx context.Context) (AdvisoryConfig, error) {
	cfg := NormalizeAdvisoryConfig(m.advisory)
	if m.store == nil {
		return cfg, nil
	}
	if raw, ok, err := m.store.GetSetting(ctx, settingAdvisoryEnabled); err != nil {
		return cfg, err
	} else if ok {
		cfg.Enabled = strings.EqualFold(strings.TrimSpace(raw), "true")
	}
	if raw, ok, err := m.store.GetSetting(ctx, settingAdvisoryModel); err != nil {
		return cfg, err
	} else if ok {
		model := strings.ToLower(strings.TrimSpace(raw))
		switch model {
		case AdvisoryModelNormative, AdvisoryModelHeuristic, AdvisoryModelHybrid:
			cfg.Model = model
		}
	}
	return cfg, nil
}

// SetAdvisoryEnabled persists the UI toggle.
func (m *Manager) SetAdvisoryEnabled(ctx context.Context, enabled bool) error {
	if m.store == nil {
		return errors.New("budgets: persistence is not configured")
	}
	value := "false"
	if enabled {
		value = "true"
	}
	return m.store.SetSetting(ctx, settingAdvisoryEnabled, value)
}

// SetModel persists an explicit advisory model.
func (m *Manager) SetModel(ctx context.Context, model string) error {
	if m.store == nil {
		return errors.New("budgets: persistence is not configured")
	}
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case AdvisoryModelNormative, AdvisoryModelHeuristic, AdvisoryModelHybrid:
	default:
		return errors.New("budgets: invalid advisory model")
	}
	return m.store.SetSetting(ctx, settingAdvisoryModel, model)
}

// Apply persists recommended category limits for every advisory with a
// positive recommendation.
func (m *Manager) Apply(ctx context.Context, advisories []Advisory) (int, error) {
	if m.store == nil {
		return 0, errors.New("budgets: persistence is not configured")
	}
	active, err := m.Active(ctx)
	if err != nil {
		return 0, err
	}
	notifyByKey := make(map[string][]int, len(active))
	for _, budget := range active {
		notifyByKey[budgetKey(budget.Category, budget.Currency)] = budget.NotifyAtPercent
	}
	count := 0
	for _, advisory := range advisories {
		for _, category := range advisory.Categories {
			if category.RecommendedMinor <= 0 {
				continue
			}
			key := budgetKey(category.Category, advisory.Currency)
			notify, activeBudget := notifyByKey[key]
			if !activeBudget {
				// Apply only updates existing category caps; it does not
				// silently create budgets for every spending category.
				continue
			}
			if err := m.store.UpsertBudgetOverride(ctx, domain.BudgetOverride{
				Category:        category.Category,
				LimitMinor:      category.RecommendedMinor,
				Currency:        advisory.Currency,
				NotifyAtPercent: notify,
				UpdatedAt:       m.now().UTC(),
			}); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

func budgetKey(category, currency string) string {
	return strings.ToLower(strings.TrimSpace(category)) + "\x00" + strings.ToUpper(strings.TrimSpace(currency))
}
