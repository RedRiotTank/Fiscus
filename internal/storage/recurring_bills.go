package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const recurringBillColumns = `id, workspace, currency, counterparty, category, pillar,
	recurrence, split_mode, split_people, gross_minor, personal_share_minor, active, created_at, updated_at`

func scanRecurringBillRule(row rowScanner) (domain.RecurringBillRule, error) {
	var (
		rule      domain.RecurringBillRule
		active    int
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&rule.ID, &rule.Workspace, &rule.Currency, &rule.Counterparty, &rule.Category,
		&rule.Pillar, &rule.Recurrence, &rule.SplitMode, &rule.SplitPeople, &rule.GrossMinor,
		&rule.PersonalShareMinor, &active, &createdAt, &updatedAt,
	); err != nil {
		return domain.RecurringBillRule{}, err
	}
	rule.Active = active != 0
	var err error
	if rule.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.RecurringBillRule{}, err
	}
	if rule.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.RecurringBillRule{}, err
	}
	return rule, nil
}

func normalizeRecurringBillRule(rule domain.RecurringBillRule) (domain.RecurringBillRule, error) {
	rule.Workspace = strings.ToLower(strings.TrimSpace(rule.Workspace))
	if rule.Workspace == "" {
		rule.Workspace = domain.WorkspacePersonal
	}
	rule.Currency = strings.ToUpper(strings.TrimSpace(rule.Currency))
	if rule.Currency == "" {
		rule.Currency = "EUR"
	}
	rule.Counterparty = strings.TrimSpace(rule.Counterparty)
	if rule.Counterparty == "" {
		return domain.RecurringBillRule{}, errors.New("storage: recurring bill counterparty is required")
	}
	rule.Category = strings.TrimSpace(rule.Category)
	if rule.Category == "" {
		rule.Category = "Utilities & Telecom"
	}
	rule.Pillar = strings.ToLower(strings.TrimSpace(rule.Pillar))
	switch rule.Pillar {
	case "needs", "wants", "savings":
	default:
		return domain.RecurringBillRule{}, fmt.Errorf("storage: invalid recurring bill pillar %q", rule.Pillar)
	}
	rule.Recurrence = strings.ToLower(strings.TrimSpace(rule.Recurrence))
	if rule.Recurrence == "" {
		rule.Recurrence = domain.RecurringMonthly
	}
	switch rule.Recurrence {
	case domain.RecurringMonthly, domain.RecurringQuarterly, domain.RecurringYearly:
	default:
		return domain.RecurringBillRule{}, fmt.Errorf("storage: invalid recurring bill recurrence %q", rule.Recurrence)
	}
	rule.SplitMode = strings.ToLower(strings.TrimSpace(rule.SplitMode))
	switch rule.SplitMode {
	case domain.RecurringSplitSolo, domain.RecurringSplitShared:
	default:
		return domain.RecurringBillRule{}, fmt.Errorf("storage: invalid recurring bill split mode %q", rule.SplitMode)
	}
	if rule.SplitPeople < 1 {
		rule.SplitPeople = 1
	}
	if rule.GrossMinor < 0 {
		return domain.RecurringBillRule{}, errors.New("storage: recurring bill gross amount cannot be negative")
	}
	if rule.PersonalShareMinor < 0 {
		return domain.RecurringBillRule{}, errors.New("storage: recurring bill personal share cannot be negative")
	}
	return rule, nil
}

// ListRecurringBillRules returns confirmed recurring bill rules for a workspace
// and currency ordered by counterparty.
func (s *Store) ListRecurringBillRules(ctx context.Context, workspace, currency string) ([]domain.RecurringBillRule, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+recurringBillColumns+`
		FROM recurring_bill_rules
		WHERE workspace = ? AND currency = ? AND active = 1
		ORDER BY counterparty ASC`, workspace, currency)
	if err != nil {
		return nil, fmt.Errorf("storage: list recurring bill rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.RecurringBillRule
	for rows.Next() {
		rule, scanErr := scanRecurringBillRule(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan recurring bill rule: %w", scanErr)
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate recurring bill rules: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Counterparty) < strings.ToLower(out[j].Counterparty)
	})
	return out, nil
}

