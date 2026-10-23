# Changelog

All notable changes to Fiscus are documented here. Versions use semantic
versioning and annotated Git tags.

## v1.1.0 - 2026-10-24

Usability and workflow release focused on receipt recovery, transaction
annotations, runtime workspace mapping, and search ergonomics.

### Added

- Receipt inbox re-pairing:
  - Every successful bank sync retries exact amount/currency/±3-day matching
    for all unpaired receipts.
  - A **Re-pair inbox** HTMX action runs the same pass on demand.
  - Ambiguous matches remain unpaired for manual linking.
- Transaction notes:
  - Inline notes editor on the Transactions page via
    `PATCH /api/transactions/{id}/notes`.
  - Telegram `/nota <transaction_id> <texto>` for mobile capture.
  - Notes are immediately visible to FTS5 through the existing update trigger.
- Dynamic workspace mapping:
  - Personal/Business selector on the Connections page and
    `POST /api/accounts/{id}/type`.
  - Explicit UI/API mappings are persisted in SQLite and protected from later
    config-driven overwrites or restarts.
- Search and scenario refinements:
  - 1-based `page` and `limit` pagination for FTS5 transaction search.
  - Locale-tolerant amount filters for dot decimals, comma decimals, and grouped
    thousands.
  - Scenario simulator yield now follows the projection yield slider instead of
    a fixed 3%.

### Changed

- The latest database schema version is 11.
- README documents re-pairing, notes, runtime workspace overrides, search
  pagination, and localized amount filters.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.1.0'" ./cmd/fiscus`

## v1.0.1 - 2026-10-24

Integrity and security patch for the v1.0.0 treasury platform.

### Fixed

- Balance reconciliation now compares the booked transaction ledger against
  `closingBooked` or `interimBooked` balances. When an ASPSP only exposes an
  available balance, pending debits and credits are added back to avoid
  persistent false positives from card holds.
- Discrepancy alerts are suppressed until the initial reconciliation baseline
  has recorded transaction history.
- Receipt downloads always use an attachment disposition with a sanitized
  filename and an allowlisted PDF/JPEG/PNG/WEBP content type; anything else
  falls back to `application/octet-stream`.
- Receipt upload and pairing endpoints now require the `X-Fiscus-Request: 1`
  header used by other state-changing dashboard actions.
- `fiscus backup` and scheduled Telegram backups now produce a single `.tar.gz`
  archive containing the online SQLite snapshot (`fiscus.db`) and the complete
  `attachments/` directory.

### Changed

- The latest database schema version is 10.
- README and configuration examples describe the new archive format and restore
  procedure.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.0.1'" ./cmd/fiscus`

## v1.0.0 - 2026-10-23

Fiscus v1.0.0 is the unified personal and business treasury intelligence
platform release. It adds six audit-oriented modules while keeping a single pure
Go, CGO-free static binary.

### Added

- Workspace profiles:
  - `personal`, `business`, and `hybrid` profiles.
  - Locked single-workspace views or an All / Personal / Business switcher.
  - Per-account workspace mapping with `personal` as the unmapped default.
  - Search, analytics, exports, alerts, and dashboards respect the active view.
- Balance integrity watchdog:
  - Opening-balance anchors established on the first successful check.
  - Mathematical reconciliation of observed balance versus ledger movement.
  - Discrepancy alerts that only fire when account state or delta changes.
  - Overview reconciliation counts and account-level discrepancy details.
- Receipt and invoice pairing:
  - Authenticated uploads from the Transactions page and Telegram documents or
    photos.
  - File-integrity storage with SHA-256 metadata and safe download handling.
  - Exact absolute-amount, currency, and ±3-day auto-pairing; ambiguous matches
    remain unpaired for manual linking.
  - Receipt inbox and one missing-receipt alert per business debit after the
    configured grace period.
- Tax liquidity shield:
  - Quarterly VAT and income-tax estimation from booked business transactions.
  - Per-currency reserves, no cross-currency aggregation.
  - True liquid operating capital (`balance - tax reserve`) on the dashboard.
  - Quarter-end reserve alerts de-duplicated per quarter and currency.
- FTS5 transaction search:
  - Migration to SQLite FTS5 with triggers synced on transaction and account-type
    changes; notes added to transactions.
  - Prefix search across description, counterparty, category, notes, and
    workspace type.
  - Structured `amount:`, `category:`, `status:`, `has:`, and `type:` filters.
  - Search UI on the Transactions page and Telegram `/buscar`.
