package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type migration struct {
	version    int
	statements []string
}

// migrations is append-only. Never modify a released migration; add a new one.
var migrations = []migration{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS connections (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				requisition_id TEXT NOT NULL UNIQUE,
				institution_id TEXT NOT NULL DEFAULT '',
				institution_name TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				link TEXT NOT NULL DEFAULT '',
				reference TEXT NOT NULL DEFAULT '',
				agreement_id TEXT NOT NULL DEFAULT '',
				last_error TEXT NOT NULL DEFAULT '',
				last_synced_at TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS accounts (
				id TEXT PRIMARY KEY,
				connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
				iban TEXT NOT NULL DEFAULT '',
				name TEXT NOT NULL DEFAULT '',
				owner_name TEXT NOT NULL DEFAULT '',
				currency TEXT NOT NULL DEFAULT 'EUR',
				balance_minor INTEGER NOT NULL DEFAULT 0,
				balance_type TEXT NOT NULL DEFAULT '',
				balance_updated_at TEXT,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_accounts_connection ON accounts(connection_id)`,
			`CREATE TABLE IF NOT EXISTS transactions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
				external_id TEXT NOT NULL,
				amount_minor INTEGER NOT NULL,
				currency TEXT NOT NULL,
				booking_date TEXT NOT NULL,
				value_date TEXT,
				counterparty TEXT NOT NULL DEFAULT '',
				description TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'booked',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(account_id, external_id)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_transactions_account_date ON transactions(account_id, booking_date DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(booking_date DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_transactions_duplicate ON transactions(account_id, amount_minor, booking_date)`,
			`CREATE INDEX IF NOT EXISTS idx_transactions_counterparty ON transactions(account_id, counterparty, booking_date)`,
			`CREATE TABLE IF NOT EXISTS alerts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				rule TEXT NOT NULL,
				severity TEXT NOT NULL,
				title TEXT NOT NULL,
				message TEXT NOT NULL,
				account_id TEXT NOT NULL DEFAULT '',
				transaction_id INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
				dedup_key TEXT NOT NULL UNIQUE,
				created_at TEXT NOT NULL,
				delivered_at TEXT,
				delivery_error TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_alerts_created ON alerts(created_at DESC)`,
		},
	},
	{
		version: 2,
		statements: []string{
			`CREATE INDEX IF NOT EXISTS idx_connections_reference ON connections(reference)`,
		},
	},
	{
		version: 3,
		statements: []string{
			`ALTER TABLE connections ADD COLUMN consent_expires_at TEXT`,
		},
	},
	{
		version: 4,
		statements: []string{
			`ALTER TABLE transactions ADD COLUMN category TEXT NOT NULL DEFAULT 'Uncategorized'`,
		},
	},
}

// Migrate applies all pending migrations. It is safe to call multiple times and
// from a fresh database.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("storage: create migrations table: %w", err)
	}

	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	latest := 0
	for _, m := range migrations {
		if m.version > latest {
			latest = m.version
		}
	}
	if current > latest {
		return fmt.Errorf("storage: database schema version %d is newer than supported version %d", current, latest)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin migration %d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, stmt := range m.statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migration %d statement %d: %w", m.version, i+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, encodeTime(nowUTC()),
	); err != nil {
		return fmt.Errorf("storage: record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit migration %d: %w", m.version, err)
	}
	return nil
}

// SchemaVersion returns the highest applied migration, or 0 for a fresh DB.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("storage: inspect schema: %w", err)
	}
	if exists == 0 {
		return 0, nil
	}
	var version sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("storage: read schema version: %w", err)
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}
