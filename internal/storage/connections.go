package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RedRiotTank/Fiscus/internal/domain"
)

type rowScanner interface {
	Scan(dest ...any) error
}

const connectionColumns = `id, requisition_id, institution_id, institution_name, status, link,
	reference, agreement_id, last_error, consent_expires_at, last_synced_at, created_at, updated_at`

func scanConnection(row rowScanner) (domain.Connection, error) {
	var (
		c          domain.Connection
		expiresAt  sql.NullString
		lastSynced sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := row.Scan(
		&c.ID, &c.RequisitionID, &c.InstitutionID, &c.InstitutionName, &c.Status, &c.Link,
		&c.Reference, &c.AgreementID, &c.LastError, &expiresAt, &lastSynced, &createdAt, &updatedAt,
	); err != nil {
		return domain.Connection{}, err
	}
	var err error
	if c.ConsentExpiresAt, err = decodeNullTime(expiresAt); err != nil {
		return domain.Connection{}, err
	}
	if c.LastSyncedAt, err = decodeNullTime(lastSynced); err != nil {
		return domain.Connection{}, err
	}
	if c.CreatedAt, err = decodeTime(createdAt); err != nil {
		return domain.Connection{}, err
	}
	if c.UpdatedAt, err = decodeTime(updatedAt); err != nil {
		return domain.Connection{}, err
	}
	return c, nil
}

// UpsertConnection inserts or updates a connection identified by its GoCardless
// requisition ID. Sync bookkeeping fields (last_synced_at, last_error) are owned
// by UpdateConnectionSync and are not overwritten here.
func (s *Store) UpsertConnection(ctx context.Context, c *domain.Connection) error {
	if c.RequisitionID == "" {
		return errors.New("storage: connection requisition ID is required")
	}
	now := nowUTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO connections (
			requisition_id, institution_id, institution_name, status, link, reference,
			agreement_id, consent_expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(requisition_id) DO UPDATE SET
			institution_id = excluded.institution_id,
			institution_name = excluded.institution_name,
			status = excluded.status,
			link = excluded.link,
			reference = excluded.reference,
			agreement_id = excluded.agreement_id,
			consent_expires_at = excluded.consent_expires_at,
			updated_at = excluded.updated_at`,
		c.RequisitionID, c.InstitutionID, c.InstitutionName, c.Status, c.Link, c.Reference,
		c.AgreementID, encodeNullTime(c.ConsentExpiresAt), encodeTime(c.CreatedAt), encodeTime(c.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("storage: upsert connection: %w", err)
	}
	if c.ID == 0 {
		if err := s.db.QueryRowContext(ctx,
			`SELECT id FROM connections WHERE requisition_id = ?`, c.RequisitionID,
		).Scan(&c.ID); err != nil {
			return fmt.Errorf("storage: read connection ID: %w", err)
		}
	}
	return nil
}

// GetConnectionByID returns a connection by its local ID.
func (s *Store) GetConnectionByID(ctx context.Context, id int64) (domain.Connection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM connections WHERE id = ?`, id)
	c, err := scanConnection(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Connection{}, ErrNotFound
		}
		return domain.Connection{}, fmt.Errorf("storage: get connection %d: %w", id, err)
	}
	return c, nil
}

// GetConnectionByRequisitionID returns a connection by GoCardless requisition ID.
func (s *Store) GetConnectionByRequisitionID(ctx context.Context, requisitionID string) (domain.Connection, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM connections WHERE requisition_id = ?`, requisitionID)
	c, err := scanConnection(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Connection{}, ErrNotFound
		}
		return domain.Connection{}, fmt.Errorf("storage: get connection %q: %w", requisitionID, err)
	}
	return c, nil
}

// GetConnectionByReference returns the most recent connection with a matching
// consent reference. Enable Banking uses the reference to carry the OAuth state
// while a consent is pending.
func (s *Store) GetConnectionByReference(ctx context.Context, reference string) (domain.Connection, error) {
	if reference == "" {
		return domain.Connection{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM connections
		 WHERE reference = ? ORDER BY id DESC LIMIT 1`, reference)
	c, err := scanConnection(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Connection{}, ErrNotFound
		}
		return domain.Connection{}, fmt.Errorf("storage: get connection by reference: %w", err)
	}
	return c, nil
}

// UpdateConnectionAuthorization replaces the temporary authorization ID with the
// provider session ID and marks the consent as linked.
func (s *Store) UpdateConnectionAuthorization(
	ctx context.Context,
	id int64,
	requisitionID, status, link, institutionID, institutionName, agreementID string,
) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE connections SET
			requisition_id = ?, status = ?, link = ?, institution_id = ?,
			institution_name = ?, agreement_id = ?, reference = '', last_error = '',
			updated_at = ?
		WHERE id = ?`,
		requisitionID, status, link, institutionID, institutionName, agreementID,
		encodeTime(nowUTC()), id,
	)
	if err != nil {
		return fmt.Errorf("storage: update connection authorization: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update connection authorization: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateConnectionConsentExpiry stores the provider-reported consent expiry.
func (s *Store) UpdateConnectionConsentExpiry(ctx context.Context, id int64, expiresAt *time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE connections SET consent_expires_at = ?, updated_at = ? WHERE id = ?`,
		encodeNullTime(expiresAt), encodeTime(nowUTC()), id,
	)
	if err != nil {
		return fmt.Errorf("storage: update connection consent expiry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update connection consent expiry: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListConnections returns all connections ordered by creation time.
func (s *Store) ListConnections(ctx context.Context) ([]domain.Connection, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+connectionColumns+` FROM connections ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("storage: list connections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("storage: scan connection: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate connections: %w", err)
	}
	return out, nil
}

// UpdateConnectionSync records the result of a sync attempt. A non-empty
// syncErr is exposed on the dashboard and cleared by the next successful sync.
func (s *Store) UpdateConnectionSync(ctx context.Context, id int64, syncedAt time.Time, syncErr string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE connections
		SET last_synced_at = ?, last_error = ?, updated_at = ?
		WHERE id = ?`,
		encodeTime(syncedAt), syncErr, encodeTime(nowUTC()), id,
	)
	if err != nil {
		return fmt.Errorf("storage: update connection sync: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: update connection sync: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
