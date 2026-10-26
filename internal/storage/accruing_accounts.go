package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
	"github.com/RedRiotTank/Fiscus/pkg/accrual"
	"github.com/RedRiotTank/Fiscus/pkg/categorizer"
)

const accruingAccountColumns = `id, workspace_id, name, currency, anchor_balance_cents,
	anchor_date, apy_rate_basis_points, compounding_frequency, tax_withholding_bps,
	transfer_match_pattern, budget_category, is_active, created_at, updated_at`

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
	account.BudgetCategory = strings.TrimSpace(account.BudgetCategory)
	switch strings.ToLower(account.BudgetCategory) {
	case "", "savings", "savings / accruing", "transfers / savings":
		account.BudgetCategory = accrual.CategorySavings
	case "emergency fund":
		account.BudgetCategory = "Emergency Fund"
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
			transfer_match_pattern, budget_category, is_active, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			budget_category = excluded.budget_category,
			is_active = excluded.is_active,
			updated_at = excluded.updated_at`,
		account.ID, account.WorkspaceID, account.Name, account.Currency,
		account.AnchorBalanceCents, account.AnchorDate.Format("2006-01-02"),
		account.APYRateBasisPoints, account.CompoundingFrequency, account.TaxWithholdingBPS,
		account.TransferMatchPattern, account.BudgetCategory, active,
		encodeTime(account.CreatedAt), encodeTime(account.UpdatedAt)); err != nil {
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
	synced, err := s.ListAccounts(ctx)
	if err != nil {
		return 0, err
	}
	workspaceByAccount := make(map[string]string, len(synced))
	kindByAccount := make(map[string]string, len(synced))
	type accountHint struct {
		id   string
		text string
	}
	hints := make([]accountHint, 0, len(synced)*2)
	for _, account := range synced {
		workspaceByAccount[account.ID] = domain.NormalizeAccountType(account.Type)
		kindByAccount[account.ID] = domain.NormalizeAccountKind(account.Kind)
		for _, value := range []string{account.Name, account.IBAN, account.ID} {
			value = strings.ToLower(strings.TrimSpace(value))
			if len(value) >= 3 {
				hints = append(hints, accountHint{id: account.ID, text: value})
			}
		}
	}
	transactions, err := s.ListTransactionsBetween(ctx,
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return 0, err
	}
	type accruingMatcher struct {
		account domain.AccruingAccount
		re      *regexp.Regexp
	}
	matchers := make([]accruingMatcher, 0, len(accounts))
	for _, account := range accounts {
		if !account.IsActive {
			continue
		}
		re, compileErr := accrual.CompilePattern(account.TransferMatchPattern)
		if compileErr != nil || re == nil {
			continue
		}
		matchers = append(matchers, accruingMatcher{account: account, re: re})
	}
	matchesAccruingAccount := func(tx domain.Transaction) bool {
		for _, matcher := range matchers {
			if workspaceByAccount[tx.AccountID] != matcher.account.WorkspaceID {
				continue
			}
			if matcher.account.Currency != "" && !strings.EqualFold(tx.Currency, matcher.account.Currency) {
				continue
			}
			text := accrual.NormalizeText(tx.Counterparty + " " + tx.Description)
			if matcher.re.MatchString(text) {
				return true
			}
		}
		return false
	}
	changed := 0
	seen := make(map[int64]struct{})
	apply := func(tx domain.Transaction, category string, excluded bool) error {
		if _, done := seen[tx.ID]; done {
			return nil
		}
		seen[tx.ID] = struct{}{}
		if strings.EqualFold(tx.Category, category) && tx.IsBudgetExcluded == excluded {
			return nil
		}
		if err := s.UpdateTransactionCategory(ctx, tx.ID, category); err != nil {
			return err
		}
		changed++
		return nil
	}

	usedSavingsTransfers := make(map[int64]struct{})
	// Generic savings routing: savings patterns, account names/hints, or an
	// exact offsetting movement into an account marked savings/investment.
	for _, tx := range transactions {
		if tx.Status != domain.TransactionStatusBooked || tx.AmountMinor == 0 {
			continue
		}
		if matchesAccruingAccount(tx) {
			continue
		}
		text := accrual.NormalizeText(tx.Counterparty + " " + tx.Description)
		lowerText := strings.ToLower(text)
		kind := kindByAccount[tx.AccountID]
		matched := kind == domain.AccountKindSavings || kind == domain.AccountKindInvestment ||
			categorizer.IsSavingsTransferText(text)
		if !matched {
			for _, hint := range hints {
				if hint.id != tx.AccountID && strings.Contains(lowerText, hint.text) {
					matched = true
					break
				}
			}
		}
		if !matched && kind == domain.AccountKindChecking {
			for _, candidate := range transactions {
				candidateKind := kindByAccount[candidate.AccountID]
				if candidateKind != domain.AccountKindSavings && candidateKind != domain.AccountKindInvestment {
					continue
				}
				if candidate.Status != domain.TransactionStatusBooked || candidate.AmountMinor != -tx.AmountMinor {
					continue
				}
				if !strings.EqualFold(candidate.Currency, tx.Currency) {
					continue
				}
				if _, used := usedSavingsTransfers[candidate.ID]; used {
					continue
				}
				distance := tx.BookingDate.Sub(candidate.BookingDate)
				if distance < 0 {
					distance = -distance
				}
				if distance > 72*time.Hour {
					continue
				}
				usedSavingsTransfers[candidate.ID] = struct{}{}
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		category, excluded := accrual.CategoryForTransaction(tx.AmountMinor)
		if kind == domain.AccountKindSavings || kind == domain.AccountKindInvestment {
			category, excluded = accrual.CategoryWithdrawal, true
		}
		if err := apply(tx, category, excluded); err != nil {
			return changed, err
		}
	}

	for _, matcher := range matchers {
		account := matcher.account
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
			if _, done := seen[tx.ID]; done {
				continue
			}
			text := accrual.NormalizeText(tx.Counterparty + " " + tx.Description)
			if !matcher.re.MatchString(text) {
				continue
			}
			category := account.BudgetCategory
			if strings.TrimSpace(category) == "" {
				category = accrual.CategorySavings
			}
			excluded := false
			if tx.AmountMinor > 0 {
				category, excluded = accrual.CategoryWithdrawal, true
			}
			if err := apply(tx, category, excluded); err != nil {
				return changed, err
			}
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
		&account.TransferMatchPattern, &account.BudgetCategory, &active, &createdAt, &updatedAt,
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
	if strings.TrimSpace(account.BudgetCategory) == "" {
		account.BudgetCategory = accrual.CategorySavings
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