// ListAllRecurringBillRules returns every active recurring bill rule across
// workspaces and currencies.
func (s *Store) ListAllRecurringBillRules(ctx context.Context) ([]domain.RecurringBillRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+recurringBillColumns+`
		FROM recurring_bill_rules
		WHERE active = 1
		ORDER BY workspace ASC, currency ASC, counterparty ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list all recurring bill rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.RecurringBillRule
	for rows.Next() {
		rule, scanErr := scanRecurringBillRule(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan recurring bill rule: %w", scanErr)
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate recurring bill rules: %w", err)
	}
	return out, nil
}

// GetRecurringBillRule returns one confirmed rule for a counterparty.
func (s *Store) GetRecurringBillRule(ctx context.Context, workspace, currency, counterparty string) (domain.RecurringBillRule, bool, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	counterparty = strings.TrimSpace(counterparty)
	row := s.db.QueryRowContext(ctx, `
		SELECT `+recurringBillColumns+`
		FROM recurring_bill_rules
		WHERE workspace = ? AND currency = ? AND counterparty = ? COLLATE NOCASE`,
		workspace, currency, counterparty)
	rule, err := scanRecurringBillRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RecurringBillRule{}, false, nil
	}
	if err != nil {
		return domain.RecurringBillRule{}, false, fmt.Errorf("storage: get recurring bill rule: %w", err)
	}
	return rule, true, nil
}

// SaveRecurringBillRule creates or replaces a confirmed recurring bill rule.
func (s *Store) SaveRecurringBillRule(ctx context.Context, rule domain.RecurringBillRule) (domain.RecurringBillRule, error) {
	rule, err := normalizeRecurringBillRule(rule)
	if err != nil {
		return domain.RecurringBillRule{}, err
	}
	now := nowUTC()
	active := 0
	if rule.Active || rule.ID == 0 {
		active = 1
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO recurring_bill_rules
			(workspace, currency, counterparty, category, pillar, recurrence, split_mode,
			 split_people, gross_minor, personal_share_minor, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace, currency, counterparty) DO UPDATE SET
			category = excluded.category,
			pillar = excluded.pillar,
			recurrence = excluded.recurrence,
			split_mode = excluded.split_mode,
			split_people = excluded.split_people,
			gross_minor = excluded.gross_minor,
			personal_share_minor = excluded.personal_share_minor,
			active = excluded.active,
			updated_at = excluded.updated_at`,
		rule.Workspace, rule.Currency, rule.Counterparty, rule.Category, rule.Pillar,
		rule.Recurrence, rule.SplitMode, rule.SplitPeople, rule.GrossMinor, rule.PersonalShareMinor,
		active, encodeTime(now), encodeTime(now)); err != nil {
		return domain.RecurringBillRule{}, fmt.Errorf("storage: save recurring bill rule: %w", err)
	}
	saved, ok, err := s.GetRecurringBillRule(ctx, rule.Workspace, rule.Currency, rule.Counterparty)
	if err != nil {
		return domain.RecurringBillRule{}, err
	}
	if !ok {
		return domain.RecurringBillRule{}, errors.New("storage: recurring bill rule disappeared after save")
	}
	return saved, nil
}

// DeleteRecurringBillRule removes a confirmed rule.
func (s *Store) DeleteRecurringBillRule(ctx context.Context, workspace, currency, counterparty string) error {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM recurring_bill_rules
		WHERE workspace = ? AND currency = ? AND counterparty = ? COLLATE NOCASE`,
		workspace, currency, strings.TrimSpace(counterparty)); err != nil {
		return fmt.Errorf("storage: delete recurring bill rule: %w", err)
	}
	return nil
}
