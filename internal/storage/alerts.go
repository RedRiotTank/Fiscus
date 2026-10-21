package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

const alertColumns = `id, rule, severity, title, message, account_id, transaction_id,
	dedup_key, created_at, delivered_at, delivery_error`

func scanAlert(row rowScanner) (domain.Alert, error) {
	var (
		a           domain.Alert
		txID        sql.NullInt64
		createdAt   string
		deliveredAt sql.NullString
	)
	if err := row.Scan(
		&a.ID, &a.Rule, &a.Severity, &a.Title, &a.Message, &a.AccountID, &txID,
		&a.DedupKey, &createdAt, &deliveredAt, &a.DeliveryError,
	); err != nil {
		return domain.Alert{}, err
	}
	if txID.Valid {
		id := txID.Int64
		a.TransactionID = &id
	}
	var err error
	if a.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.Alert{}, err
	}
	if a.DeliveredAt, err = decodeNullTime(deliveredAt); err != nil {
		return domain.Alert{}, err
	}
	return a, nil
}

// CreateAlert persists an alert. It returns false when the dedup key already
// exists, in which case no notification should be sent.
func (s *Store) CreateAlert(ctx context.Context, a *domain.Alert) (bool, error) {
	if a.DedupKey == "" {
		return false, errors.New("storage: alert dedup key is required")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = nowUTC()
	}
	if a.Severity == "" {
		a.Severity = domain.SeverityInfo
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts (rule, severity, title, message, account_id, transaction_id,
			dedup_key, created_at, delivered_at, delivery_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dedup_key) DO NOTHING`,
		a.Rule, a.Severity, a.Title, a.Message, a.AccountID, a.TransactionID,
		a.DedupKey, encodeTime(a.CreatedAt), encodeNullTime(a.DeliveredAt), a.DeliveryError,
	)
	if err != nil {
		return false, fmt.Errorf("storage: create alert: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("storage: create alert: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false, fmt.Errorf("storage: alert ID: %w", err)
	}
	a.ID = id
	return true, nil
}

// MarkAlertDelivered records successful delivery to at least one channel.
func (s *Store) MarkAlertDelivered(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET delivered_at = ?, delivery_error = '' WHERE id = ?`,
		encodeTime(at), id)
	if err != nil {
		return fmt.Errorf("storage: mark alert delivered: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: mark alert delivered: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAlertDeliveryFailed records the last delivery error, keeping the alert
// undelivered so the dashboard can surface the failure.
func (s *Store) MarkAlertDeliveryFailed(ctx context.Context, id int64, deliveryErr string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET delivery_error = ?, delivered_at = NULL WHERE id = ?`,
		deliveryErr, id)
	if err != nil {
		return fmt.Errorf("storage: mark alert delivery failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: mark alert delivery failed: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAlert returns one alert by ID.
func (s *Store) GetAlert(ctx context.Context, id int64) (domain.Alert, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+alertColumns+` FROM alerts WHERE id = ?`, id)
	a, err := scanAlert(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Alert{}, ErrNotFound
		}
		return domain.Alert{}, fmt.Errorf("storage: get alert: %w", err)
	}
	return a, nil
}

// ListAlerts returns the newest alerts first.
func (s *Store) ListAlerts(ctx context.Context, limit int) ([]domain.Alert, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+alertColumns+` FROM alerts ORDER BY created_at DESC, id DESC LIMIT ?`,
		normalizeLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("storage: list alerts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan alert: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate alerts: %w", err)
	}
	return out, nil
}

// CountAlerts returns the total number of persisted alerts.
func (s *Store) CountAlerts(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alerts`).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count alerts: %w", err)
	}
	return n, nil
}
