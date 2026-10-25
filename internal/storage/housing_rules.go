package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// GetHousingRule returns the persisted shared-flat rule for a workspace and
// currency.
func (s *Store) GetHousingRule(ctx context.Context, workspace, currency string) (domain.HousingRule, bool, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	var (
		rule      domain.HousingRule
		flatmates string
		created   string
		updated   string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, workspace, currency, landlord_counterparty, personal_net_share_cents,
		       calculation_mode, flatmate_counterparties, created_at, updated_at
		FROM housing_rules
		WHERE workspace = ? AND currency = ?`, workspace, currency).
		Scan(&rule.ID, &rule.Workspace, &rule.Currency, &rule.LandlordCounterparty,
			&rule.PersonalNetShareCents, &rule.CalculationMode, &flatmates, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.HousingRule{}, false, nil
	}
	if err != nil {
		return domain.HousingRule{}, false, fmt.Errorf("storage: get housing rule: %w", err)
	}
	if flatmates != "" {
		if err := json.Unmarshal([]byte(flatmates), &rule.FlatmateCounterparties); err != nil {
			return domain.HousingRule{}, false, fmt.Errorf("storage: decode housing flatmates: %w", err)
		}
	}
	rule.CreatedAt, err = decodeTime(created)
	if err != nil {
		return domain.HousingRule{}, false, err
	}
	rule.UpdatedAt, err = decodeTime(updated)
	if err != nil {
		return domain.HousingRule{}, false, err
	}
	return rule, true, nil
}

// SaveHousingRule creates or replaces the shared-flat rule for a workspace and
// currency.
func (s *Store) SaveHousingRule(ctx context.Context, rule domain.HousingRule) (domain.HousingRule, error) {
	workspace := strings.ToLower(strings.TrimSpace(rule.Workspace))
	currency := strings.ToUpper(strings.TrimSpace(rule.Currency))
	if workspace == "" {
		workspace = domain.WorkspacePersonal
	}
	if currency == "" {
		currency = "EUR"
	}
	landlord := strings.TrimSpace(rule.LandlordCounterparty)
	if landlord == "" {
		return domain.HousingRule{}, errors.New("storage: housing landlord counterparty is required")
	}
	mode := strings.ToLower(strings.TrimSpace(rule.CalculationMode))
	switch mode {
	case domain.HousingModeNetBalance, domain.HousingModeFixedShare:
	default:
		return domain.HousingRule{}, fmt.Errorf("storage: invalid housing calculation mode %q", rule.CalculationMode)
	}
	if rule.PersonalNetShareCents < 0 {
		return domain.HousingRule{}, errors.New("storage: housing personal share cannot be negative")
	}
	flatmates := make([]string, 0, len(rule.FlatmateCounterparties))
	seen := make(map[string]struct{}, len(rule.FlatmateCounterparties))
	for _, flatmate := range rule.FlatmateCounterparties {
		flatmate = strings.TrimSpace(flatmate)
		if flatmate == "" {
			continue
		}
		key := strings.ToLower(flatmate)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		flatmates = append(flatmates, flatmate)
	}
	flatmateJSON, err := json.Marshal(flatmates)
	if err != nil {
		return domain.HousingRule{}, fmt.Errorf("storage: encode housing flatmates: %w", err)
	}
	now := nowUTC()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO housing_rules
			(workspace, currency, landlord_counterparty, personal_net_share_cents,
			 calculation_mode, flatmate_counterparties, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace, currency) DO UPDATE SET
			landlord_counterparty = excluded.landlord_counterparty,
			personal_net_share_cents = excluded.personal_net_share_cents,
			calculation_mode = excluded.calculation_mode,
			flatmate_counterparties = excluded.flatmate_counterparties,
			updated_at = excluded.updated_at`,
		workspace, currency, landlord, rule.PersonalNetShareCents, mode,
		string(flatmateJSON), encodeTime(now), encodeTime(now)); err != nil {
		return domain.HousingRule{}, fmt.Errorf("storage: save housing rule: %w", err)
	}
	rule, ok, err := s.GetHousingRule(ctx, workspace, currency)
	if err != nil {
		return domain.HousingRule{}, err
	}
	if !ok {
		return domain.HousingRule{}, errors.New("storage: housing rule disappeared after save")
	}
	return rule, nil
}

// DeleteHousingRule removes a workspace/currency shared-flat rule.
func (s *Store) DeleteHousingRule(ctx context.Context, workspace, currency string) error {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "EUR"
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM housing_rules WHERE workspace = ? AND currency = ?`, workspace, currency); err != nil {
		return fmt.Errorf("storage: delete housing rule: %w", err)
	}
	return nil
}

// DeleteLearnedRulesBySource removes generated rules for reconfiguration.
func (s *Store) DeleteLearnedRulesBySource(ctx context.Context, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("storage: learned rule source is required")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM learned_rules WHERE source = ?`, source); err != nil {
		return fmt.Errorf("storage: delete learned rules by source: %w", err)
	}
	return nil
}
