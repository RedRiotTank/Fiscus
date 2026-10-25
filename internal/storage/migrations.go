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
	{
		version: 5,
		statements: []string{
			`ALTER TABLE connections ADD COLUMN rate_limited_until TEXT`,
			`ALTER TABLE connections ADD COLUMN last_rate_limit_reason TEXT`,
			`ALTER TABLE accounts ADD COLUMN rate_limited_until TEXT`,
			`ALTER TABLE accounts ADD COLUMN last_rate_limit_reason TEXT`,
		},
	},
	{
		version: 6,
		statements: []string{
			`ALTER TABLE accounts ADD COLUMN type TEXT NOT NULL DEFAULT 'personal'`,
			`CREATE INDEX IF NOT EXISTS idx_accounts_type ON accounts(type)`,
		},
	},
	{
		version: 7,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS account_reconciliation (
				account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
				currency TEXT NOT NULL DEFAULT 'EUR',
				opening_balance_minor INTEGER NOT NULL,
				anchor_at TEXT NOT NULL,
				last_checked_at TEXT,
				last_delta_minor INTEGER NOT NULL DEFAULT 0,
				status TEXT NOT NULL DEFAULT 'reconciled',
				discrepancy_start TEXT,
				discrepancy_end TEXT,
				updated_at TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS idx_reconciliation_status ON account_reconciliation(status)`,
		},
	},
	{
		version: 8,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS transaction_attachments (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				transaction_id INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
				filename TEXT NOT NULL,
				file_path TEXT NOT NULL UNIQUE,
				content_type TEXT NOT NULL DEFAULT '',
				size_bytes INTEGER NOT NULL DEFAULT 0,
				sha256 TEXT NOT NULL DEFAULT '',
				uploaded_at TEXT NOT NULL,
				source TEXT NOT NULL DEFAULT 'web',
				extracted_date TEXT,
				extracted_amount_minor INTEGER,
				extracted_currency TEXT NOT NULL DEFAULT ''
			)`,
			`CREATE INDEX IF NOT EXISTS idx_attachments_transaction ON transaction_attachments(transaction_id)`,
			`CREATE INDEX IF NOT EXISTS idx_attachments_uploaded ON transaction_attachments(uploaded_at)`,
		},
	},
	{
		version: 9,
		statements: []string{
			`ALTER TABLE transactions ADD COLUMN notes TEXT NOT NULL DEFAULT ''`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS transactions_fts USING fts5(
				transaction_id UNINDEXED,
				description,
				counterparty,
				category,
				notes,
				account_type,
				tokenize='unicode61'
			)`,
			`INSERT INTO transactions_fts (rowid, transaction_id, description, counterparty, category, notes, account_type)
			 SELECT t.id, t.id, t.description, t.counterparty, t.category, t.notes, COALESCE(a.type, 'personal')
			 FROM transactions t LEFT JOIN accounts a ON a.id = t.account_id`,
			`CREATE TRIGGER IF NOT EXISTS transactions_fts_ai AFTER INSERT ON transactions BEGIN
				INSERT INTO transactions_fts (rowid, transaction_id, description, counterparty, category, notes, account_type)
				SELECT new.id, new.id, new.description, new.counterparty, new.category, new.notes,
					COALESCE((SELECT type FROM accounts WHERE id = new.account_id), 'personal');
			END`,
			`CREATE TRIGGER IF NOT EXISTS transactions_fts_au AFTER UPDATE ON transactions BEGIN
				DELETE FROM transactions_fts WHERE rowid = old.id;
				INSERT INTO transactions_fts (rowid, transaction_id, description, counterparty, category, notes, account_type)
				SELECT new.id, new.id, new.description, new.counterparty, new.category, new.notes,
					COALESCE((SELECT type FROM accounts WHERE id = new.account_id), 'personal');
			END`,
			`CREATE TRIGGER IF NOT EXISTS transactions_fts_ad AFTER DELETE ON transactions BEGIN
				DELETE FROM transactions_fts WHERE rowid = old.id;
			END`,
			`CREATE TRIGGER IF NOT EXISTS accounts_fts_type_au AFTER UPDATE OF type ON accounts BEGIN
				DELETE FROM transactions_fts WHERE transaction_id IN (SELECT id FROM transactions WHERE account_id = new.id);
				INSERT INTO transactions_fts (rowid, transaction_id, description, counterparty, category, notes, account_type)
				SELECT t.id, t.id, t.description, t.counterparty, t.category, t.notes, COALESCE(new.type, 'personal')
				FROM transactions t WHERE t.account_id = new.id;
			END`,
		},
	},
	{
		version: 10,
		statements: []string{
			// Existing reconciliation rows have already completed their setup
			// baseline. New rows start at 0 and are promoted after the first
			// pass with booked history.
			`ALTER TABLE account_reconciliation ADD COLUMN baseline_established INTEGER NOT NULL DEFAULT 1`,
		},
	},
	{
		version: 11,
		statements: []string{
			`ALTER TABLE accounts ADD COLUMN type_manual INTEGER NOT NULL DEFAULT 0`,
		},
	},
	{
		version: 12,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS budget_overrides (
				category TEXT NOT NULL COLLATE NOCASE,
				currency TEXT NOT NULL DEFAULT 'EUR',
				limit_minor INTEGER NOT NULL,
				notify_at_percent TEXT NOT NULL DEFAULT '',
				updated_at TEXT NOT NULL,
				PRIMARY KEY (category, currency)
			)`,
			`CREATE TABLE IF NOT EXISTS app_settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
		},
	},
	{
		version: 13,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS budget_profiles (
				workspace TEXT NOT NULL,
				currency TEXT NOT NULL,
				needs_bps INTEGER NOT NULL DEFAULT 5000,
				wants_bps INTEGER NOT NULL DEFAULT 3000,
				savings_bps INTEGER NOT NULL DEFAULT 2000,
				advisory_enabled INTEGER NOT NULL DEFAULT 1,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (workspace, currency)
			)`,
			`CREATE TABLE IF NOT EXISTS budget_allocations (
				workspace TEXT NOT NULL,
				currency TEXT NOT NULL,
				category TEXT NOT NULL COLLATE NOCASE,
				pillar TEXT NOT NULL,
				percent_bps INTEGER NOT NULL,
				notify_at_percent TEXT NOT NULL DEFAULT '',
				sort_order INTEGER NOT NULL DEFAULT 0,
				custom INTEGER NOT NULL DEFAULT 0,
				updated_at TEXT NOT NULL,
				PRIMARY KEY (workspace, currency, category)
			)`,
		},
	},
	{
		version: 14,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS learned_rules (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				pattern TEXT NOT NULL COLLATE NOCASE UNIQUE,
				category TEXT NOT NULL,
				source TEXT NOT NULL DEFAULT 'inline',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL
			)`,
		},
	},
	{
		version: 15,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS housing_rules (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace TEXT NOT NULL DEFAULT 'personal',
				currency TEXT NOT NULL DEFAULT 'EUR',
				landlord_counterparty TEXT NOT NULL,
				personal_net_share_cents INTEGER NOT NULL DEFAULT 0,
				calculation_mode TEXT NOT NULL DEFAULT 'net_balance',
				flatmate_counterparties TEXT NOT NULL DEFAULT '[]',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(workspace, currency)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_housing_rules_workspace ON housing_rules(workspace, currency)`,
		},
	},
	{
		version: 16,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS recurring_bill_rules (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				workspace TEXT NOT NULL DEFAULT 'personal',
				currency TEXT NOT NULL DEFAULT 'EUR',
				counterparty TEXT NOT NULL COLLATE NOCASE,
				category TEXT NOT NULL DEFAULT 'Utilities & Telecom',
				pillar TEXT NOT NULL DEFAULT 'needs',
				split_mode TEXT NOT NULL DEFAULT 'solo',
				split_people INTEGER NOT NULL DEFAULT 1,
				gross_minor INTEGER NOT NULL DEFAULT 0,
				personal_share_minor INTEGER NOT NULL DEFAULT 0,
				active INTEGER NOT NULL DEFAULT 1,
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(workspace, currency, counterparty)
			)`,
			`CREATE INDEX IF NOT EXISTS idx_recurring_bill_rules_workspace ON recurring_bill_rules(workspace, currency)`,
		},
	},
	{
		version: 17,
		statements: []string{
			`ALTER TABLE recurring_bill_rules ADD COLUMN recurrence TEXT NOT NULL DEFAULT 'monthly'`,
		},
	},
	{
		version: 18,
		statements: []string{
			// The housing setup used to install counterparty-wide rules for every
			// flatmate. Those rules reclassified micro-transfers as Housing / Rent.
			`DELETE FROM learned_rules
			 WHERE category = 'Housing / Rent'
			   AND source LIKE 'housing:%'`,
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
