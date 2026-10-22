package analysis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// Reader supplies stored transactions for analysis.
type Reader interface {
	ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error)
}

// Analyzer loads a transaction window and computes a Snapshot.
type Analyzer struct {
	store    Reader
	now      func() time.Time
	lookback time.Duration
}

// NewAnalyzer constructs an Analyzer with a two-year lookback, enough to see
// at least two yearly charges while keeping the dashboard responsive.
func NewAnalyzer(store Reader, now func() time.Time) (*Analyzer, error) {
	if store == nil {
		return nil, errors.New("analysis: transaction reader is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Analyzer{store: store, now: now, lookback: 2 * 365 * 24 * time.Hour}, nil
}

// Snapshot loads the analysis window and returns detected subscriptions and
// fees.
func (a *Analyzer) Snapshot(ctx context.Context) (Snapshot, error) {
	now := a.now().UTC()
	from := now.Add(-a.lookback)
	transactions, err := a.store.ListTransactionsBetween(ctx, from, now.Add(24*time.Hour))
	if err != nil {
		return Snapshot{}, fmt.Errorf("analysis: load transactions: %w", err)
	}
	return Analyze(transactions, now), nil
}
