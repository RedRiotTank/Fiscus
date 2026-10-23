package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const reconciliationColumns = `account_id, currency, opening_balance_minor, anchor_at,
	last_checked_at, last_delta_minor, status, discrepancy_start, discrepancy_end,
	baseline_established, updated_at`

func scanReconciliation(row rowScanner) (domain.Reconciliation, error) {
	var (
		rec         domain.Reconciliation
		anchor      string
		lastChecked sql.NullString
		discStart   sql.NullString
		discEnd     sql.NullString
		baseline    int
		updatedAt   string
	)
	if err := row.Scan(
		&rec.AccountID, &rec.Currency, &rec.OpeningBalanceMinor, &anchor,
		&lastChecked, &rec.LastDeltaMinor, &rec.Status, &discStart, &discEnd,
		&baseline, &updatedAt,
	); err != nil {
		return domain.Reconciliation{}, err
	}
	rec.BaselineEstablished = baseline != 0
	var err error
	if rec.AnchorAt, err = decodeTime(anchor); err != nil {
		return domain.Reconciliation{}, err
	}
	if rec.LastCheckedAt, err = decodeNullTime(lastChecked); err != nil {
		return domain.Reconciliation{}, err
	}
	if rec.DiscrepancyStart, err = decodeNullTime(discStart); err != nil {
		return domain.Reconciliation{}, err
	}
	if rec.DiscrepancyEnd, err = decodeNullTime(discEnd); err != nil {
		return domain.Reconciliation{}, err
	}
	if rec.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.Reconciliation{}, err
	}
	return rec, nil
}

// GetReconciliation returns the watchdog state for an account.
func (s *Store) GetReconciliation(ctx context.Context, accountID string) (domain.Reconciliation, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+reconciliationColumns+` FROM account_reconciliation WHERE account_id = ?`, accountID)
	rec, err := scanReconciliation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Reconciliation{}, ErrNotFound
		}
		return domain.Reconciliation{}, fmt.Errorf("storage: get reconciliation: %w", err)
	}
	return rec, nil
}

// SaveReconciliation inserts or replaces the watchdog state for an account.
func (s *Store) SaveReconciliation(ctx context.Context, rec *domain.Reconciliation) error {
	if rec.AccountID == "" {
		return errors.New("storage: reconciliation account ID is required")
	}
	if rec.Status == "" {
		rec.Status = domain.ReconciliationStatusReconciled
	}
	if rec.Currency == "" {
		rec.Currency = "EUR"
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = nowUTC()
	}
	baseline := 0
	if rec.BaselineEstablished {
		baseline = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO account_reconciliation (
			account_id, currency, opening_balance_minor, anchor_at, last_checked_at,
			last_delta_minor, status, discrepancy_start, discrepancy_end,
			baseline_established, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id) DO UPDATE SET
			currency = excluded.currency,
			opening_balance_minor = excluded.opening_balance_minor,
			anchor_at = excluded.anchor_at,
			last_checked_at = excluded.last_checked_at,
			last_delta_minor = excluded.last_delta_minor,
			status = excluded.status,
			discrepancy_start = excluded.discrepancy_start,
			discrepancy_end = excluded.discrepancy_end,
			baseline_established = excluded.baseline_established,
			updated_at = excluded.updated_at`,
		rec.AccountID, rec.Currency, rec.OpeningBalanceMinor, encodeTime(rec.AnchorAt),
		encodeNullTime(rec.LastCheckedAt), rec.LastDeltaMinor, rec.Status,
		encodeNullTime(rec.DiscrepancyStart), encodeNullTime(rec.DiscrepancyEnd),
		baseline, encodeTime(rec.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("storage: save reconciliation: %w", err)
	}
	return nil
}

// ListPendingTransactionsForAccount returns pending transactions for an
// account, oldest first. Pending holds explain why an available balance can
// differ from a booked ledger.
func (s *Store) ListPendingTransactionsForAccount(ctx context.Context, accountID string) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE account_id = ? AND status = ?
		ORDER BY booking_date ASC, id ASC`,
		accountID, domain.TransactionStatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list pending transactions for reconciliation: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan pending reconciliation transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate pending reconciliation transactions: %w", err)
	}
	return out, nil
}

// ListBookedTransactionsForAccount returns booked transactions for an account,
// oldest first, for balance reconstruction.
func (s *Store) ListBookedTransactionsForAccount(ctx context.Context, accountID string) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE account_id = ? AND status = ?
		ORDER BY booking_date ASC, id ASC`,
		accountID, domain.TransactionStatusBooked,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list booked transactions for reconciliation: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan reconciliation transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate reconciliation transactions: %w", err)
	}
	return out, nil
}
