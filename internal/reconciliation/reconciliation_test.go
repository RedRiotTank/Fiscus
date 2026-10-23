package reconciliation

import (
	"context"
	"testing"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/internal/storage"
)

type fakeStore struct {
	rec          domain.Reconciliation
	hasRec       bool
	transactions []domain.Transaction
}

func (f *fakeStore) GetReconciliation(_ context.Context, _ string) (domain.Reconciliation, error) {
	if !f.hasRec {
		return domain.Reconciliation{}, storage.ErrNotFound
	}
	return f.rec, nil
}

func (f *fakeStore) SaveReconciliation(_ context.Context, rec *domain.Reconciliation) error {
	f.rec = *rec
	f.hasRec = true
	return nil
}

func (f *fakeStore) ListBookedTransactionsForAccount(_ context.Context, _ string) ([]domain.Transaction, error) {
	out := make([]domain.Transaction, 0, len(f.transactions))
	for _, tx := range f.transactions {
		if tx.Status == domain.TransactionStatusBooked {
			out = append(out, tx)
		}
	}
	return out, nil
}

func TestReconcileDetectsDiscrepancyAndResolution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{transactions: []domain.Transaction{
		{AmountMinor: 5000, BookingDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), Status: domain.TransactionStatusBooked},
		{AmountMinor: -1000, BookingDate: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC), Status: domain.TransactionStatusBooked},
		{AmountMinor: -999, BookingDate: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC), Status: domain.TransactionStatusPending},
	}}
	reconciler, err := New(store, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	account := domain.Account{ID: "acc-1", Currency: "EUR", BalanceMinor: 10000}

	first, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}
	if first.Reconciliation.OpeningBalanceMinor != 6000 {
		t.Fatalf("opening balance = %d, want 6000", first.Reconciliation.OpeningBalanceMinor)
	}
	if !first.Reconciliation.Balanced() {
		t.Fatalf("initial status = %q", first.Reconciliation.Status)
	}

	// The bank reports an extra 1000 that is missing from the stored ledger.
	account.BalanceMinor = 11000
	now = now.Add(time.Hour)
	transition, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("discrepancy Reconcile: %v", err)
	}
	if !transition.Changed {
		t.Fatal("expected a discrepancy transition")
	}
	if transition.Reconciliation.Status != domain.ReconciliationStatusDiscrepancy || transition.Reconciliation.LastDeltaMinor != 1000 {
		t.Fatalf("reconciliation = %+v", transition.Reconciliation)
	}
	if transition.Reconciliation.DiscrepancyStart == nil || !transition.Reconciliation.DiscrepancyStart.Equal(time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("discrepancy start = %v", transition.Reconciliation.DiscrepancyStart)
	}

	// The same discrepancy must not be re-alerted.
	now = now.Add(time.Hour)
	again, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("repeat Reconcile: %v", err)
	}
	if again.Changed {
		t.Fatal("unchanged discrepancy should not be flagged as changed")
	}

	// A different delta is a new discrepancy state.
	account.BalanceMinor = 12000
	now = now.Add(time.Hour)
	changed, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("delta change Reconcile: %v", err)
	}
	if !changed.Changed || changed.Reconciliation.LastDeltaMinor != 2000 {
		t.Fatalf("delta change = %+v", changed)
	}

	// Returning to the reconstructed balance resolves the discrepancy.
	account.BalanceMinor = 10000
	now = now.Add(time.Hour)
	resolved, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("resolution Reconcile: %v", err)
	}
	if !resolved.Changed || !resolved.Reconciliation.Balanced() {
		t.Fatalf("resolution = %+v", resolved)
	}
	if resolved.Reconciliation.DiscrepancyStart != nil || resolved.Reconciliation.DiscrepancyEnd != nil {
		t.Fatal("resolution should clear discrepancy bracket")
	}
}

func TestReconcileIgnoresPendingTransactions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{transactions: []domain.Transaction{
		{AmountMinor: 1000, BookingDate: now.AddDate(0, 0, -1), Status: domain.TransactionStatusBooked},
		{AmountMinor: -500, BookingDate: now, Status: domain.TransactionStatusPending},
	}}
	reconciler, err := New(store, func() time.Time { return now })
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	account := domain.Account{ID: "acc-1", Currency: "EUR", BalanceMinor: 2000}
	first, err := reconciler.Reconcile(ctx, account)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !first.Reconciliation.Balanced() || first.Reconciliation.OpeningBalanceMinor != 1000 {
		t.Fatalf("pending transaction affected reconciliation: %+v", first.Reconciliation)
	}
}
