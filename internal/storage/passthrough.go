package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/transfers"
)

// NeutralizePassThroughs scans booked transactions for exact offsetting
// inflows/outflows and links both legs as budget-neutral pass-throughs.
func (s *Store) NeutralizePassThroughs(ctx context.Context, windowDays int) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE status = ? AND pass_through_suppressed = 0
		ORDER BY booking_date ASC, id ASC`,
		domain.TransactionStatusBooked)
	if err != nil {
		return 0, fmt.Errorf("storage: list pass-through candidates: %w", err)
	}
	transactions, err := scanTransactions(rows)
	if err != nil {
		return 0, err
	}
	matches := transfers.Detect(transactions, windowDays)
	if len(matches) == 0 {
		return 0, nil
	}
	if err := s.applyPassThroughMatches(ctx, matches); err != nil {
		return 0, err
	}
	return len(matches), nil
}

func (s *Store) applyPassThroughMatches(ctx context.Context, matches []transfers.Match) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin pass-through neutralization: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()

	now := encodeTime(nowUTC())
	for _, match := range matches {
		if match.Outflow.ID <= 0 || match.Inflow.ID <= 0 {
			continue
		}
		if _, err := sqlTx.ExecContext(ctx, `
			UPDATE transactions SET
				category = ?, paired_transaction_id = ?, budget_excluded = 1,
				pass_through_suppressed = 0, updated_at = ?
			WHERE id = ?`,
			transfers.CategoryPassThrough, match.Inflow.ID, now, match.Outflow.ID); err != nil {
			return fmt.Errorf("storage: neutralize pass-through outflow: %w", err)
		}
		if _, err := sqlTx.ExecContext(ctx, `
			UPDATE transactions SET
				category = ?, paired_transaction_id = ?, budget_excluded = 1,
				pass_through_suppressed = 0, updated_at = ?
			WHERE id = ?`,
			transfers.CategoryPassThrough, match.Outflow.ID, now, match.Inflow.ID); err != nil {
			return fmt.Errorf("storage: neutralize pass-through inflow: %w", err)
		}
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("storage: commit pass-through neutralization: %w", err)
	}
	return nil
}

// GetTransactionsByIDs loads a batch of transactions for linked-row display.
func (s *Store) GetTransactionsByIDs(ctx context.Context, ids []int64) (map[int64]domain.Transaction, error) {
	out := make(map[int64]domain.Transaction, len(ids))
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for index, id := range unique {
		placeholders[index] = "?"
		args[index] = id
	}
	// #nosec G202 -- placeholders are generated from a fixed "?" template.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("storage: get transactions by IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		tx, scanErr := scanTransaction(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan linked transaction: %w", scanErr)
		}
		out[tx.ID] = tx
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate linked transactions: %w", err)
	}
	return out, nil
}

func scanTransactions(rows *sql.Rows) ([]domain.Transaction, error) {
	defer func() { _ = rows.Close() }()
	out := make([]domain.Transaction, 0)
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan pass-through transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate pass-through transactions: %w", err)
	}
	return out, nil
}
