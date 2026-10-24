package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// ListBudgetOverrides returns persisted user-adopted budgets.
func (s *Store) ListBudgetOverrides(ctx context.Context) ([]domain.BudgetOverride, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT category, currency, limit_minor, notify_at_percent, updated_at
		FROM budget_overrides
		ORDER BY category COLLATE NOCASE, currency`)
	if err != nil {
		return nil, fmt.Errorf("storage: list budget overrides: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.BudgetOverride
	for rows.Next() {
		var (
			override  domain.BudgetOverride
			notifyRaw string
			updated   string
		)
		if err := rows.Scan(&override.Category, &override.Currency, &override.LimitMinor, &notifyRaw, &updated); err != nil {
			return nil, fmt.Errorf("storage: scan budget override: %w", err)
		}
		override.NotifyAtPercent = parsePercentList(notifyRaw)
		override.UpdatedAt, err = decodeTime(updated)
		if err != nil {
			return nil, err
		}
		out = append(out, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate budget overrides: %w", err)
	}
	return out, nil
}

// UpsertBudgetOverride inserts or updates one persisted category budget.
func (s *Store) UpsertBudgetOverride(ctx context.Context, override domain.BudgetOverride) error {
	category := strings.TrimSpace(override.Category)
	if category == "" {
		return errors.New("storage: budget category is required")
	}
	currency := strings.ToUpper(strings.TrimSpace(override.Currency))
	if currency == "" {
		currency = "EUR"
	}
	if override.LimitMinor <= 0 {
		return errors.New("storage: budget limit must be positive")
	}
	updated := override.UpdatedAt
	if updated.IsZero() {
		updated = nowUTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO budget_overrides (category, currency, limit_minor, notify_at_percent, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(category, currency) DO UPDATE SET
			limit_minor = excluded.limit_minor,
			notify_at_percent = excluded.notify_at_percent,
			updated_at = excluded.updated_at`,
		category, currency, override.LimitMinor, encodePercentList(override.NotifyAtPercent), encodeTime(updated))
	if err != nil {
		return fmt.Errorf("storage: upsert budget override: %w", err)
	}
	return nil
}

// GetSetting returns a stored string setting.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("storage: get setting: %w", err)
	}
	return value, true, nil
}

// SetSetting stores a string setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("storage: setting key is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, encodeTime(nowUTC()))
	if err != nil {
		return fmt.Errorf("storage: set setting: %w", err)
	}
	return nil
}

func encodePercentList(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value > 0 && value <= 100 {
			parts = append(parts, strconv.Itoa(value))
		}
	}
	return strings.Join(parts, ",")
}

func parsePercentList(raw string) []int {
	fields := strings.Split(raw, ",")
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(strings.TrimSpace(field))
		if err == nil && value > 0 && value <= 100 {
			out = append(out, value)
		}
	}
	return out
}
