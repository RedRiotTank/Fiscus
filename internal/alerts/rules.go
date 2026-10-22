// Package alerts evaluates treasury rules and dispatches notifications.
package alerts

import (
	"context"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Rule names used in alerts and metrics.
const (
	RuleDuplicate         = "duplicate"
	RuleAnomaly           = "anomaly"
	RuleLiquidity         = "liquidity"
	RuleConsentExpiration = "consent_expiration"
)

// TransactionRule inspects a single transaction, usually against history.
type TransactionRule interface {
	Name() string
	Evaluate(ctx context.Context, account domain.Account, tx domain.Transaction) ([]domain.Alert, error)
}

// BalanceRule inspects an account balance snapshot.
type BalanceRule interface {
	Name() string
	Evaluate(ctx context.Context, account domain.Account) ([]domain.Alert, error)
}

// ConsentRule inspects bank connection consent validity.
type ConsentRule interface {
	Name() string
	Evaluate(ctx context.Context) ([]domain.Alert, error)
}

// AnalysisRule inspects a historical transaction window.
type AnalysisRule interface {
	Name() string
	Evaluate(ctx context.Context) ([]domain.Alert, error)
}

// DuplicateFinder finds prior transactions with the same counterparty and
// amount inside a time window.
type DuplicateFinder interface {
	FindDuplicateCandidates(ctx context.Context, accountID, counterparty string, amountMinor int64, from, to time.Time, excludeID int64) ([]domain.Transaction, error)
}

// DebitStatistics summarizes historical debits for anomaly detection.
type DebitStatistics interface {
	DebitStats(ctx context.Context, accountID string, from, to time.Time, excludeID int64) (totalMinor int64, count int, err error)
}

func accountLabel(account domain.Account) string {
	switch {
	case account.Name != "":
		return account.Name
	case account.IBAN != "":
		return account.IBAN
	default:
		return account.ID
	}
}
