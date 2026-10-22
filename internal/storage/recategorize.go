package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

// CategoryShift aggregates proposed category changes with the same source and
// destination category.
type CategoryShift struct {
	From  string
	To    string
	Count int
}

// CategoryChange describes one transaction whose category would change.
type CategoryChange struct {
	ID           int64
	AccountID    string
	ExternalID   string
	Counterparty string
	Description  string
	From         string
	To           string
}

// RecategorizeResult summarizes a batch reclassification.
type RecategorizeResult struct {
	Examined int
	Changed  int
	Applied  bool
	Changes  []CategoryChange
	Shifts   []CategoryShift
}

// Recategorize evaluates classify against every stored transaction inside one
// database transaction. When apply is false, the transaction is rolled back and
// the database is left untouched. Changes are returned in a deterministic order
// so CLI and dashboard callers can render stable summaries.
func (s *Store) Recategorize(ctx context.Context, classify func(domain.Transaction) string, apply bool) (RecategorizeResult, error) {
	if classify == nil {
		return RecategorizeResult{}, errors.New("storage: category classifier is required")
	}
	return s.recategorize(ctx, classify, apply)
}

func (s *Store) recategorize(ctx context.Context, classify func(domain.Transaction) string, apply bool) (RecategorizeResult, error) {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecategorizeResult{}, fmt.Errorf("storage: begin recategorize transaction: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()

	rows, err := sqlTx.QueryContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions ORDER BY booking_date DESC, id DESC`)
	if err != nil {
		return RecategorizeResult{}, fmt.Errorf("storage: list transactions for recategorization: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := RecategorizeResult{Applied: apply}
	shiftCounts := make(map[CategoryShift]int)
	for rows.Next() {
		tx, scanErr := scanTransaction(rows)
		if scanErr != nil {
			return RecategorizeResult{}, fmt.Errorf("storage: scan transaction for recategorization: %w", scanErr)
		}
		result.Examined++
		next := strings.TrimSpace(classify(tx))
		if next == "" || next == tx.Category {
			continue
		}
		result.Changed++
		result.Changes = append(result.Changes, CategoryChange{
			ID:           tx.ID,
			AccountID:    tx.AccountID,
			ExternalID:   tx.ExternalID,
			Counterparty: tx.Counterparty,
			Description:  tx.Description,
			From:         tx.Category,
			To:           next,
		})
		shiftCounts[CategoryShift{From: tx.Category, To: next}]++
	}
	if err := rows.Err(); err != nil {
		return RecategorizeResult{}, fmt.Errorf("storage: iterate transactions for recategorization: %w", err)
	}
	if err := rows.Close(); err != nil {
		return RecategorizeResult{}, fmt.Errorf("storage: close recategorization rows: %w", err)
	}

	for _, change := range result.Changes {
		if !apply {
			continue
		}
		if _, execErr := sqlTx.ExecContext(ctx,
			`UPDATE transactions SET category = ?, updated_at = ? WHERE id = ?`,
			change.To, encodeTime(nowUTC()), change.ID,
		); execErr != nil {
			return RecategorizeResult{}, fmt.Errorf("storage: update transaction %d category: %w", change.ID, execErr)
		}
	}

	for shift, count := range shiftCounts {
		shift.Count = count
		result.Shifts = append(result.Shifts, shift)
	}
	sort.Slice(result.Shifts, func(i, j int) bool {
		if result.Shifts[i].From == result.Shifts[j].From {
			return result.Shifts[i].To < result.Shifts[j].To
		}
		return result.Shifts[i].From < result.Shifts[j].From
	})

	if !apply {
		if err := sqlTx.Rollback(); err != nil {
			return RecategorizeResult{}, fmt.Errorf("storage: roll back recategorization preview: %w", err)
		}
		return result, nil
	}
	if err := sqlTx.Commit(); err != nil {
		return RecategorizeResult{}, fmt.Errorf("storage: commit recategorization: %w", err)
	}
	return result, nil
}
