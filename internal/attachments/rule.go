package attachments

import (
	"context"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// MissingReceiptStore finds business debits without an attachment.
type MissingReceiptStore interface {
	UnattachedBusinessTransactions(ctx context.Context, before time.Time, thresholdMinor int64) ([]domain.Transaction, error)
}

// MissingReceiptRule warns about deductible business expenses that have no
// receipt after a grace period.
type MissingReceiptRule struct {
	store          MissingReceiptStore
	thresholdMinor int64
	graceDays      int
	now            func() time.Time
}

// NewMissingReceiptRule constructs the receipt-urgency rule.
func NewMissingReceiptRule(store MissingReceiptStore, thresholdMinor int64, graceDays int, now func() time.Time) *MissingReceiptRule {
	if now == nil {
		now = time.Now
	}
	if graceDays < 0 {
		graceDays = 0
	}
	return &MissingReceiptRule{store: store, thresholdMinor: thresholdMinor, graceDays: graceDays, now: now}
}

// Name implements alerts.AnalysisRule.
func (r *MissingReceiptRule) Name() string { return "missing_receipt" }

// Evaluate implements alerts.AnalysisRule.
func (r *MissingReceiptRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	if r.store == nil {
		return nil, nil
	}
	before := r.now().UTC().AddDate(0, 0, -r.graceDays)
	transactions, err := r.store.UnattachedBusinessTransactions(ctx, before, r.thresholdMinor)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Alert, 0, len(transactions))
	for _, tx := range transactions {
		currency := tx.Currency
		if currency == "" {
			currency = "EUR"
		}
		name := tx.Counterparty
		if name == "" {
			name = tx.Description
		}
		if name == "" {
			name = tx.AccountID
		}
		out = append(out, domain.Alert{
			Rule:      r.Name(),
			Severity:  domain.SeverityWarning,
			Title:     "Missing receipt",
			Message:   fmt.Sprintf("%s: %s %s on %s has no attached receipt.", name, money.FormatGrouped(-tx.AmountMinor, currency), currency, tx.BookingDate.Format("02 Jan 2006")),
			AccountID: tx.AccountID,
			DedupKey:  fmt.Sprintf("missing_receipt:%d", tx.ID),
		})
	}
	return out, nil
}
