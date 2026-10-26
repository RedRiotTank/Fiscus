package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
)

const accruingAccountColumns = `id, workspace_id, name, currency, anchor_balance_cents,
	anchor_date, apy_rate_basis_points, compounding_frequency, tax_withholding_bps,
	transfer_match_pattern, is_active, created_at, updated_at`

// GetAccruingAccount returns one manually tracked savings account.
func (s *Store) GetAccruingAccount(ctx context.Context, id string) (domain.AccruingAccount, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return domain.AccruingAccount{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accruingAccountColumns+` FROM accruing_accounts WHERE id = ?`, id)
	account, err := scanAccruingAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AccruingAccount{}, ErrNotFound
	}
	if err != nil {
		return domain.AccruingAccount{}, fmt.Errorf("storage: get accruing account: %w", err)
	}
	return account, nil
}

// ListAccruingAccounts returns all manually tracked savings accounts.
func (s *Store) ListAccruingAccounts(ctx context.Context) ([]domain.AccruingAccount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accruingAccountColumns+` FROM accruing_accounts
		 ORDER BY workspace_id ASC, currency ASC, name ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list accruing accounts: %w", err)
	}
	return collectAccruingAccounts(rows)
}

// SaveAccruingAccount inserts or updates a manually tracked savings account.
func (s *Store) SaveAccruingAccount(ctx context.Context, account *domain.AccruingAccount) error {
	if account == nil || strings.TrimSpace(account.ID) == "" {
		return errors.New("storage: accruing account ID is required")
	}
	account.WorkspaceID = domain.NormalizeAccountType(account.WorkspaceID)
	account.Name = strings.TrimSpace(account.Name)
	if account.Name == "" {
		return errors.New("storage: accruing account name is required")
	}
	account.Currency = strings.ToUpper(strings.TrimSpace(account.Currency))
	if account.Currency == "" {
		account.Currency = "EUR"
	}
	account.AnchorDate = account.AnchorDate.UTC()
	if account.AnchorDate.IsZero() {
		account.AnchorDate = nowUTC()
	}
	account.AnchorDate = time.Date(account.AnchorDate.Year(), account.AnchorDate.Month(), account.AnchorDate.Day(), 0, 0, 0, 0, time.UTC)
	account.CompoundingFrequency = strings.ToLower(strings.TrimSpace(account.CompoundingFrequency))
	switch account.CompoundingFrequency {
	case domain.AccruingMonthly:
	default:
		account.CompoundingFrequency = domain.AccruingDaily
	}
	if account.APYRateBasisPoints < 0 {
		account.APYRateBasisPoints = 0
	}
	if account.TaxWithholdingBPS < 0 {
		account.TaxWithholdingBPS = 0
	}
	if account.TaxWithholdingBPS > 10000 {
		account.TaxWithholdingBPS = 10000
	}
	if _, err := accrual.CompilePattern(account.TransferMatchPattern); err != nil {
		return err
	}
	now := nowUTC()
	created := account.CreatedAt.IsZero()
	if created {
		account.CreatedAt = now
	}
	account.UpdatedAt = now
	active := 0
	if account.IsActive || created {
		active = 1
	}
	// An update with IsActive explicitly false keeps the account inactive; the
	// setup form always creates active accounts.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO accruing_accounts (
			id, workspace_id, name, currency, anchor_balance_cents, anchor_date,
			apy_rate_basis_points, compounding_frequency, tax_withholding_bps,
			transfer_match_pattern, is_active, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			name = excluded.name,
			currency = excluded.currency,
			anchor_balance_cents = excluded.anchor_balance_cents,
			anchor_date = excluded.anchor_date,
			apy_rate_basis_points = excluded.apy_rate_basis_points,
			compounding_frequency = excluded.compounding_frequency,
			tax_withholding_bps = excluded.tax_withholding_bps,
			transfer_match_pattern = excluded.transfer_match_pattern,
			is_active = excluded.is_active,
			updated_at = excluded.updated_at`,
		account.ID, account.WorkspaceID, account.Name, account.Currency,
		account.AnchorBalanceCents, account.AnchorDate.Format("2006-01-02"),
		account.APYRateBasisPoints, account.CompoundingFrequency, account.TaxWithholdingBPS,
		account.TransferMatchPattern, active, encodeTime(account.CreatedAt), encodeTime(account.UpdatedAt)); err != nil {
		return fmt.Errorf("storage: save accruing account: %w", err)
	}
	return nil
}

