package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const transactionColumns = `id, account_id, external_id, amount_minor, currency, booking_date,
	value_date, counterparty, description, category, status, created_at, updated_at`

// TransactionFilter controls transaction listing.
type TransactionFilter struct {
	AccountID string
	Limit     int
	Offset    int
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func scanTransaction(row rowScanner) (domain.Transaction, error) {
	var (
		t         domain.Transaction
		booking   string
		valueDate sql.NullString
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&t.ID, &t.AccountID, &t.ExternalID, &t.AmountMinor, &t.Currency, &booking,
		&valueDate, &t.Counterparty, &t.Description, &t.Category, &t.Status, &createdAt, &updatedAt,
	); err != nil {
		return domain.Transaction{}, err
	}
	var err error
	if t.BookingDate, err = decodeTime(booking); err != nil {
		return domain.Transaction{}, err
	}
	if t.ValueDate, err = decodeNullTime(valueDate); err != nil {
		return domain.Transaction{}, err
	}
	if t.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.Transaction{}, err
	}
	if t.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.Transaction{}, err
	}
	return t, nil
}

// UpsertTransaction stores a transaction idempotently, keyed by
// (account_id, external_id). It reports whether the row was newly inserted and
// whether a previously pending transaction was promoted to booked. Rules should
// be evaluated for both newly inserted and promoted transactions.
func (s *Store) UpsertTransaction(ctx context.Context, t *domain.Transaction) (inserted bool, promoted bool, err error) {
	if t.AccountID == "" {
		return false, false, errors.New("storage: transaction account ID is required")
	}
	if t.ExternalID == "" {
		return false, false, errors.New("storage: transaction external ID is required")
	}
	if t.Currency == "" {
		return false, false, errors.New("storage: transaction currency is required")
	}
	if t.Status == "" {
		t.Status = domain.TransactionStatusBooked
	}
	if strings.TrimSpace(t.Category) == "" {
		t.Category = "Uncategorized"
	}
	if t.BookingDate.IsZero() {
		t.BookingDate = nowUTC()
	}
	t.BookingDate = t.BookingDate.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("storage: begin transaction upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingID        int64
		existingStatus    string
		existingCreatedAt string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, status, created_at FROM transactions
		WHERE account_id = ? AND external_id = ?`,
		t.AccountID, t.ExternalID,
	).Scan(&existingID, &existingStatus, &existingCreatedAt)

	now := nowUTC()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		t.UpdatedAt = now
		res, execErr := tx.ExecContext(ctx, `
			INSERT INTO transactions (
				account_id, external_id, amount_minor, currency, booking_date, value_date,
				counterparty, description, category, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.AccountID, t.ExternalID, t.AmountMinor, t.Currency, encodeTime(t.BookingDate),
			encodeNullTime(t.ValueDate), t.Counterparty, t.Description, t.Category, t.Status,
			encodeTime(t.CreatedAt), encodeTime(t.UpdatedAt),
		)
		if execErr != nil {
			return false, false, fmt.Errorf("storage: insert transaction: %w", execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return false, false, fmt.Errorf("storage: transaction ID: %w", idErr)
		}
		t.ID = id
		inserted = true
	case err != nil:
		return false, false, fmt.Errorf("storage: inspect transaction: %w", err)
	default:
		createdAt, parseErr := decodeTime(existingCreatedAt)
		if parseErr != nil {
			return false, false, parseErr
		}
		t.ID = existingID
		t.CreatedAt = createdAt
		t.UpdatedAt = now
		_, execErr := tx.ExecContext(ctx, `
			UPDATE transactions SET
				amount_minor = ?, currency = ?, booking_date = ?, value_date = ?,
				counterparty = ?, description = ?, category = ?, status = ?, updated_at = ?
			WHERE id = ?`,
			t.AmountMinor, t.Currency, encodeTime(t.BookingDate), encodeNullTime(t.ValueDate),
			t.Counterparty, t.Description, t.Category, t.Status, encodeTime(t.UpdatedAt), existingID,
		)
		if execErr != nil {
			return false, false, fmt.Errorf("storage: update transaction: %w", execErr)
		}
		promoted = existingStatus != domain.TransactionStatusBooked && t.Status == domain.TransactionStatusBooked
	}

	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("storage: commit transaction upsert: %w", err)
	}
	return inserted, promoted, nil
}

