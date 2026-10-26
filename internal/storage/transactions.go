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

const transactionColumns = `id, account_id, external_id, amount_minor, currency, booking_date,
	value_date, counterparty, description, category, notes, status, created_at, updated_at`

// FlowFacet is the direction half of the transaction filter toolbar.
type FlowFacet string

const (
	// FlowAll applies no amount predicate.
	FlowAll FlowFacet = "all"
	// FlowInflows keeps positive amounts.
	FlowInflows FlowFacet = "inflows"
	// FlowOutflows keeps negative amounts.
	FlowOutflows FlowFacet = "outflows"
)

// CategoryFacet is the category/pillar half of the transaction filter toolbar.
type CategoryFacet string

const (
	// CategoryFacetAll applies no category predicate.
	CategoryFacetAll CategoryFacet = "all"
	// CategoryFacetNeeds keeps categories allocated to the Needs pillar.
	CategoryFacetNeeds CategoryFacet = "needs"
	// CategoryFacetWants keeps categories allocated to the Wants pillar.
	CategoryFacetWants CategoryFacet = "wants"
	// CategoryFacetHousing keeps housing and rent categories.
	CategoryFacetHousing CategoryFacet = "housing"
	// CategoryFacetUtilities keeps utilities and telecom categories.
	CategoryFacetUtilities CategoryFacet = "utilities"
	// CategoryFacetGroceries keeps grocery categories.
	CategoryFacetGroceries CategoryFacet = "groceries"
	// CategoryFacetP2P keeps Bizum and peer-to-peer transfers.
	CategoryFacetP2P CategoryFacet = "p2p"
	// CategoryFacetCash keeps cash and ATM withdrawals.
	CategoryFacetCash CategoryFacet = "cash"
)

// NormalizeFlowFacet validates a direction filter query parameter.
func NormalizeFlowFacet(raw string) FlowFacet {
	switch FlowFacet(strings.ToLower(strings.TrimSpace(raw))) {
	case FlowInflows:
		return FlowInflows
	case FlowOutflows:
		return FlowOutflows
	default:
		return FlowAll
	}
}

// NormalizeCategoryFacet validates a category filter query parameter.
func NormalizeCategoryFacet(raw string) CategoryFacet {
	switch CategoryFacet(strings.ToLower(strings.TrimSpace(raw))) {
	case CategoryFacetNeeds:
		return CategoryFacetNeeds
	case CategoryFacetWants:
		return CategoryFacetWants
	case CategoryFacetHousing:
		return CategoryFacetHousing
	case CategoryFacetUtilities:
		return CategoryFacetUtilities
	case CategoryFacetGroceries:
		return CategoryFacetGroceries
	case CategoryFacetP2P:
		return CategoryFacetP2P
	case CategoryFacetCash:
		return CategoryFacetCash
	default:
		return CategoryFacetAll
	}
}

// NormalizeLegacyFilter maps the previous single-chip filter onto the two
// facets so bookmarked transaction URLs keep working.
func NormalizeLegacyFilter(raw string) (FlowFacet, CategoryFacet) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "inflows":
		return FlowInflows, CategoryFacetAll
	case "outflows":
		return FlowOutflows, CategoryFacetAll
	case "needs":
		return FlowAll, CategoryFacetNeeds
	case "wants":
		return FlowAll, CategoryFacetWants
	case "p2p":
		return FlowAll, CategoryFacetP2P
	default:
		return FlowAll, CategoryFacetAll
	}
}

// TransactionFilter controls transaction listing. Flow and Category are
// independent facets and combine with AND.
type TransactionFilter struct {
	AccountID    string
	AccountIDs   []string
	Flow         FlowFacet
	Category     CategoryFacet
	Categories   []string
	CategoryName string
	MonthFrom    time.Time
	MonthTo      time.Time
	Limit        int
	Offset       int
}

var (
	housingFacetCategories = []string{
		"housing / rent", "housing", "rent", "renta", "alquiler", "mortgage", "hipoteca",
	}
	utilitiesFacetCategories = []string{
		"utilities & telecom", "utilities", "utility", "telecom", "telecomunicaciones",
	}
	groceriesFacetCategories = []string{
		"groceries", "grocery", "supermarket", "supermercado",
	}
	p2pFacetCategories = []string{
		"transfers / p2p", "bizum", "transfers / bizum", "p2p",
	}
	cashFacetCategories = []string{
		"cash / atm", "cash", "atm", "cajero", "reintegro",
	}
)

