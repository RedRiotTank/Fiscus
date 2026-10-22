package alerts

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const (
	consentWarningDays      = 14
	consentUrgentDays       = 3
	consentExpirationLayout = "2 Jan 2006"
)

// ConsentStore lists the bank connections whose consents should be checked.
type ConsentStore interface {
	ListConnections(ctx context.Context) ([]domain.Connection, error)
}

// ConsentExpirationRule raises warnings before an active PSD2 consent expires.
type ConsentExpirationRule struct {
	store      ConsentStore
	now        func() time.Time
	renewalURL func(domain.Connection) string
}

// NewConsentRule constructs a consent-expiration rule. renewalURL may be nil,
// in which case the connection's stored authorization link is used.
func NewConsentRule(store ConsentStore, now func() time.Time, renewalURL func(domain.Connection) string) (*ConsentExpirationRule, error) {
	if store == nil {
		return nil, errors.New("alerts: consent store is required")
	}
	if now == nil {
		now = time.Now
	}
	if renewalURL == nil {
		renewalURL = func(c domain.Connection) string {
			return strings.TrimSpace(c.Link)
		}
	}
	return &ConsentExpirationRule{store: store, now: now, renewalURL: renewalURL}, nil
}

// Name implements ConsentRule.
func (r *ConsentExpirationRule) Name() string { return RuleConsentExpiration }

// Evaluate implements ConsentRule. It returns at most one alert per connection:
// the 3-day threshold takes precedence over the 14-day threshold.
func (r *ConsentExpirationRule) Evaluate(ctx context.Context) ([]domain.Alert, error) {
	connections, err := r.store.ListConnections(ctx)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	now := r.now().UTC()
	out := make([]domain.Alert, 0)
	for _, connection := range connections {
		if !connection.Linked() || connection.ConsentExpiresAt == nil {
			continue
		}
		expires := connection.ConsentExpiresAt.UTC()
		threshold, ok := consentThreshold(expires, now)
		if !ok {
			continue
		}
		days := int(math.Ceil(expires.Sub(now).Hours() / 24))
		if days < 0 {
			days = 0
		}
		institution := strings.TrimSpace(connection.InstitutionName)
		if institution == "" {
			institution = strings.TrimSpace(connection.InstitutionID)
		}
		if institution == "" {
			institution = connection.RequisitionID
		}
		renewalURL := strings.TrimSpace(r.renewalURL(connection))
		if renewalURL == "" {
			renewalURL = strings.TrimSpace(connection.Link)
		}
		title := fmt.Sprintf("Bank consent expires in %d days", days)
		if days == 0 {
			title = "Bank consent expires today"
		}
		message := fmt.Sprintf(
			"The PSD2 consent for %s expires on %s (%d days remaining).",
			institution, expires.Format(consentExpirationLayout), days,
		)
		if renewalURL != "" {
			message += "\nRenew access: " + renewalURL
		}
		out = append(out, domain.Alert{
			Rule:     RuleConsentExpiration,
			Severity: domain.SeverityWarning,
			Title:    title,
			Message:  message,
			DedupKey: consentDedupKey(connection, expires, threshold),
		})
	}
	return out, nil
}

func consentThreshold(expires, now time.Time) (threshold int, ok bool) {
	switch {
	case !expires.After(now):
		return 0, false
	case !expires.After(now.AddDate(0, 0, consentUrgentDays)):
		return consentUrgentDays, true
	case !expires.After(now.AddDate(0, 0, consentWarningDays)):
		return consentWarningDays, true
	default:
		return 0, false
	}
}

func consentDedupKey(connection domain.Connection, expires time.Time, threshold int) string {
	identity := strings.TrimSpace(connection.RequisitionID)
	if identity == "" {
		identity = fmt.Sprintf("connection-%d", connection.ID)
	}
	return fmt.Sprintf("consent-expiration:%s:%s:%d",
		identity, expires.UTC().Format(time.RFC3339), threshold)
}
