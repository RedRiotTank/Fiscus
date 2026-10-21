package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const accountColumns = `id, connection_id, iban, name, owner_name, currency,
	balance_minor, balance_type, balance_updated_at, created_at, updated_at`

func scanAccount(row rowScanner) (domain.Account, error) {
	var (
		a         domain.Account
		balanceAt sql.NullString
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&a.ID, &a.ConnectionID, &a.IBAN, &a.Name, &a.OwnerName, &a.Currency,
		&a.BalanceMinor, &a.BalanceType, &balanceAt, &createdAt, &updatedAt,
	); err != nil {
		return domain.Account{}, err
	}
	var err error
	if a.BalanceUpdatedAt, err = decodeNullTime(balanceAt); err != nil {
		return domain.Account{}, err
	}
	if a.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.Account{}, err
	}
	if a.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.Account{}, err
	}
	return a, nil
}

// UpsertAccount inserts account metadata. On conflict it updates descriptive
// fields but never touches the cached balance, which is owned by
// UpdateAccountBalance.
func (s *Store) UpsertAccount(ctx context.Context, a *domain.Account) error {
	if a.ID == "" {
		return errors.New("storage: account ID is required")
	}
	if a.ConnectionID == 0 {
		return errors.New("storage: account connection ID is required")
	}
	now := nowUTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	if a.Currency == "" {
		a.Currency = "EUR"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (
			id, connection_id, iban, name, owner_name, currency,
			balance_minor, balance_type, balance_updated_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			connection_id = excluded.connection_id,
			iban = excluded.iban,
			name = excluded.name,
			owner_name = excluded.owner_name,
			currency = excluded.currency,
			updated_at = excluded.updated_at`,
		a.ID, a.ConnectionID, a.IBAN, a.Name, a.OwnerName, a.Currency,
		a.BalanceMinor, a.BalanceType, encodeNullTime(a.BalanceUpdatedAt),
		encodeTime(a.CreatedAt), encodeTime(a.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("storage: upsert account %q: %w", a.ID, err)
	}
	return nil
}

// UpdateAccountBalance stores the latest balance snapshot for an account and
// returns the refreshed account.
func (s *Store) UpdateAccountBalance(ctx context.Context, accountID string, balanceMinor int64, balanceType string, at time.Time) error {
	if accountID == "" {
		return errors.New("storage: account ID is required")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET balance_minor = ?, balance_type = ?, balance_updated_at = ?, updated_at = ?
		WHERE id = ?`,
		balanceMinor, balanceType, encodeTime(at), encodeTime(nowUTC()), accountID,
	)
	if err != nil {
		return fmt.Errorf("storage: update account balance: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update account balance: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAccount returns an account by GoCardless account ID.
func (s *Store) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)
	a, err := scanAccount(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Account{}, ErrNotFound
		}
		return domain.Account{}, fmt.Errorf("storage: get account %q: %w", id, err)
	}
	return a, nil
}

// ListAccounts returns all accounts ordered by currency and name.
func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY currency ASC, name ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collectAccounts(rows)
}

// ListAccountsByConnection returns accounts belonging to one connection.
func (s *Store) ListAccountsByConnection(ctx context.Context, connectionID int64) ([]domain.Account, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE connection_id = ? ORDER BY name ASC, id ASC`,
		connectionID)
	if err != nil {
		return nil, fmt.Errorf("storage: list accounts by connection: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collectAccounts(rows)
}

func collectAccounts(rows *sql.Rows) ([]domain.Account, error) {
	var out []domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan account: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate accounts: %w", err)
	}
	return out, nil
}
