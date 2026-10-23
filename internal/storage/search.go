package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// TransactionSearch is a parsed FTS5 query with structured filters.
type TransactionSearch struct {
	Match       string
	AmountOp    string
	AmountMinor int64
	Category    string
	Status      string
	AccountType string
	HasReceipt  *bool
}

// SearchTransactions searches the FTS5 index and applies structured filters.
func (s *Store) SearchTransactions(ctx context.Context, search TransactionSearch, limit int) ([]domain.Transaction, error) {
	return s.SearchTransactionsPage(ctx, search, limit, 0)
}

// SearchTransactionsPage searches the FTS5 index and returns one page.
func (s *Store) SearchTransactionsPage(ctx context.Context, search TransactionSearch, limit, offset int) ([]domain.Transaction, error) {
	// #nosec G202 -- transactionColumns is a static developer-defined column list.
	query := `SELECT ` + prefixColumns("t", transactionColumns) + `
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id`
	if search.Match != "" {
		query += ` JOIN transactions_fts ON transactions_fts.rowid = t.id`
	}
	var (
		where []string
		args  []any
	)
	if search.Match != "" {
		where = append(where, "transactions_fts MATCH ?")
		args = append(args, search.Match)
	}
	switch search.AmountOp {
	case "":
	case ">", ">=", "<", "<=", "=":
		where = append(where, "ABS(t.amount_minor) "+search.AmountOp+" ?")
		args = append(args, search.AmountMinor)
	default:
		return nil, fmt.Errorf("storage: invalid amount operator %q", search.AmountOp)
	}
	if search.Category != "" {
		where = append(where, "LOWER(t.category) = LOWER(?)")
		args = append(args, search.Category)
	}
	if search.Status != "" {
		where = append(where, "t.status = ?")
		args = append(args, search.Status)
	}
	if search.AccountType != "" {
		if search.Match != "" {
			where = append(where, "transactions_fts.account_type = ?")
		} else {
			where = append(where, "a.type = ?")
		}
		args = append(args, search.AccountType)
	}
	if search.HasReceipt != nil {
		if *search.HasReceipt {
			where = append(where, "EXISTS (SELECT 1 FROM transaction_attachments att WHERE att.transaction_id = t.id)")
		} else {
			where = append(where, "NOT EXISTS (SELECT 1 FROM transaction_attachments att WHERE att.transaction_id = t.id)")
		}
	}
	if len(where) > 0 {
		// #nosec G202 -- where contains only static predicates; values are bound.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY t.booking_date DESC, t.id DESC LIMIT ? OFFSET ?"
	args = append(args, normalizeLimit(limit), max(offset, 0))

	// #nosec G202 -- query is composed from static fragments and bound values.
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: search transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan search result: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate search results: %w", err)
	}
	return out, nil
}
