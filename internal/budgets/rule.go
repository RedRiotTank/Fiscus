package budgets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// RuleBudgetThreshold is the alert rule name for budget pacing.
const RuleBudgetThreshold = "budget_threshold"

// Spender provides category spending for a period.
type Spender interface {
	SpendingByCategory(ctx context.Context, from, to time.Time) ([]storage.CategorySpending, error)
}

// BudgetProvider supplies the effective budgets, including persisted
// overrides.
type BudgetProvider interface {
	Active(ctx context.Context) ([]Budget, error)
}

// Rule evaluates configured monthly category caps.
type Rule struct {
	store    Spender
	budgets  []Budget
	provider BudgetProvider
	now      func() time.Time
}

// NewRule constructs a budget pacing rule. Empty budget lists yield a rule that
// produces no alerts.
func NewRule(store Spender, budgets []Budget, now func() time.Time) (*Rule, error) {
	if store == nil {
		return nil, errors.New("budgets: spender is required")
	}
	if now == nil {
		now = time.Now
	}
	normalized := make([]Budget, 0, len(budgets))
	for _, budget := range budgets {
		normalized = append(normalized, Normalize(budget))
	}
	return &Rule{store: store, budgets: normalized, now: now}, nil
}

// NewRuleWithProvider constructs a budget pacing rule that reads the effective
// budgets (including SQLite overrides) on every evaluation.
func NewRuleWithProvider(store Spender, provider BudgetProvider, now func() time.Time) (*Rule, error) {
	if store == nil {
		return nil, errors.New("budgets: spender is required")
	}
	if provider == nil {
		return nil, errors.New("budgets: budget provider is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Rule{store: store, provider: provider, now: now}, nil
}

// Name implements alerts.AnalysisRule.
func (r *Rule) Name() string { return RuleBudgetThreshold }

// Evaluate implements alerts.AnalysisRule.
func (r *Rule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	active := r.budgets
	if r.provider != nil {
		var err error
		active, err = r.provider.Active(ctx)
		if err != nil {
			return nil, fmt.Errorf("budgets: load effective budgets: %w", err)
		}
	}
	if len(active) == 0 {
		return nil, nil
	}
	now := r.now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	spending, err := r.store.SpendingByCategory(ctx, monthStart, monthStart.AddDate(0, 1, 0))
	if err != nil {
		return nil, fmt.Errorf("budgets: load month-to-date spending: %w", err)
	}
	statuses := Compute(spending, active, now)
	out := make([]domain.Alert, 0, len(statuses))
	for i, status := range statuses {
		threshold, exceeded := highestCrossedThreshold(status.Percent, active[i].NotifyAtPercent)
		if !exceeded {
			continue
		}
		severity := domain.SeverityWarning
		if threshold >= 100 {
			severity = domain.SeverityCritical
		}
		message := status.Description()
		if status.RemainingMinor < 0 {
			message += fmt.Sprintf("; over by %s %s",
				money.FormatGrouped(-status.RemainingMinor, status.Currency), status.Currency)
		}
		out = append(out, domain.Alert{
			Rule:     RuleBudgetThreshold,
			Severity: severity,
			Title:    fmt.Sprintf("Budget threshold reached: %s", status.Category),
			Message:  message,
			DedupKey: fmt.Sprintf("budget:%s:%s:%s:%d",
				strings.ToLower(status.Category), status.Currency, monthStart.Format("2006-01"), threshold),
		})
	}
	return out, nil
}

func highestCrossedThreshold(percent int64, thresholds []int) (int, bool) {
	highest := 0
	for _, threshold := range thresholds {
		if percent >= int64(threshold) && threshold > highest {
			highest = threshold
		}
	}
	return highest, highest > 0
}
