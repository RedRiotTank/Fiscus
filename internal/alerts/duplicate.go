package alerts

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// DuplicateRule flags a debit that matches an earlier debit's counterparty and
// exact amount within a configurable window.
type DuplicateRule struct {
	finder DuplicateFinder
	window time.Duration
}

// NewDuplicateRule constructs the rule. A non-positive window defaults to 24h.
func NewDuplicateRule(finder DuplicateFinder, window time.Duration) (*DuplicateRule, error) {
	if finder == nil {
		return nil, fmt.Errorf("alerts: duplicate finder is required")
	}
	if window <= 0 {
		window = 24 * time.Hour
	}
	return &DuplicateRule{finder: finder, window: window}, nil
}

// Name implements TransactionRule.
func (r *DuplicateRule) Name() string { return RuleDuplicate }

// Evaluate implements TransactionRule.
func (r *DuplicateRule) Evaluate(ctx context.Context, account domain.Account, tx domain.Transaction) ([]domain.Alert, error) {
	if !tx.IsDebit() || strings.TrimSpace(tx.Counterparty) == "" {
		return nil, nil
	}
	from := tx.BookingDate.Add(-r.window)
	to := tx.BookingDate.Add(r.window)
	duplicates, err := r.finder.FindDuplicateCandidates(ctx,
		account.ID, strings.TrimSpace(tx.Counterparty), tx.AmountMinor, from, to, tx.ID)
	if err != nil {
		return nil, fmt.Errorf("duplicate rule: %w", err)
	}
	if len(duplicates) == 0 {
		return nil, nil
	}
	original := duplicates[0]
	alert := domain.Alert{
		Rule:     RuleDuplicate,
		Severity: domain.SeverityWarning,
		Title:    "Possible duplicate charge",
		Message: fmt.Sprintf(
			"%s was charged %s %s on %s, matching an earlier charge of %s on %s (%s window).",
			strings.TrimSpace(tx.Counterparty),
			money.FormatGrouped(absMinor(tx.AmountMinor), tx.Currency), tx.Currency,
			tx.BookingDate.Format("2006-01-02"),
			money.FormatGrouped(absMinor(original.AmountMinor), original.Currency),
			original.BookingDate.Format("2006-01-02"),
			humanWindow(r.window),
		),
		AccountID:     account.ID,
		TransactionID: &tx.ID,
		DedupKey:      fmt.Sprintf("duplicate:%s:%d:%d", account.ID, min(tx.ID, original.ID), max(tx.ID, original.ID)),
	}
	return []domain.Alert{alert}, nil
}

func humanWindow(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "24h"
		}
		return fmt.Sprintf("%dd", days)
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return d.String()
}
