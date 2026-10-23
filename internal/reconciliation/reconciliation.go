// Package reconciliation implements the balance integrity watchdog. It
// compares the bank's official balance with a balance reconstructed from the
// booked transactions Fiscus has stored.
package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

// Store is the persistence surface required by the reconciler.
type Store interface {
	GetReconciliation(ctx context.Context, accountID string) (domain.Reconciliation, error)
	SaveReconciliation(ctx context.Context, rec *domain.Reconciliation) error
	ListBookedTransactionsForAccount(ctx context.Context, accountID string) ([]domain.Transaction, error)
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

	previous, err := r.store.GetReconciliation(ctx, account.ID)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		rec := initialReconciliation(account, transactions, now)
		if saveErr := r.store.SaveReconciliation(ctx, &rec); saveErr != nil {
			return domain.ReconciliationTransition{}, saveErr
		}
		return domain.ReconciliationTransition{Reconciliation: rec}, nil
	case err != nil:
		return domain.ReconciliationTransition{}, fmt.Errorf("reconciliation: load state: %w", err)
	}

	expected := previous.OpeningBalanceMinor + sumAfter(transactions, previous.AnchorAt)
	delta := account.BalanceMinor - expected
	rec := previous
	rec.Currency = currencyOrFallback(account.Currency)
	rec.LastDeltaMinor = delta
	rec.LastCheckedAt = timePtr(now)
	rec.UpdatedAt = now
	transition := domain.ReconciliationTransition{
		PreviousStatus:     previous.Status,
		PreviousDeltaMinor: previous.LastDeltaMinor,
	}

	if delta == 0 {
		rec.Status = domain.ReconciliationStatusReconciled
		rec.DiscrepancyStart = nil
		rec.DiscrepancyEnd = nil
		transition.Changed = previous.Status != domain.ReconciliationStatusReconciled
	} else {
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

func initialReconciliation(account domain.Account, transactions []domain.Transaction, now time.Time) domain.Reconciliation {
	anchor := now
	if len(transactions) > 0 {
		first := transactions[0].BookingDate.UTC()
		anchor = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	}
	sum := sumAfter(transactions, anchor)
	return domain.Reconciliation{
		AccountID:           account.ID,
		Currency:            currencyOrFallback(account.Currency),
		OpeningBalanceMinor: account.BalanceMinor - sum,
		AnchorAt:            anchor,
		LastCheckedAt:       timePtr(now),
		Status:              domain.ReconciliationStatusReconciled,
		UpdatedAt:           now,
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
