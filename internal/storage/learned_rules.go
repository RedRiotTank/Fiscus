package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// ListLearnedRules returns persisted inline-learning rules, most specific
// first so a broad merchant pattern cannot shadow a narrower one.
func (s *Store) ListLearnedRules(ctx context.Context) ([]domain.LearnedRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pattern, category, source, created_at, updated_at
		FROM learned_rules
		ORDER BY LENGTH(pattern) DESC, updated_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list learned rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.LearnedRule
	for rows.Next() {
		var (
			rule      domain.LearnedRule
			created   string
			updatedAt string
		)
		if err := rows.Scan(&rule.ID, &rule.Pattern, &rule.Category, &rule.Source, &created, &updatedAt); err != nil {
			return nil, fmt.Errorf("storage: scan learned rule: %w", err)
		}
		rule.CreatedAt, err = decodeTime(created)
		if err != nil {
			return nil, err
		}
		rule.UpdatedAt, err = decodeTime(updatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate learned rules: %w", err)
	}
	return out, nil
}

// UpsertLearnedRule persists a user-taught pattern. Existing patterns are
// updated in place so reclassifying a merchant never creates contradictory
// duplicates.
func (s *Store) UpsertLearnedRule(ctx context.Context, pattern, category, source string) (domain.LearnedRule, error) {
	pattern = strings.TrimSpace(pattern)
	category = strings.TrimSpace(category)
	source = strings.TrimSpace(source)
	if pattern == "" || category == "" {
		return domain.LearnedRule{}, errors.New("storage: learned rule pattern and category are required")
	}
	if source == "" {
		source = "inline"
	}
	now := encodeTime(nowUTC())
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO learned_rules (pattern, category, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pattern) DO UPDATE SET
			category = excluded.category,
			source = excluded.source,
			updated_at = excluded.updated_at`,
		pattern, category, source, now, now); err != nil {
		return domain.LearnedRule{}, fmt.Errorf("storage: upsert learned rule: %w", err)
	}
	var (
		rule      domain.LearnedRule
		created   string
		updatedAt string
		err       error
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, pattern, category, source, created_at, updated_at
		FROM learned_rules WHERE pattern = ?`, pattern).
		Scan(&rule.ID, &rule.Pattern, &rule.Category, &rule.Source, &created, &updatedAt); err != nil {
		return domain.LearnedRule{}, fmt.Errorf("storage: read learned rule: %w", err)
	}
	rule.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.LearnedRule{}, err
	}
	rule.UpdatedAt, err = decodeTime(updatedAt)
	if err != nil {
		return domain.LearnedRule{}, err
	}
	return rule, nil
}
