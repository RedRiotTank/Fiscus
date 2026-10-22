# Changelog

All notable changes to Fiscus are documented here. Versions use semantic
versioning and annotated Git tags.

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
