package analysis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// RuleSubscriptionPriceIncrease is the alert rule name for subscription price
// changes.
const RuleSubscriptionPriceIncrease = "subscription_price_increase"

// SnapshotProvider provides an analysis snapshot.
type SnapshotProvider interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

// SubscriptionRule raises an alert when a recurring subscription's latest
// charge is higher than its previous charge.
type SubscriptionRule struct {
	provider SnapshotProvider
}

// NewSubscriptionRule constructs a price-increase rule.
func NewSubscriptionRule(provider SnapshotProvider) (*SubscriptionRule, error) {
	if provider == nil {
		return nil, errors.New("analysis: snapshot provider is required")
	}
	return &SubscriptionRule{provider: provider}, nil
}

// Name implements alerts.AnalysisRule.
func (r *SubscriptionRule) Name() string { return RuleSubscriptionPriceIncrease }

// Evaluate implements alerts.AnalysisRule.
func (r *SubscriptionRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	snapshot, err := r.provider.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Alert, 0)
	for _, subscription := range snapshot.Subscriptions {
		if !subscription.Active || subscription.PriceIncreaseMinor <= 0 {
			continue
		}
		percent := IncreasePercent(subscription.AmountMinor, subscription.PreviousAmountMinor)
		changedAt := subscription.PriceChangedAt
		if changedAt.IsZero() {
			changedAt = subscription.LastCharge
		}
		title := fmt.Sprintf("Subscription price increased: %s", subscription.Counterparty)
		message := fmt.Sprintf(
			"%s increased from %s %s to %s %s (+%d%%) on %s.",
			subscription.Counterparty,
			money.FormatGrouped(subscription.PreviousAmountMinor, subscription.Currency), subscription.Currency,
			money.FormatGrouped(subscription.AmountMinor, subscription.Currency), subscription.Currency,
			percent,
			changedAt.Format("2 Jan 2006"),
		)
		out = append(out, domain.Alert{
			Rule:      RuleSubscriptionPriceIncrease,
			Severity:  domain.SeverityWarning,
			Title:     title,
			Message:   message,
			AccountID: subscription.AccountID,
			DedupKey: fmt.Sprintf("subscription-price:%s:%s:%s:%d:%d",
				subscription.AccountID,
				strings.ToLower(strings.TrimSpace(subscription.Counterparty)),
				changedAt.UTC().Format("2006-01-02"),
				subscription.AmountMinor,
				subscription.PreviousAmountMinor,
			),
		})
	}
	return out, nil
}
