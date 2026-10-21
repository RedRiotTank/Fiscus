package alerts

import (
	"context"
	"fmt"
	"math"
	"math/big"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// AnomalyRule flags a debit that exceeds the rolling average of prior debits by
// more than the configured fraction (for example 0.30 for 30%). Comparisons are
// exact integer arithmetic; the threshold fraction is configuration, not money.
type AnomalyRule struct {
	stats        DebitStatistics
	thresholdBPS int64
	lookbackDays int
	minHistory   int
}

// AnomalyConfig configures the spike detector.
type AnomalyConfig struct {
	Threshold    float64
	LookbackDays int
	MinHistory   int
}

// NewAnomalyRule constructs the rule with sensible defaults for zero values.
func NewAnomalyRule(stats DebitStatistics, cfg AnomalyConfig) (*AnomalyRule, error) {
	if stats == nil {
		return nil, fmt.Errorf("alerts: debit statistics are required")
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 0.30
	}
	if cfg.LookbackDays <= 0 {
		cfg.LookbackDays = 90
	}
	if cfg.MinHistory <= 0 {
		cfg.MinHistory = 3
	}
	bps := int64(math.Round(cfg.Threshold * 10000))
	if bps <= 0 {
		bps = 3000
	}
	return &AnomalyRule{
		stats:        stats,
		thresholdBPS: bps,
		lookbackDays: cfg.LookbackDays,
		minHistory:   cfg.MinHistory,
	}, nil
}

// Name implements TransactionRule.
func (r *AnomalyRule) Name() string { return RuleAnomaly }

// Evaluate implements TransactionRule.
func (r *AnomalyRule) Evaluate(ctx context.Context, account domain.Account, tx domain.Transaction) ([]domain.Alert, error) {
	if !tx.IsDebit() {
		return nil, nil
	}
	from := tx.BookingDate.AddDate(0, 0, -r.lookbackDays)
	total, count, err := r.stats.DebitStats(ctx, account.ID, from, tx.BookingDate, tx.ID)
	if err != nil {
		return nil, fmt.Errorf("anomaly rule: %w", err)
	}
	if count < r.minHistory || total <= 0 {
		return nil, nil
	}
	average := total / int64(count)
	if average <= 0 {
		return nil, nil
	}
	amount := absMinor(tx.AmountMinor)
	if amount == 0 || !exceedsThreshold(amount, average, r.thresholdBPS) {
		return nil, nil
	}

	alert := domain.Alert{
		Rule:     RuleAnomaly,
		Severity: domain.SeverityWarning,
		Title:    "Unusual charge amount",
		Message: fmt.Sprintf(
			"A debit of %s %s on %s is %d%% above the %d-transaction average of %s %s for %s over the last %d days.",
			money.FormatGrouped(amount, tx.Currency), tx.Currency,
			tx.BookingDate.Format("2006-01-02"),
			percentAbove(amount, average), count,
			money.FormatGrouped(average, tx.Currency), tx.Currency,
			accountLabel(account), r.lookbackDays,
		),
		AccountID:     account.ID,
		TransactionID: &tx.ID,
		DedupKey:      fmt.Sprintf("anomaly:%s:%d", account.ID, tx.ID),
	}
	return []domain.Alert{alert}, nil
}

// exceedsThreshold reports amount*10000 > average*(10000+bps) exactly.
func exceedsThreshold(amount, average, bps int64) bool {
	left := new(big.Int).Mul(big.NewInt(amount), big.NewInt(10000))
	right := new(big.Int).Mul(big.NewInt(average), big.NewInt(10000+bps))
	return left.Cmp(right) > 0
}

// percentAbove returns floor((amount-average)/average*100), computed with
// integer arithmetic.
func percentAbove(amount, average int64) int64 {
	if average <= 0 {
		return 0
	}
	diff := new(big.Int).Sub(big.NewInt(amount), big.NewInt(average))
	diff.Mul(diff, big.NewInt(100))
	diff.Quo(diff, big.NewInt(average))
	if !diff.IsInt64() {
		return math.MaxInt64
	}
	return diff.Int64()
}

func absMinor(v int64) int64 {
	if v >= 0 {
		return v
	}
	if v == math.MinInt64 {
		return math.MaxInt64
	}
	return -v
}