- Cash-flow scenario simulator:
  - `POST /api/analytics/scenarios/simulate` with monthly income/expense deltas,
    one-off events, configurable event month, yield, and 1–180 month horizons.
  - Analytics drawer with baseline-versus-simulated chart overlay and ending
    balance/net-change summary.
  - Shared integer minor-unit math with the existing projection engine.

### Changed

- Version metadata is now `v1.0.0`.
- The dashboard navigation, overview cards, analytics workspace, Transactions
  page, and Telegram help include the new modules.
- README covers workspace profiles, reconciliation, receipts, tax estimation,
  search syntax, the scenario API, and the expanded alert rules.
- Configuration examples and environment variables document the `attachments`
  and `tax` sections. Strict YAML validation covers both.
- New migrations are append-only; the latest schema version is 9.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.0.0'" ./cmd/fiscus`

## v0.3.0 - 2026-10-22

### Added

- Retroactive categorization:
  - `fiscus recategorize` previews proposed category changes by default.
  - `--force` applies changes in a single storage transaction; `--dry-run`
    remains an explicit preview and conflicts with `--force`.
  - `--suggest` prints quoted, case-insensitive YAML rules for Uncategorized
    counterparties seen at least three times.
  - The Transactions dashboard page exposes the same preview/apply action.
- Monthly category budgets:
  - Integer minor-unit limits with configurable warning thresholds.
  - A critical 100% threshold is always included.
  - Pacing alerts report the highest newly crossed threshold and de-duplicate per
    threshold, category, and calendar month.
  - Overview dashboard progress bars for month-to-date consumption.
- Telegram inline actions and commands:
  - Booked Uncategorized transactions can be categorized with an inline keyboard
    of up to five configured categories plus Ignore.
  - Callback taps update the stored category and answer the callback.
  - `/runway` reports liquid balance, monthly burn, and runway months.
  - `/presupuesto` reports current-month budget consumption.
- Operational monitoring:
  - Expected monthly inflows with configurable amount, currency, day window, and
    grace period; missing credits produce one alert per month.
  - Ghost subscription detection marks monthly subscriptions with no charge for
    more than 45 days and yearly subscriptions with no charge for more than 390
    days as Inactive / Potential Ghost and notifies once.
- Resilience and integrations:
  - `fiscus backup` writes consistent gzipped online SQLite snapshots while the
    daemon keeps running.
  - Optional weekly Telegram delivery of the generated backup document.
  - Generic outbound webhooks for `transaction.created`, `alert.triggered`, and
    `sync.completed`.
  - Asynchronous delivery with exponential-backoff retries and optional
    HMAC-SHA256 request signing.

### Changed

- The strict YAML configuration schema now covers `budgets`, `expected_inflows`,
  `backups`, and `webhooks`; unknown keys continue to fail fast.
- The dashboard overview includes budget pacing, and the Subscriptions & Fees
  page labels stale recurring charges as Inactive / Potential Ghost.
- README documentation now covers the new CLI commands, monitoring rules,
  Telegram behavior, backups, and webhook signatures.
- `fiscus version` now reports `v0.3.0` for release builds.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`

## v0.2.0 - 2026-10-22

### Added

- Spending analytics API and dashboard charts:
  - Category shares with month, quarter, and year-to-date filters.
  - Six-month income-versus-expense trend.
  - Current-month cash-flow waterfall from opening to closing balance.
  - Internal transfers between owned accounts are excluded.
- Burn-up spending pace:
  - Current month-to-date cumulative curve.
  - Six historical baseline curves and their average.
  - Above/below/near-normal velocity indicator.
- Spending activity matrix:
  - Weekday by amount-bracket transaction frequency for the last 90 days.
- Wealth and savings projections:
  - Historical monthly net-savings mean and sample standard deviation.
  - 1, 5, 10, and 15-year median projections with a variance cone.
  - Live extra-savings and annual-yield scenario controls.
- Multi-account liquidity distribution:
  - Reconstructed daily balances grouped by institution.
  - Stacked area chart for 90-day and 180-day windows.
- Locally bundled Chart.js 4.4.7 and its license in the embedded asset bundle.
  Charts make no CDN or third-party requests at runtime.

### Changed

- The dashboard now links to an Analytics workspace and includes spending
  charts alongside the treasury overview.
- Added a compiled analytics package with integer minor-unit money handling and
  internal-transfer-aware calculations.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
