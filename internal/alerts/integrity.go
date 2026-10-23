package alerts

import (
	"context"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// IntegrityRule turns a balance discrepancy transition into an actionable
// alert. Resolutions are recorded silently by the reconciler.
type IntegrityRule struct{}

// NewIntegrityRule constructs a balance-integrity rule.
func NewIntegrityRule() *IntegrityRule {
	return &IntegrityRule{}
}

// Name implements ReconciliationRule.
func (r *IntegrityRule) Name() string {
	return RuleReconciliation
}

// Evaluate implements ReconciliationRule.
func (r *IntegrityRule) Evaluate(_ context.Context, transition domain.ReconciliationTransition) ([]domain.Alert, error) {
	rec := transition.Reconciliation
	if rec.Status != domain.ReconciliationStatusDiscrepancy || rec.LastDeltaMinor == 0 {
		return nil, nil
	}
	currency := rec.Currency
	if currency == "" {
		currency = "EUR"
	}
	start := rec.UpdatedAt
	if rec.DiscrepancyStart != nil {
		start = *rec.DiscrepancyStart
	}
	end := rec.UpdatedAt
	if rec.DiscrepancyEnd != nil {
		end = *rec.DiscrepancyEnd
	}
	message := fmt.Sprintf(
		"Account %s has an official balance that differs from the reconstructed ledger by %s %s. The discrepancy appeared between %s and %s.",
		rec.AccountID,
		money.FormatGrouped(rec.LastDeltaMinor, currency),
		currency,
		start.UTC().Format("02 Jan 2006 15:04 UTC"),
		end.UTC().Format("02 Jan 2006 15:04 UTC"),
	)
	return []domain.Alert{{
		Rule:      RuleReconciliation,
		Severity:  domain.SeverityCritical,
		Title:     "Balance discrepancy detected",
		Message:   message,
		AccountID: rec.AccountID,
		DedupKey:  fmt.Sprintf("reconciliation:%s:%d:%s", rec.AccountID, rec.LastDeltaMinor, start.UTC().Format(time.RFC3339)),
	}}, nil
}
