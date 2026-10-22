# Changelog

All notable changes to Fiscus are documented here. Versions use semantic
versioning and annotated Git tags.

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