// ListTransactions returns transactions matching the filter, newest first.
func (s *Store) ListTransactions(ctx context.Context, filter TransactionFilter) ([]domain.Transaction, error) {
	var (
		where []string
		args  []any
	)
	if filter.AccountID != "" {
		where = append(where, "account_id = ?")
		args = append(args, filter.AccountID)
	}
	query := `SELECT ` + transactionColumns + ` FROM transactions`
	if len(where) > 0 {
		// #nosec G202 -- where contains only static, developer-defined predicates.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY booking_date DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, normalizeLimit(filter.Limit), max(filter.Offset, 0))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan transaction: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate transactions: %w", err)
	}
	return out, nil
}

// RecentTransactions returns the newest transactions across all accounts.
func (s *Store) RecentTransactions(ctx context.Context, limit int) ([]domain.Transaction, error) {
	return s.ListTransactions(ctx, TransactionFilter{Limit: limit})
}

// CategorySpending is the absolute booked spending for one category and
// currency within a period.
type CategorySpending struct {
	Category string
	Currency string
	Minor    int64
}

// SpendingByCategory aggregates booked debits in [from, to) by category and
// currency. Returned amounts are positive.
func (s *Store) SpendingByCategory(ctx context.Context, from, to time.Time) ([]CategorySpending, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(category), ''), 'Uncategorized'), currency,
			COALESCE(SUM(-amount_minor), 0)
		FROM transactions
		WHERE amount_minor < 0 AND status = ? AND booking_date >= ? AND booking_date < ?
		GROUP BY 1, 2
		ORDER BY 1 ASC, 2 ASC`,
		domain.TransactionStatusBooked, encodeTime(from), encodeTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: spending by category: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CategorySpending
	for rows.Next() {
		var item CategorySpending
		if err := rows.Scan(&item.Category, &item.Currency, &item.Minor); err != nil {
			return nil, fmt.Errorf("storage: scan category spending: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate category spending: %w", err)
	}
	return out, nil
}

// ListTransactionsBetween returns booked and pending transactions with booking
// dates in [from, to), oldest first. It is used by offline analysis and exports.
func (s *Store) ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE booking_date >= ? AND booking_date < ?
		ORDER BY booking_date ASC, id ASC`,
		encodeTime(from), encodeTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list transactions between: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan transaction: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate transactions: %w", err)
	}
	return out, nil
}

// CountTransactions returns the total number of stored transactions.
func (s *Store) CountTransactions(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count transactions: %w", err)
	}
	return n, nil
}

// FindDuplicateCandidates looks for booked transactions with the same
// case-insensitive counterparty and exact amount within [from, to], excluding
// the transaction identified by excludeID.
func (s *Store) FindDuplicateCandidates(
	ctx context.Context,
	accountID, counterparty string,
	amountMinor int64,
	from, to time.Time,
	excludeID int64,
) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE account_id = ?
			AND LOWER(TRIM(counterparty)) = LOWER(TRIM(?))
			AND amount_minor = ?
			AND status = ?
			AND booking_date >= ? AND booking_date <= ?
			AND id <> ?
		ORDER BY booking_date DESC, id DESC
		LIMIT 10`,
		accountID, counterparty, amountMinor, domain.TransactionStatusBooked,
		encodeTime(from), encodeTime(to), excludeID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: find duplicate candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan duplicate candidate: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate duplicate candidates: %w", err)
	}
	return out, nil
}

// DebitStats returns the number of booked debits and their absolute total in
// [from, to], excluding the transaction identified by excludeID. Debits are
// stored as negative amounts.
func (s *Store) DebitStats(ctx context.Context, accountID string, from, to time.Time, excludeID int64) (totalMinor int64, count int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(-amount_minor), 0), COUNT(*)
		FROM transactions
		WHERE account_id = ?
			AND amount_minor < 0
			AND status = ?
			AND booking_date >= ? AND booking_date <= ?
			AND id <> ?`,
		accountID, domain.TransactionStatusBooked,
		encodeTime(from), encodeTime(to), excludeID,
	).Scan(&totalMinor, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("storage: debit stats: %w", err)
	}
	return totalMinor, count, nil
}
