package monitoring

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/analysis"
	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/money"
)

// RuleGhostSubscription is the alert rule name for dormant recurring charges.
const RuleGhostSubscription = "subscription_ghost"

// SnapshotProvider supplies detected subscriptions.
type SnapshotProvider interface {
	Snapshot(ctx context.Context) (analysis.Snapshot, error)
}

// GhostSubscriptionRule notifies when a recurring charge has not appeared
// within the stale window for its billing frequency.
type GhostSubscriptionRule struct {
	provider SnapshotProvider
	now      func() time.Time
}

// NewGhostSubscriptionRule constructs a dormant-subscription watcher.
func NewGhostSubscriptionRule(provider SnapshotProvider, now func() time.Time) (*GhostSubscriptionRule, error) {
	if provider == nil {
		return nil, errors.New("monitoring: snapshot provider is required")
	}
	if now == nil {
		now = time.Now
	}
	return &GhostSubscriptionRule{provider: provider, now: now}, nil
}

// Name implements alerts.AnalysisRule.
func (r *GhostSubscriptionRule) Name() string { return RuleGhostSubscription }

// Evaluate implements alerts.AnalysisRule.
func (r *GhostSubscriptionRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	snapshot, err := r.provider.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	out := make([]domain.Alert, 0)
	for _, subscription := range snapshot.Subscriptions {
		if subscription.Active {
			continue
		}
		days := int(now.Sub(subscription.LastCharge).Hours() / 24)
		out = append(out, domain.Alert{
			Rule:      RuleGhostSubscription,
			Severity:  domain.SeverityWarning,
			Title:     fmt.Sprintf("Potential ghost subscription: %s", subscription.Counterparty),
			AccountID: subscription.AccountID,
			Message: fmt.Sprintf(
				"%s (%s, %s) has not charged since %s (%d days). It is marked Inactive / Potential Ghost; consider cancelling it if unused.",
				subscription.Counterparty,
				subscription.Frequency,
				money.FormatGrouped(subscription.AmountMinor, subscription.Currency)+" "+subscription.Currency,
				subscription.LastCharge.Format("2 Jan 2006"),
				days,
			),
			DedupKey: fmt.Sprintf("subscription-ghost:%s:%s:%s",
				subscription.AccountID,
				strings.ToLower(strings.TrimSpace(subscription.Counterparty)),
				subscription.LastCharge.UTC().Format("2006-01-02"),
			),
		})
	}
	return out, nil
}
