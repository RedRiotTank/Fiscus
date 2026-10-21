package alerts

import (
	"context"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// LiquidityRule raises a critical alert when an account balance drops below the
// configured threshold for its currency.
type LiquidityRule struct {
	thresholds map[string]int64
}

// NewLiquidityRule copies per-currency thresholds expressed in minor units.
func NewLiquidityRule(thresholds map[string]int64) *LiquidityRule {
	copied := make(map[string]int64, len(thresholds))
	for currency, amount := range thresholds {
		c := strings.ToUpper(strings.TrimSpace(currency))
		if c == "" || amount <= 0 {
			continue
		}
		copied[c] = amount
	}
	return &LiquidityRule{thresholds: copied}
}

// Name implements BalanceRule.
func (r *LiquidityRule) Name() string { return RuleLiquidity }

// Evaluate implements BalanceRule.
func (r *LiquidityRule) Evaluate(_ context.Context, account domain.Account) ([]domain.Alert, error) {
	if len(r.thresholds) == 0 {
		return nil, nil
	}
	currency := strings.ToUpper(account.Currency)
	threshold, ok := r.thresholds[currency]
	if !ok {
		return nil, nil
	}
	if account.BalanceMinor >= threshold {
		return nil, nil
	}
	day := account.UpdatedAt
	if day.IsZero() {
		return nil, nil
	}
	alert := domain.Alert{
		Rule:     RuleLiquidity,
		Severity: domain.SeverityCritical,
		Title:    "Low balance",
		Message: fmt.Sprintf(
			"%s has %s %s available, below the configured %s %s liquidity threshold.",
			accountLabel(account),
			money.FormatGrouped(account.BalanceMinor, currency), currency,
			money.FormatGrouped(threshold, currency), currency,
		),
		AccountID: account.ID,
		DedupKey:  fmt.Sprintf("liquidity:%s:%s", account.ID, day.UTC().Format("2006-01-02")),
	}
	return []domain.Alert{alert}, nil
}
