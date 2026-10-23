package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// ListBusinessTransactionsBetween returns booked transactions belonging to
// business accounts in the [from, to) interval.
func (s *Store) ListBusinessTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error) {
	// #nosec G202 -- transactionColumns is a static developer-defined column list.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixColumns("t", transactionColumns)+` FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		WHERE a.type = ? AND t.status = ? AND t.booking_date >= ? AND t.booking_date < ?
		ORDER BY t.booking_date ASC, t.id ASC`,
		domain.AccountTypeBusiness, domain.TransactionStatusBooked,
		encodeTime(from), encodeTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list business transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan business transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate business transactions: %w", err)
	}
	return out, nil
}