// DeleteAccruingAccount removes a manually tracked savings account.
func (s *Store) DeleteAccruingAccount(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accruing_accounts WHERE id = ?`, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("storage: delete accruing account: %w", err)
	}
	return nil
}

// LinkAccruingTransfers applies savings categories to matching synced
// transactions. Outflows fund Savings; inflows are neutral pass-through
// withdrawals that never count as income.
func (s *Store) LinkAccruingTransfers(ctx context.Context) (int, error) {
	accounts, err := s.ListAccruingAccounts(ctx)
	if err != nil {
		return 0, err
	}
	if len(accounts) == 0 {
		return 0, nil
	}
	synced, err := s.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	workspaceByAccount := make(map[string]string, len(synced))
	for _, account := range synced {
		workspaceByAccount[account.ID] = domain.NormalizeAccountType(account.Type)
	}
	transactions, err := s.ListTransactionsBetween(ctx,
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return 0, err
	}
	changed := 0
	seen := make(map[int64]struct{})
	for _, account := range accounts {
		if !account.IsActive {
			continue
		}
		re, err := accrual.CompilePattern(account.TransferMatchPattern)
		if err != nil || re == nil {
			continue
		}
		for _, tx := range transactions {
			if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor == 0 {
				continue
			}
			if workspaceByAccount[tx.AccountID] != account.WorkspaceID {
				continue
			}
			if account.Currency != "" && !strings.EqualFold(tx.Currency, account.Currency) {
				continue
			}
			if tx.BookingDate.Before(account.AnchorDate) {
				continue
			}
			if _, done := seen[tx.ID]; done {
				continue
			}
			text := strings.TrimSpace(tx.Counterparty + " " + tx.Description)
			if !re.MatchString(text) {
				continue
			}
			category, excluded := accrual.CategoryForTransaction(tx.AmountMinor)
			if strings.EqualFold(tx.Category, category) && tx.IsBudgetExcluded == excluded {
				seen[tx.ID] = struct{}{}
				continue
			}
			if err := s.UpdateTransactionCategory(ctx, tx.ID, category); err != nil {
				return changed, err
			}
			seen[tx.ID] = struct{}{}
			changed++
		}
	}
	return changed, nil
}

type accruingRowScanner interface {
	Scan(dest ...any) error
}

func scanAccruingAccount(row accruingRowScanner) (domain.AccruingAccount, error) {
	var (
		account    domain.AccruingAccount
		anchorDate string
		active     int
		createdAt  string
		updatedAt  string
	)
	if err := row.Scan(
		&account.ID, &account.WorkspaceID, &account.Name, &account.Currency,
		&account.AnchorBalanceCents, &anchorDate, &account.APYRateBasisPoints,
		&account.CompoundingFrequency, &account.TaxWithholdingBPS,
		&account.TransferMatchPattern, &active, &createdAt, &updatedAt,
	); err != nil {
		return domain.AccruingAccount{}, err
	}
	parsed, err := time.Parse("2006-01-02", anchorDate)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, anchorDate)
	}
	if err != nil {
		return domain.AccruingAccount{}, fmt.Errorf("storage: parse accruing anchor date: %w", err)
	}
	account.AnchorDate = parsed.UTC()
	account.IsActive = active != 0
	if account.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.AccruingAccount{}, err
	}
	if account.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.AccruingAccount{}, err
	}
	if account.WorkspaceID == "" {
		account.WorkspaceID = domain.WorkspacePersonal
	}
	return account, nil
}

func collectAccruingAccounts(rows *sql.Rows) ([]domain.AccruingAccount, error) {
	defer func() { _ = rows.Close() }()
	out := make([]domain.AccruingAccount, 0)
	for rows.Next() {
		account, err := scanAccruingAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan accruing account: %w", err)
		}
		out = append(out, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate accruing accounts: %w", err)
	}
	return out, nil
}