func transactionWhere(filter TransactionFilter) ([]string, []any) {
	var (
		where []string
		args  []any
	)
	if filter.AccountID != "" {
		where = append(where, "account_id = ?")
		args = append(args, filter.AccountID)
	} else if clause, clauseArgs := accountIDsClause(filter.AccountIDs); clause != "" {
		where = append(where, clause)
		args = append(args, clauseArgs...)
	}
	if !filter.MonthFrom.IsZero() {
		where = append(where, "booking_date >= ?")
		args = append(args, encodeTime(filter.MonthFrom))
	}
	if !filter.MonthTo.IsZero() {
		where = append(where, "booking_date < ?")
		args = append(args, encodeTime(filter.MonthTo))
	}
	if category := strings.TrimSpace(filter.CategoryName); category != "" {
		categories := canonicalFilterCategories([]string{category})
		if len(categories) > 1 {
			placeholders := make([]string, len(categories))
			for i, candidate := range categories {
				placeholders[i] = "?"
				args = append(args, candidate)
			}
			where = append(where, "LOWER(TRIM(category)) IN ("+strings.Join(placeholders, ",")+")")
		} else {
			where = append(where, "LOWER(TRIM(category)) = ?")
			args = append(args, strings.ToLower(category))
		}
	}
	switch NormalizeFlowFacet(string(filter.Flow)) {
	case FlowInflows:
		where = append(where, "amount_minor > 0")
	case FlowOutflows:
		where = append(where, "amount_minor < 0")
	}
	categoryFacet := NormalizeCategoryFacet(string(filter.Category))
	var categories []string
	switch categoryFacet {
	case CategoryFacetNeeds, CategoryFacetWants:
		categories = canonicalFilterCategories(filter.Categories)
	case CategoryFacetHousing:
		categories = housingFacetCategories
	case CategoryFacetUtilities:
		categories = utilitiesFacetCategories
	case CategoryFacetGroceries:
		categories = groceriesFacetCategories
	case CategoryFacetP2P:
		categories = p2pFacetCategories
	case CategoryFacetCash:
		categories = cashFacetCategories
	}
	if categoryFacet != CategoryFacetAll {
		if len(categories) == 0 {
			where = append(where, "1 = 0")
		} else {
			placeholders := make([]string, len(categories))
			for i, category := range categories {
				placeholders[i] = "?"
				args = append(args, category)
			}
			where = append(where, "LOWER(TRIM(category)) IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	return where, args
}

func canonicalFilterCategories(categories []string) []string {
	seen := make(map[string]struct{}, len(categories))
	out := make([]string, 0, len(categories))
	p2p := false
	for _, category := range categories {
		category = strings.ToLower(strings.TrimSpace(category))
		if category == "" {
			continue
		}
		if _, exists := seen[category]; exists {
			continue
		}
		seen[category] = struct{}{}
		out = append(out, category)
		switch category {
		case "transfers / p2p", "transfers/p2p", "p2p", "transfers / bizum", "transfers/bizum", "bizum":
			p2p = true
		}
	}
	if p2p {
		for _, category := range p2pFacetCategories {
			if _, exists := seen[category]; exists {
				continue
			}
			seen[category] = struct{}{}
			out = append(out, category)
		}
	}
	return out
}

// accountIDsClause returns an "account_id IN (...)" predicate and its
// arguments. An empty slice produces an empty predicate.
func accountIDsClause(accountIDs []string) (string, []any) {
	if len(accountIDs) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(accountIDs))
	args := make([]any, len(accountIDs))
	for i, id := range accountIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	return "account_id IN (" + strings.Join(placeholders, ",") + ")", args
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

func scanTransaction(row rowScanner) (domain.Transaction, error) {
	var (
		t         domain.Transaction
		booking   string
		valueDate sql.NullString
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&t.ID, &t.AccountID, &t.ExternalID, &t.AmountMinor, &t.Currency, &booking,
		&valueDate, &t.Counterparty, &t.Description, &t.Category, &t.Notes, &t.Status, &createdAt, &updatedAt,
	); err != nil {
		return domain.Transaction{}, err
	}
	var err error
	if t.BookingDate, err = decodeTime(booking); err != nil {
		return domain.Transaction{}, err
	}
	if t.ValueDate, err = decodeNullTime(valueDate); err != nil {
		return domain.Transaction{}, err
	}
	if t.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.Transaction{}, err
	}
	if t.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.Transaction{}, err
	}
	return t, nil
}

// UpsertTransaction stores a transaction idempotently, keyed by
// (account_id, external_id). It reports whether the row was newly inserted and
// whether a previously pending transaction was promoted to booked. Rules should
// be evaluated for both newly inserted and promoted transactions.
func (s *Store) UpsertTransaction(ctx context.Context, t *domain.Transaction) (inserted bool, promoted bool, err error) {
	if t.AccountID == "" {
		return false, false, errors.New("storage: transaction account ID is required")
	}
	if t.ExternalID == "" {
		return false, false, errors.New("storage: transaction external ID is required")
	}
	if t.Currency == "" {
		return false, false, errors.New("storage: transaction currency is required")
	}
	if t.Status == "" {
		t.Status = domain.TransactionStatusBooked
	}
	if strings.TrimSpace(t.Category) == "" {
		t.Category = "Uncategorized"
	}
	if t.BookingDate.IsZero() {
		t.BookingDate = nowUTC()
	}
	t.BookingDate = t.BookingDate.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, fmt.Errorf("storage: begin transaction upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		existingID        int64
		existingStatus    string
		existingCreatedAt string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, status, created_at FROM transactions
		WHERE account_id = ? AND external_id = ?`,
		t.AccountID, t.ExternalID,
	).Scan(&existingID, &existingStatus, &existingCreatedAt)

	now := nowUTC()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if t.CreatedAt.IsZero() {
			t.CreatedAt = now
		}
		t.UpdatedAt = now
		res, execErr := tx.ExecContext(ctx, `
			INSERT INTO transactions (
				account_id, external_id, amount_minor, currency, booking_date, value_date,
				counterparty, description, category, notes, status, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.AccountID, t.ExternalID, t.AmountMinor, t.Currency, encodeTime(t.BookingDate),
			encodeNullTime(t.ValueDate), t.Counterparty, t.Description, t.Category, t.Notes, t.Status,
			encodeTime(t.CreatedAt), encodeTime(t.UpdatedAt),
		)
		if execErr != nil {
			return false, false, fmt.Errorf("storage: insert transaction: %w", execErr)
		}
		id, idErr := res.LastInsertId()
		if idErr != nil {
			return false, false, fmt.Errorf("storage: transaction ID: %w", idErr)
		}
		t.ID = id
		inserted = true
	case err != nil:
		return false, false, fmt.Errorf("storage: inspect transaction: %w", err)
	default:
		createdAt, parseErr := decodeTime(existingCreatedAt)
		if parseErr != nil {
			return false, false, parseErr
		}
		t.ID = existingID
		t.CreatedAt = createdAt
		t.UpdatedAt = now
		_, execErr := tx.ExecContext(ctx, `
			UPDATE transactions SET
				amount_minor = ?, currency = ?, booking_date = ?, value_date = ?,
				counterparty = ?, description = ?, category = ?, notes = ?, status = ?, updated_at = ?
			WHERE id = ?`,
			t.AmountMinor, t.Currency, encodeTime(t.BookingDate), encodeNullTime(t.ValueDate),
			t.Counterparty, t.Description, t.Category, t.Notes, t.Status, encodeTime(t.UpdatedAt), existingID,
		)
		if execErr != nil {
			return false, false, fmt.Errorf("storage: update transaction: %w", execErr)
		}
		promoted = existingStatus != domain.TransactionStatusBooked && t.Status == domain.TransactionStatusBooked
	}

	if err := tx.Commit(); err != nil {
		return false, false, fmt.Errorf("storage: commit transaction upsert: %w", err)
	}
	return inserted, promoted, nil
}

// GetTransaction returns one transaction by ID.
func (s *Store) GetTransaction(ctx context.Context, id int64) (domain.Transaction, error) {
	if id <= 0 {
		return domain.Transaction{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+transactionColumns+` FROM transactions WHERE id = ?`, id)
	t, err := scanTransaction(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Transaction{}, ErrNotFound
	}
	if err != nil {
		return domain.Transaction{}, fmt.Errorf("storage: get transaction: %w", err)
	}
	return t, nil
}

// UpdateTransactionCategory changes a transaction's category. It is used by
// interactive categorization flows such as Telegram inline keyboards.
func (s *Store) UpdateTransactionCategory(ctx context.Context, id int64, category string) error {
	if id <= 0 {
		return ErrNotFound
	}
	category = strings.TrimSpace(category)
	if category == "" {
		category = "Uncategorized"
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE transactions SET category = ?, updated_at = ? WHERE id = ?`,
		category, encodeTime(nowUTC()), id)
	if err != nil {
		return fmt.Errorf("storage: update transaction category: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update transaction category: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTransactionNotes changes a transaction's free-form notes. The FTS5
// update trigger re-indexes the row immediately.
func (s *Store) UpdateTransactionNotes(ctx context.Context, id int64, notes string) error {
	if id <= 0 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE transactions SET notes = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(notes), encodeTime(nowUTC()), id)
	if err != nil {
		return fmt.Errorf("storage: update transaction notes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update transaction notes: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTransactions returns transactions matching the filter, newest first.
func (s *Store) ListTransactions(ctx context.Context, filter TransactionFilter) ([]domain.Transaction, error) {
	where, args := transactionWhere(filter)
	query := `SELECT ` + transactionColumns + ` FROM transactions`
	if len(where) > 0 {
		// #nosec G202 -- where contains only static, developer-defined predicates.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY booking_date DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, normalizeLimit(filter.Limit), max(filter.Offset, 0))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan transaction: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate transactions: %w", err)
	}
	return out, nil
}

// RecentTransactions returns the newest transactions across all accounts.
func (s *Store) RecentTransactions(ctx context.Context, limit int) ([]domain.Transaction, error) {
	return s.ListTransactions(ctx, TransactionFilter{Limit: limit})
}

// RecentBookedTransactions returns the newest confirmed transactions across all
// accounts. Pending transactions are excluded.
func (s *Store) RecentBookedTransactions(ctx context.Context, limit int) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE status = ?
		ORDER BY booking_date DESC, id DESC LIMIT ?`,
		domain.TransactionStatusBooked, normalizeLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: recent booked transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan booked transaction: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate booked transactions: %w", err)
	}
	return out, nil
}

// CategorySpending is the absolute booked spending for one category and
// currency within a period.
type CategorySpending struct {
	Category string
	Currency string
	Minor    int64
}

// SpendingByCategory aggregates booked debits in [from, to) by category and
// currency. Returned amounts are positive.
func (s *Store) SpendingByCategory(ctx context.Context, from, to time.Time) ([]CategorySpending, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(NULLIF(TRIM(category), ''), 'Uncategorized'), currency,
			COALESCE(SUM(-amount_minor), 0)
		FROM transactions
		WHERE amount_minor < 0 AND status = ? AND booking_date >= ? AND booking_date < ?
		GROUP BY 1, 2
		ORDER BY 1 ASC, 2 ASC`,
		domain.TransactionStatusBooked, encodeTime(from), encodeTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: spending by category: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CategorySpending
	for rows.Next() {
		var item CategorySpending
		if err := rows.Scan(&item.Category, &item.Currency, &item.Minor); err != nil {
			return nil, fmt.Errorf("storage: scan category spending: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate category spending: %w", err)
	}
	return out, nil
}

// ListTransactionsBetween returns booked and pending transactions with booking
// dates in [from, to), oldest first. It is used by offline analysis and exports.
func (s *Store) ListTransactionsBetween(ctx context.Context, from, to time.Time) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE booking_date >= ? AND booking_date < ?
		ORDER BY booking_date ASC, id ASC`,
		encodeTime(from), encodeTime(to),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: list transactions between: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan transaction: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate transactions: %w", err)
	}
	return out, nil
}

// CountTransactions returns the total number of stored transactions.
func (s *Store) CountTransactions(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count transactions: %w", err)
	}
	return n, nil
}

