package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// GetBudgetProfile returns one workspace/currency allocation profile.
func (s *Store) GetBudgetProfile(ctx context.Context, workspace, currency string) (domain.BudgetProfile, bool, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	var (
		profile domain.BudgetProfile
		enabled int
		updated string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT workspace, currency, needs_bps, wants_bps, savings_bps, advisory_enabled, updated_at
		FROM budget_profiles
		WHERE workspace = ? AND currency = ?`, workspace, currency).
		Scan(&profile.Workspace, &profile.Currency, &profile.NeedsBPS, &profile.WantsBPS,
			&profile.SavingsBPS, &enabled, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BudgetProfile{}, false, nil
	}
	if err != nil {
		return domain.BudgetProfile{}, false, fmt.Errorf("storage: get budget profile: %w", err)
	}
	profile.AdvisoryEnabled = enabled != 0
	profile.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.BudgetProfile{}, false, err
	}
	return profile, true, nil
}

// ListBudgetAllocations returns all category allocations for a workspace and
// currency, ordered by their configured position.
func (s *Store) ListBudgetAllocations(ctx context.Context, workspace, currency string) ([]domain.BudgetAllocation, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace, currency, category, pillar, percent_bps, notify_at_percent,
		       sort_order, custom, updated_at
		FROM budget_allocations
		WHERE workspace = ? AND currency = ?
		ORDER BY sort_order, category COLLATE NOCASE`, workspace, currency)
	if err != nil {
		return nil, fmt.Errorf("storage: list budget allocations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.BudgetAllocation
	for rows.Next() {
		var (
			allocation domain.BudgetAllocation
			notifyRaw  string
			custom     int
			updated    string
		)
		if err := rows.Scan(&allocation.Workspace, &allocation.Currency, &allocation.Category,
			&allocation.Pillar, &allocation.PercentBPS, &notifyRaw, &allocation.SortOrder,
			&custom, &updated); err != nil {
			return nil, fmt.Errorf("storage: scan budget allocation: %w", err)
		}
		allocation.NotifyAtPercent = parsePercentList(notifyRaw)
		allocation.Custom = custom != 0
		allocation.UpdatedAt, err = decodeTime(updated)
		if err != nil {
			return nil, err
		}
		out = append(out, allocation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate budget allocations: %w", err)
	}
	return out, nil
}

// SaveBudgetPlan atomically replaces a workspace/currency profile and its
// category allocations.
func (s *Store) SaveBudgetPlan(ctx context.Context, profile domain.BudgetProfile, allocations []domain.BudgetAllocation) error {
	workspace := strings.ToLower(strings.TrimSpace(profile.Workspace))
	currency := strings.ToUpper(strings.TrimSpace(profile.Currency))
	if workspace == "" || currency == "" {
		return errors.New("storage: budget profile workspace and currency are required")
	}
	if profile.NeedsBPS < 0 || profile.WantsBPS < 0 || profile.SavingsBPS < 0 ||
		profile.NeedsBPS+profile.WantsBPS+profile.SavingsBPS > 10000 {
		return errors.New("storage: budget pillar percentages cannot exceed 100%")
	}
	updated := profile.UpdatedAt
	if updated.IsZero() {
		updated = nowUTC()
	}
	profile.Workspace = workspace
	profile.Currency = currency
	profile.UpdatedAt = updated

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin budget plan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	enabled := 0
	if profile.AdvisoryEnabled {
		enabled = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO budget_profiles (workspace, currency, needs_bps, wants_bps, savings_bps, advisory_enabled, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace, currency) DO UPDATE SET
			needs_bps = excluded.needs_bps,
			wants_bps = excluded.wants_bps,
			savings_bps = excluded.savings_bps,
			advisory_enabled = excluded.advisory_enabled,
			updated_at = excluded.updated_at`,
		profile.Workspace, profile.Currency, profile.NeedsBPS, profile.WantsBPS,
		profile.SavingsBPS, enabled, encodeTime(updated)); err != nil {
		return fmt.Errorf("storage: save budget profile: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM budget_allocations WHERE workspace = ? AND currency = ?`,
		profile.Workspace, profile.Currency); err != nil {
		return fmt.Errorf("storage: clear budget allocations: %w", err)
	}
	for index, allocation := range allocations {
		category := strings.TrimSpace(allocation.Category)
		if category == "" {
			return errors.New("storage: budget allocation category is required")
		}
		pillar := strings.ToLower(strings.TrimSpace(allocation.Pillar))
		switch pillar {
		case "needs", "wants", "savings":
		default:
			return fmt.Errorf("storage: invalid budget pillar %q", allocation.Pillar)
		}
		if allocation.PercentBPS < 0 {
			return errors.New("storage: budget allocation percent cannot be negative")
		}
		sortOrder := allocation.SortOrder
		if sortOrder == 0 {
			sortOrder = index
		}
		custom := 0
		if allocation.Custom {
			custom = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO budget_allocations
				(workspace, currency, category, pillar, percent_bps, notify_at_percent, sort_order, custom, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			profile.Workspace, profile.Currency, category, pillar, allocation.PercentBPS,
			encodePercentList(allocation.NotifyAtPercent), sortOrder, custom, encodeTime(updated)); err != nil {
			return fmt.Errorf("storage: save budget allocation %s: %w", category, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit budget plan: %w", err)
	}
	return nil
}

// SetBudgetProfileAdvisory updates the advisory toggle for an existing plan.
func (s *Store) SetBudgetProfileAdvisory(ctx context.Context, workspace, currency string, enabled bool) error {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	value := 0
	if enabled {
		value = 1
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE budget_profiles
		SET advisory_enabled = ?, updated_at = ?
		WHERE workspace = ? AND currency = ?`,
		value, encodeTime(nowUTC()), workspace, currency)
	if err != nil {
		return fmt.Errorf("storage: update budget advisory: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: budget advisory rows: %w", err)
	}
	if rows == 0 {
		return errors.New("storage: budget profile not found")
	}
	return nil
}
