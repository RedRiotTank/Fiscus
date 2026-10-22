// Package monitoring implements proactive watchers for expected income and
// dormant subscriptions. Monetary values are integer minor units.
package monitoring

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// RuleExpectedInflow is the alert rule name for missing monthly inflows.
const RuleExpectedInflow = "expected_inflow"

// TransactionReader supplies transactions for a time window.
type TransactionReader interface {
	ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
}

// Inflow describes a recurring credit expected each calendar month.
type Inflow struct {
	Name         string
	AmountMin    int64
	Currency     string
	ExpectedDays []int
	GraceDays    int
}

// Normalize applies defaults and sorts the expected-day window.
func (i Inflow) Normalize() Inflow {
	i.Name = strings.TrimSpace(i.Name)
	i.Currency = strings.ToUpper(strings.TrimSpace(i.Currency))
	if i.Currency == "" {
		i.Currency = "EUR"
	}
	if i.GraceDays <= 0 {
		i.GraceDays = 2
	}
	days := append([]int(nil), i.ExpectedDays...)
	sort.Ints(days)
	i.ExpectedDays = days
	return i
}

// InflowRule raises one alert per month when a configured credit does not
// arrive inside its expected-day window plus grace period.
type InflowRule struct {
	store   TransactionReader
	inflows []Inflow
	now     func() time.Time
}

// NewInflowRule constructs an expected-inflow watcher.
func NewInflowRule(store TransactionReader, inflows []Inflow, now func() time.Time) (*InflowRule, error) {
	if store == nil {
		return nil, errors.New("monitoring: transaction reader is required")
	}
	if now == nil {
		now = time.Now
	}
	normalized := make([]Inflow, 0, len(inflows))
	for _, inflow := range inflows {
		normalized = append(normalized, inflow.Normalize())
	}
	return &InflowRule{store: store, inflows: normalized, now: now}, nil
}

// Name implements alerts.AnalysisRule.
func (r *InflowRule) Name() string { return RuleExpectedInflow }

// Evaluate implements alerts.AnalysisRule.
func (r *InflowRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	if len(r.inflows) == 0 {
		return nil, nil
	}
	now := r.now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	out := make([]domain.Alert, 0)
	for _, inflow := range r.inflows {
		if len(inflow.ExpectedDays) == 0 || inflow.AmountMin <= 0 || inflow.Name == "" {
			continue
		}
		firstDay := min(max(inflow.ExpectedDays[0], 1), daysInMonth)
		lastDay := min(max(inflow.ExpectedDays[len(inflow.ExpectedDays)-1], firstDay), daysInMonth)
		windowStart := time.Date(now.Year(), now.Month(), firstDay, 0, 0, 0, 0, time.UTC)
		windowEnd := time.Date(now.Year(), now.Month(), lastDay, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
		deadline := windowEnd.AddDate(0, 0, inflow.GraceDays)
		if now.Before(deadline) {
			continue
		}
		transactions, err := r.store.ListTransactionsBetween(ctx, windowStart, deadline)
		if err != nil {
			return nil, fmt.Errorf("monitoring: load expected inflow window for %q: %w", inflow.Name, err)
		}
		if inflowReceived(transactions, inflow) {
			continue
		}
		out = append(out, domain.Alert{
			Rule:     RuleExpectedInflow,
			Severity: domain.SeverityWarning,
			Title:    fmt.Sprintf("Missing expected inflow: %s", inflow.Name),
			Message: fmt.Sprintf(
				"Expected at least %s %s from %q between day %d and day %d; not found after a %d-day grace period (deadline %s).",
				money.FormatGrouped(inflow.AmountMin, inflow.Currency), inflow.Currency,
				inflow.Name, firstDay, lastDay, inflow.GraceDays, deadline.Add(-time.Second).Format("2 Jan 2006"),
			),
			DedupKey: fmt.Sprintf("inflow:%s:%s:%s",
				strings.ToLower(inflow.Name), inflow.Currency, monthStart.Format("2006-01")),
		})
	}
	return out, nil
}

func inflowReceived(transactions []domain.Transaction, inflow Inflow) bool {
	name := strings.ToLower(strings.TrimSpace(inflow.Name))
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor < inflow.AmountMin {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(tx.Currency), inflow.Currency) {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(tx.Counterparty + " " + tx.Description))
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
}
