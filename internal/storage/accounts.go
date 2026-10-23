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

const accountColumns = `id, connection_id, iban, name, owner_name, currency,
	type, type_manual, balance_minor, balance_type, balance_updated_at, rate_limited_until,
	last_rate_limit_reason, created_at, updated_at`

func scanAccount(row rowScanner) (domain.Account, error) {
	var (
		a             domain.Account
		typeManual    int
		balanceAt     sql.NullString
		rateLimited   sql.NullString
		rateLimitNote sql.NullString
		createdAt     string
		updatedAt     string
	)
	if err := row.Scan(
		&a.ID, &a.ConnectionID, &a.IBAN, &a.Name, &a.OwnerName, &a.Currency,
		&a.Type, &typeManual, &a.BalanceMinor, &a.BalanceType, &balanceAt, &rateLimited, &rateLimitNote,
		&createdAt, &updatedAt,
	); err != nil {
		return domain.Account{}, err
	}
	a.TypeManual = typeManual != 0
	var err error
	if a.BalanceUpdatedAt, err = decodeNullTime(balanceAt); err != nil {
		return domain.Account{}, err
	}
	if a.RateLimitedUntil, err = decodeNullTime(rateLimited); err != nil {
		return domain.Account{}, err
	}
	a.LastRateLimitReason = rateLimitNote.String
	if a.Type == "" {
		a.Type = domain.AccountTypePersonal
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

	typeHint := nullableString(a.Type)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (
			id, connection_id, iban, name, owner_name, currency,
			type, balance_minor, balance_type, balance_updated_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, COALESCE(?, 'personal'), ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			connection_id = excluded.connection_id,
			iban = excluded.iban,
			name = excluded.name,
			owner_name = excluded.owner_name,
			currency = excluded.currency,
			type = CASE
				WHEN accounts.type_manual = 1 THEN accounts.type
				WHEN ? IS NOT NULL THEN ?
				ELSE accounts.type
			END,
			updated_at = excluded.updated_at`,
		a.ID, a.ConnectionID, a.IBAN, a.Name, a.OwnerName, a.Currency,
		typeHint, a.BalanceMinor, a.BalanceType, encodeNullTime(a.BalanceUpdatedAt),
		encodeTime(a.CreatedAt), encodeTime(a.UpdatedAt),
		typeHint, typeHint,
	)
	if err != nil {
		return fmt.Errorf("storage: upsert account %q: %w", a.ID, err)
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// SetAccountType assigns the workspace type (personal or business) to an
// account. Unknown accounts return ErrNotFound.
func (s *Store) SetAccountType(ctx context.Context, accountID, accountType string) error {
	if accountID == "" {
		return errors.New("storage: account ID is required")
	}
	accountType = strings.ToLower(strings.TrimSpace(accountType))
	switch accountType {
	case domain.AccountTypePersonal, domain.AccountTypeBusiness:
	default:
		return fmt.Errorf("storage: account type %q must be personal or business", accountType)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET type = ?, type_manual = 1, updated_at = ? WHERE id = ?`,
		accountType, encodeTime(nowUTC()), accountID)
	if err != nil {
		return fmt.Errorf("storage: set account type: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: set account type: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ApplyConfigAccountType applies a configuration mapping unless the account has
// an explicit UI/API override. Unknown accounts return ErrNotFound.
func (s *Store) ApplyConfigAccountType(ctx context.Context, accountID, accountType string) error {
	if accountID == "" {
		return errors.New("storage: account ID is required")
	}
	accountType = strings.ToLower(strings.TrimSpace(accountType))
	switch accountType {
	case domain.AccountTypePersonal, domain.AccountTypeBusiness:
	default:
		return fmt.Errorf("storage: account type %q must be personal or business", accountType)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET type = ?, updated_at = ? WHERE id = ? AND type_manual = 0`,
		accountType, encodeTime(nowUTC()), accountID)
	if err != nil {
		return fmt.Errorf("storage: apply configured account type: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: apply configured account type: %w", err)
	}
	if n > 0 {
		return nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE id = ?`, accountID).Scan(&exists); err != nil {
		return fmt.Errorf("storage: check account existence: %w", err)
	}
	if exists == 0 {
		return ErrNotFound
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

// SetAccountRateLimit records a PSD2 cooldown for an account.
func (s *Store) SetAccountRateLimit(ctx context.Context, accountID string, until time.Time, reason string) error {
	if accountID == "" {
		return errors.New("storage: account ID is required")
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET rate_limited_until = ?, last_rate_limit_reason = ?, updated_at = ?
		WHERE id = ?`,
		encodeTime(until), strings.TrimSpace(reason), encodeTime(nowUTC()), accountID,
	)
	if err != nil {
		return fmt.Errorf("storage: set account rate limit: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: set account rate limit: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearAccountRateLimit removes an account cooldown after a successful sync.
func (s *Store) ClearAccountRateLimit(ctx context.Context, accountID string) error {
	if accountID == "" {
		return errors.New("storage: account ID is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE accounts
		SET rate_limited_until = NULL, last_rate_limit_reason = NULL, updated_at = ?
		WHERE id = ? AND (rate_limited_until IS NOT NULL OR last_rate_limit_reason IS NOT NULL)`,
		encodeTime(nowUTC()), accountID,
	)
	if err != nil {
		return fmt.Errorf("storage: clear account rate limit: %w", err)
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
