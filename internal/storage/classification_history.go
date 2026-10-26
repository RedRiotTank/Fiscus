package storage

import (
	"context"
	"fmt"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// ListCategorizedTransactionsForTraining returns recently confirmed,
// non-uncategorized transactions used to seed the in-memory Bayesian
// categorizer. The query is bounded so startup remains cheap even for large
// ledgers.
func (s *Store) ListCategorizedTransactionsForTraining(ctx context.Context, limit int) ([]domain.Transaction, error) {
	if limit <= 0 {
		limit = 5000
	}
	if limit > 20000 {
		limit = 20000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE status = ?
		  AND TRIM(category) <> ''
		  AND LOWER(TRIM(category)) <> 'uncategorized'
		ORDER BY booking_date DESC, id DESC
		LIMIT ?`,
		domain.TransactionStatusBooked, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: list categorization history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]domain.Transaction, 0, min(limit, 256))
	for rows.Next() {
		tx, scanErr := scanTransaction(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan categorization history: %w", scanErr)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate categorization history: %w", err)
	}
	return out, nil
}