// CountTransactionsFiltered returns the number of transactions restricted to
// accountIDs. An empty slice counts every transaction.
func (s *Store) CountTransactionsFiltered(ctx context.Context, accountIDs []string) (int, error) {
	query := `SELECT COUNT(*) FROM transactions`
	var args []any
	if clause, clauseArgs := accountIDsClause(accountIDs); clause != "" {
		query += " WHERE " + clause
		args = append(args, clauseArgs...)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count filtered transactions: %w", err)
	}
	return n, nil
}

// CountTransactionsMatching returns the number of transactions matching the
// account and quick-filter predicates.
func (s *Store) CountTransactionsMatching(ctx context.Context, filter TransactionFilter) (int, error) {
	where, args := transactionWhere(filter)
	query := `SELECT COUNT(*) FROM transactions`
	if len(where) > 0 {
		// #nosec G202 -- where contains only static, developer-defined predicates.
		query += " WHERE " + strings.Join(where, " AND ")
	}
	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count matching transactions: %w", err)
	}
	return n, nil
}

// SpendingByCategoryForAccounts aggregates booked debits in [from, to) by
// category and currency, restricted to accountIDs. An empty slice includes all
// accounts.
func (s *Store) SpendingByCategoryForAccounts(ctx context.Context, from, to time.Time, accountIDs []string) ([]CategorySpending, error) {
	where := []string{"amount_minor < 0", "status = ?", "booking_date >= ?", "booking_date < ?"}
	args := []any{domain.TransactionStatusBooked, encodeTime(from), encodeTime(to)}
	if clause, clauseArgs := accountIDsClause(accountIDs); clause != "" {
		where = append(where, clause)
		args = append(args, clauseArgs...)
	}
	// #nosec G202 -- where contains only static, developer-defined predicates.
	query := `
		SELECT COALESCE(NULLIF(TRIM(category), ''), 'Uncategorized'), currency,
			COALESCE(SUM(-amount_minor), 0)
		FROM transactions
		WHERE ` + strings.Join(where, " AND ") + `
		GROUP BY 1, 2
		ORDER BY 1 ASC, 2 ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: spending by category for accounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CategorySpending
	for rows.Next() {
		var item CategorySpending
		if err := rows.Scan(&item.Category, &item.Currency, &item.Minor); err != nil {
			return nil, fmt.Errorf("storage: scan category spending: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate category spending: %w", err)
	}
	return out, nil
}

// FindDuplicateCandidates looks for booked transactions with the same
// case-insensitive counterparty and exact amount within [from, to], excluding
// the transaction identified by excludeID.
func (s *Store) FindDuplicateCandidates(
	ctx context.Context,
	accountID, counterparty string,
	amountMinor int64,
	from, to time.Time,
	excludeID int64,
) ([]domain.Transaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE account_id = ?
			AND LOWER(TRIM(counterparty)) = LOWER(TRIM(?))
			AND amount_minor = ?
			AND status = ?
			AND booking_date >= ? AND booking_date <= ?
			AND id <> ?
		ORDER BY booking_date DESC, id DESC
		LIMIT 10`,
		accountID, counterparty, amountMinor, domain.TransactionStatusBooked,
		encodeTime(from), encodeTime(to), excludeID,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: find duplicate candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan duplicate candidate: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate duplicate candidates: %w", err)
	}
	return out, nil
}

// DebitStats returns the number of booked debits and their absolute total in
// [from, to], excluding the transaction identified by excludeID. Debits are
// stored as negative amounts.
func (s *Store) DebitStats(ctx context.Context, accountID string, from, to time.Time, excludeID int64) (totalMinor int64, count int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(-amount_minor), 0), COUNT(*)
		FROM transactions
		WHERE account_id = ?
			AND amount_minor < 0
			AND status = ?
			AND booking_date >= ? AND booking_date <= ?
			AND id <> ?`,
		accountID, domain.TransactionStatusBooked,
		encodeTime(from), encodeTime(to), excludeID,
	).Scan(&totalMinor, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("storage: debit stats: %w", err)
	}
	return totalMinor, count, nil
}
