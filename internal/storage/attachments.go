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

const attachmentColumns = `id, transaction_id, filename, file_path, content_type,
	size_bytes, sha256, uploaded_at, source, extracted_date,
	extracted_amount_minor, extracted_currency`

func scanAttachment(row rowScanner) (domain.Attachment, error) {
	var (
		att         domain.Attachment
		transaction sql.NullInt64
		uploadedAt  string
		extracted   sql.NullString
		amountMinor sql.NullInt64
	)
	if err := row.Scan(
		&att.ID, &transaction, &att.Filename, &att.FilePath, &att.ContentType,
		&att.SizeBytes, &att.SHA256, &uploadedAt, &att.Source, &extracted,
		&amountMinor, &att.ExtractedCurrency,
	); err != nil {
		return domain.Attachment{}, err
	}
	if transaction.Valid {
		id := transaction.Int64
		att.TransactionID = &id
	}
	var err error
	if att.UploadedAt, err = decodeTime(uploadedAt); err != nil {
		return domain.Attachment{}, err
	}
	if att.ExtractedDate, err = decodeNullTime(extracted); err != nil {
		return domain.Attachment{}, err
	}
	if amountMinor.Valid {
		id := amountMinor.Int64
		att.ExtractedAmountMinor = &id
	}
	return att, nil
}

// CreateAttachment persists attachment metadata and sets its ID.
func (s *Store) CreateAttachment(ctx context.Context, att *domain.Attachment) error {
	if strings.TrimSpace(att.Filename) == "" {
		return errors.New("storage: attachment filename is required")
	}
	if strings.TrimSpace(att.FilePath) == "" {
		return errors.New("storage: attachment file path is required")
	}
	if att.UploadedAt.IsZero() {
		att.UploadedAt = nowUTC()
	}
	if att.Source == "" {
		att.Source = "web"
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO transaction_attachments (
			transaction_id, filename, file_path, content_type, size_bytes, sha256,
			uploaded_at, source, extracted_date, extracted_amount_minor, extracted_currency
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		att.TransactionID, att.Filename, att.FilePath, att.ContentType, att.SizeBytes,
		att.SHA256, encodeTime(att.UploadedAt), att.Source,
		encodeNullTime(att.ExtractedDate), nullableInt64(att.ExtractedAmountMinor),
		att.ExtractedCurrency,
	)
	if err != nil {
		return fmt.Errorf("storage: create attachment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("storage: create attachment ID: %w", err)
	}
	att.ID = id
	return nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// GetAttachment returns one attachment by ID.
func (s *Store) GetAttachment(ctx context.Context, id int64) (domain.Attachment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+attachmentColumns+` FROM transaction_attachments WHERE id = ?`, id)
	att, err := scanAttachment(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Attachment{}, ErrNotFound
		}
		return domain.Attachment{}, fmt.Errorf("storage: get attachment: %w", err)
	}
	return att, nil
}

// ListUnpairedAttachments returns the receipt inbox, newest first.
func (s *Store) ListUnpairedAttachments(ctx context.Context) ([]domain.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attachmentColumns+` FROM transaction_attachments
		WHERE transaction_id IS NULL
		ORDER BY uploaded_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list unpaired attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanAttachments(rows)
}

// ListAttachmentsForTransaction returns attachments linked to a transaction.
func (s *Store) ListAttachmentsForTransaction(ctx context.Context, transactionID int64) ([]domain.Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+attachmentColumns+` FROM transaction_attachments
		WHERE transaction_id = ?
		ORDER BY uploaded_at DESC, id DESC`, transactionID)
	if err != nil {
		return nil, fmt.Errorf("storage: list transaction attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanAttachments(rows)
}

func scanAttachments(rows *sql.Rows) ([]domain.Attachment, error) {
	var out []domain.Attachment
	for rows.Next() {
		att, err := scanAttachment(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan attachment: %w", err)
		}
		out = append(out, att)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate attachments: %w", err)
	}
	return out, nil
}

// AttachToTransaction links an attachment to a transaction.
func (s *Store) AttachToTransaction(ctx context.Context, attachmentID, transactionID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE transaction_attachments SET transaction_id = ? WHERE id = ?`,
		transactionID, attachmentID)
	if err != nil {
		return fmt.Errorf("storage: attach receipt: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: attach receipt: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AttachmentIDsForTransactions maps each transaction ID to its first
// attachment ID.
func (s *Store) AttachmentIDsForTransactions(ctx context.Context, transactionIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(transactionIDs))
	if len(transactionIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(transactionIDs))
	args := make([]any, len(transactionIDs))
	for i, id := range transactionIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	// #nosec G202 -- placeholders are generated from the input slice length.
	rows, err := s.db.QueryContext(ctx, `
		SELECT transaction_id, MIN(id) FROM transaction_attachments
		WHERE transaction_id IN (`+strings.Join(placeholders, ",")+`)
		GROUP BY transaction_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: list attachment transaction IDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var transactionID, attachmentID int64
		if err := rows.Scan(&transactionID, &attachmentID); err != nil {
			return nil, fmt.Errorf("storage: scan attachment transaction ID: %w", err)
		}
		out[transactionID] = attachmentID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate attachment transaction IDs: %w", err)
	}
	return out, nil
}

// FindPairingCandidates returns booked transactions with an exact amount and
// currency within days of date, nearest first.
func (s *Store) FindPairingCandidates(ctx context.Context, date time.Time, amountMinor int64, currency string, days int) ([]domain.Transaction, error) {
	if days < 0 {
		days = 0
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	from := date.AddDate(0, 0, -days)
	to := date.AddDate(0, 0, days+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+transactionColumns+` FROM transactions
		WHERE status = ? AND amount_minor = ? AND UPPER(currency) = ?
			AND booking_date >= ? AND booking_date < ?
		ORDER BY ABS(julianday(booking_date) - julianday(?)) ASC, id ASC
		LIMIT 10`,
		domain.TransactionStatusBooked, amountMinor, currency,
		encodeTime(from), encodeTime(to), encodeTime(date),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: find receipt candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan receipt candidate: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate receipt candidates: %w", err)
	}
	return out, nil
}

// UnattachedBusinessTransactions returns business-account debits at or above
// thresholdMinor booked on or before before, with no receipt attached.
func (s *Store) UnattachedBusinessTransactions(ctx context.Context, before time.Time, thresholdMinor int64) ([]domain.Transaction, error) {
	if thresholdMinor < 0 {
		thresholdMinor = 0
	}
	// #nosec G202 -- transactionColumns is a static developer-defined column list.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixColumns("t", transactionColumns)+` FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN transaction_attachments att ON att.transaction_id = t.id
		WHERE t.status = ? AND a.type = ? AND t.amount_minor < 0
			AND -t.amount_minor > ? AND t.booking_date <= ? AND att.id IS NULL
		ORDER BY t.booking_date ASC, t.id ASC
		LIMIT 500`,
		domain.TransactionStatusBooked, domain.AccountTypeBusiness,
		thresholdMinor, encodeTime(before),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: unattached business transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Transaction
	for rows.Next() {
		tx, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan unattached transaction: %w", err)
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate unattached transactions: %w", err)
	}
	return out, nil
}

func prefixColumns(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}
