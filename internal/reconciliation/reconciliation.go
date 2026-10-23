// Package reconciliation implements the balance integrity watchdog. It
// compares the bank's official balance with a balance reconstructed from the
// booked transactions Fiscus has stored.
package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// Store is the persistence surface required by the reconciler.
type Store interface {
	GetReconciliation(ctx context.Context, accountID string) (domain.Reconciliation, error)
	SaveReconciliation(ctx context.Context, rec *domain.Reconciliation) error
	ListBookedTransactionsForAccount(ctx context.Context, accountID string) ([]domain.Transaction, error)
	ListPendingTransactionsForAccount(ctx context.Context, accountID string) ([]domain.Transaction, error)
}

// Reconciler reconstructs account balances and records discrepancy state.
type Reconciler struct {
	store Store
	now   func() time.Time
}

// New constructs a Reconciler.
func New(store Store, now func() time.Time) (*Reconciler, error) {
	if store == nil {
		return nil, errors.New("reconciliation: store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Reconciler{store: store, now: now}, nil
}

// Reconcile checks one account against its booked transaction history.
func (r *Reconciler) Reconcile(ctx context.Context, account domain.Account) (domain.ReconciliationTransition, error) {
	if account.ID == "" {
		return domain.ReconciliationTransition{}, errors.New("reconciliation: account ID is required")
	}
	now := r.now().UTC()
	transactions, err := r.store.ListBookedTransactionsForAccount(ctx, account.ID)
	if err != nil {
		return domain.ReconciliationTransition{}, fmt.Errorf("reconciliation: load transactions: %w", err)
	}
	pending, err := r.store.ListPendingTransactionsForAccount(ctx, account.ID)
	if err != nil {
		return domain.ReconciliationTransition{}, fmt.Errorf("reconciliation: load pending transactions: %w", err)
	}

	previous, err := r.store.GetReconciliation(ctx, account.ID)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		rec := initialReconciliation(account, transactions, pending, now)
		if saveErr := r.store.SaveReconciliation(ctx, &rec); saveErr != nil {
			return domain.ReconciliationTransition{}, saveErr
		}
		return domain.ReconciliationTransition{Reconciliation: rec}, nil
	case err != nil:
		return domain.ReconciliationTransition{}, fmt.Errorf("reconciliation: load state: %w", err)
	}

	expected := previous.OpeningBalanceMinor + sumAfter(transactions, previous.AnchorAt)
	observed := observedBookedBalance(account, pending)
	delta := observed - expected
	rec := previous
	rec.Currency = currencyOrFallback(account.Currency)
	rec.LastCheckedAt = timePtr(now)
	rec.UpdatedAt = now
	transition := domain.ReconciliationTransition{
		PreviousStatus:     previous.Status,
		PreviousDeltaMinor: previous.LastDeltaMinor,
	}

	switch {
	case !previous.BaselineEstablished:
		// The initial setup baseline is not allowed to raise an alert. The
		// first pass with booked history promotes the record; the next pass
		// can compare against a stable baseline.
		rec.BaselineEstablished = len(transactions) > 0
		rec.LastDeltaMinor = 0
		rec.Status = domain.ReconciliationStatusReconciled
		rec.DiscrepancyStart = nil
		rec.DiscrepancyEnd = nil
		transition.Changed = previous.Status != domain.ReconciliationStatusReconciled
	case delta == 0:
		rec.LastDeltaMinor = 0
		rec.Status = domain.ReconciliationStatusReconciled
		rec.DiscrepancyStart = nil
		rec.DiscrepancyEnd = nil
		transition.Changed = previous.Status != domain.ReconciliationStatusReconciled
	default:
		rec.LastDeltaMinor = delta
		rec.Status = domain.ReconciliationStatusDiscrepancy
		if previous.Status != domain.ReconciliationStatusDiscrepancy || previous.LastDeltaMinor != delta {
			start := now
			if previous.LastCheckedAt != nil {
				start = previous.LastCheckedAt.UTC()
			}
			rec.DiscrepancyStart = timePtr(start)
			rec.DiscrepancyEnd = timePtr(now)
			transition.Changed = true
		} else {
			rec.DiscrepancyStart = previous.DiscrepancyStart
			rec.DiscrepancyEnd = previous.DiscrepancyEnd
		}
	}
	rec.UpdatedAt = now
	if err := r.store.SaveReconciliation(ctx, &rec); err != nil {
		return domain.ReconciliationTransition{}, err
	}
	transition.Reconciliation = rec
	return transition, nil
}

func initialReconciliation(account domain.Account, transactions, pending []domain.Transaction, now time.Time) domain.Reconciliation {
	anchor := now
	if len(transactions) > 0 {
		first := transactions[0].BookingDate.UTC()
		anchor = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	}
	sum := sumAfter(transactions, anchor)
	observed := observedBookedBalance(account, pending)
	return domain.Reconciliation{
		AccountID:           account.ID,
		Currency:            currencyOrFallback(account.Currency),
		OpeningBalanceMinor: observed - sum,
		AnchorAt:            anchor,
		LastCheckedAt:       timePtr(now),
		Status:              domain.ReconciliationStatusReconciled,
		BaselineEstablished: len(transactions) > 0,
		UpdatedAt:           now,
	}
}

// observedBookedBalance converts the provider balance into a booked-basis
// figure. Booked balances are used directly. When the ASPSP only exposes an
// available balance, pending debits (holds) and credits are added back so the
// comparison is against the same basis as the stored booked ledger.
func observedBookedBalance(account domain.Account, pending []domain.Transaction) int64 {
	if !isAvailableBalanceType(account.BalanceType) {
		return account.BalanceMinor
	}
	return account.BalanceMinor + pendingHoldMinor(pending)
}

// pendingHoldMinor returns pending debits minus pending credits as positive
// magnitudes. A pending debit reduces an available balance but is not yet in
// the booked ledger, so it must be added back to obtain a booked equivalent.
func pendingHoldMinor(pending []domain.Transaction) int64 {
	var debits, credits int64
	for _, tx := range pending {
		switch {
		case tx.AmountMinor < 0:
			debits += -tx.AmountMinor
		case tx.AmountMinor > 0:
			credits += tx.AmountMinor
		}
	}
	return debits - credits
}

func isAvailableBalanceType(balanceType string) bool {
	switch strings.ToLower(strings.TrimSpace(balanceType)) {
	case "interimavailable", "closingavailable", "expected", "forwardavailable",
		"openingavailable", "authorised", "available":
		return true
	default:
		return false
	}
}

func sumAfter(transactions []domain.Transaction, anchor time.Time) int64 {
	var total int64
	for _, tx := range transactions {
		if tx.BookingDate.UTC().Before(anchor) {
			continue
		}
		total += tx.AmountMinor
	}
	return total
}

func currencyOrFallback(currency string) string {
	if currency == "" {
		return "EUR"
	}
	return currency
}

func timePtr(value time.Time) *time.Time {
	return &value
}
