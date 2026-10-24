# Changelog

All notable changes to Fiscus are documented here. Versions use semantic
versioning and annotated Git tags.

## v1.4.0 - 2026-10-25

Income-driven budget allocation and deep advisory release.

### Added

- Income-driven allocation engine:
  - Monthly budget baseline from confirmed recurring net inflows. Personal uses
    net income; Business uses operating revenue with a planned tax reserve.
  - Needs, Wants, and Savings & Investment pillars with configurable target
    percentages.
  - Category allocations expressed as percentages of monthly income and
    converted to category caps.
  - Deep normative sub-caps for housing, groceries, utilities/telecom, dining,
    lifestyle spending, and savings.
  - Contextual warning and positive-reinforcement messages.
  - Overallocated, unallocated, and over-target pillar detection.
- Unified Budgets page:
  - Removed the separate Budget Advisor card and destructive “Apply Recommended
    Budgets” action.
  - Added a total monthly income/operating revenue banner.
  - Added an Advisory Active/Paused toggle persisted per workspace.
  - Added a Customize Allocation drawer for pillar targets, category
    assignments, category percentages, and custom category creation/deletion.
  - Added Needs, Wants, and Savings pillar cards with target vs allocated
    amounts, actual spend, progress bars, and health badges.
  - Dual-ring donut now shows planned income allocation plus unallocated buffer,
    with actual spending on the inner ring.
- Workspace isolation:
  - Personal and Business allocation profiles and categories are completely
    separate.
  - The All-workspaces view asks the operator to choose Personal or Business
    instead of mixing categories.
  - Empty workspaces render a clean “No budget configured” setup card and never
    draw a 0% donut.
- Persistence:
  - Added SQLite `budget_profiles` and `budget_allocations` tables.
  - Latest schema version is 13.
- Demo overhaul:
  - Personal income baseline €2,800 with Rent €850, Groceries €350,
    Utilities €200, Restaurants €300, Subscriptions €90, Leisure & Shopping
    €450, Emergency Fund €300, and Investments €260.
  - Business operating revenue €5,500 with Tax Reserve €1,155, Software & Cloud
    €450, Gestoría / Legal €200, and Office & Equipment €300.
  - Personal and business dashboards now use completely distinct categories.

### Changed

- Subscription detection excludes savings, investments, transfers, tax reserve,
  office/equipment, and leisure/shopping budget categories.
- README and configuration examples document income-driven allocations and the
  new customization endpoints.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
- Demo screenshots verified Personal, Business, and All-workspace budget views,
  including the customized donut, workspace isolation, empty state, and advisory
  messages.

## v1.3.3 - 2026-10-25

Visual polish, budget visualization, and transaction ergonomics release.

### Added

- Concentric dual-ring budget donut:
  - Outer ring shows the planned category budget allocation.
  - Inner ring shows month-to-date spending aligned radially by category.
  - Overspent categories render in amber, with deeper red for significant
    overspend.
  - Center stat shows overall month-to-date consumption and total spend versus
    total budget.
  - Tooltips show category, spent amount, budget amount, and percentage.
  - A category legend includes color swatches, amounts, and per-category
    percentage badges.
- Deterministic category styling:
  - Known categories use semantic color pairs for groceries, utilities, dining,
    software, housing, health, shopping, travel, insurance, fees, income,
    savings, and professional services.
  - Unknown categories fall back to a deterministic curated palette.
  - Category badges include a color-matched prefix dot for faster scanning.
- Responsive transaction history:
  - Fixed proportional column widths remove horizontal scrolling on desktop.
  - Description and counterparty are combined into a single transaction column
    with a muted secondary line.
  - Long descriptions truncate with ellipsis and expose the full text through a
    native hover title.
  - Account names also expose hover titles and truncate cleanly.
- Spacing and localization:
  - Added separation between transaction history and the receipt inbox.
  - Added separation between the Budget Advisor and Budgets panels.
  - Replaced the Spanish “Caja limpia” badge with **Net Operating Liquidity**
    and standardized the row label to **True liquid capital**.
  - Updated the tax alert wording to match.
- Branding:
  - Header and favicon now use the uploaded Fiscus logo assets.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
- Demo screenshot verification confirmed the dual-ring donut, colorful category
  badges, full-width transaction table, hover-title truncation, section spacing,
  and English tax-shield terminology.

## v1.3.2 - 2026-10-24

Subscription icon transparency fix.

### Fixed

- Removed the white/gray wrapper background and border behind official
  subscription brand icons.
- Brand icon containers now use a transparent background with only light
  padding for breathing room.
- Kept the styled, colored initials avatar exclusively for fallback merchants.
- Removed the Basic-Fit canvas background rectangle and the Adobe clip-path
  white fill from the embedded SVG catalog.
- Adjusted dark monochrome brand marks and the generic briefcase icon so they
  remain visible on the dark card surface without a container background.
- Removed lazy loading from brand icons so logos render immediately on the
  subscription grid.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
- Demo screenshot verification confirmed transparent icons for Vodafone, Apple,
  Cloudflare, GitHub Copilot, Stripe, Spotify, Netflix, AWS, JetBrains,
  Namecheap, Adobe, and the briefcase override, with no gray square artifacts.

## v1.3.1 - 2026-10-24

Official subscription brand icon release.

### Added

- Embedded, offline brand SVG catalog:
  - Streaming and entertainment brands including Netflix, Spotify, Disney+,
    YouTube, Google, Apple, HBO Max, and Prime Video.
  - Cloud and technology brands including AWS, GitHub, Cloudflare, OpenAI,
    JetBrains, Vercel, DigitalOcean, Hetzner, and Heroku.
  - Telecom and utility brands including Vodafone, Movistar, Orange, Endesa,
    and Iberdrola.
  - Productivity and software brands including Adobe, Microsoft, Slack,
    Notion, 1Password, Google Workspace, Figma, Namecheap, and Stripe.
  - Fitness and miscellaneous brands including Basic-Fit, Strava, and Audible,
    plus a generic briefcase icon.
- Pure Go resolver in `pkg/icons`:
  - Case-, accent-, and punctuation-insensitive merchant normalization.
  - Specific substring matching for merchant descriptions such as
    `SPOTIFY AB`, `AMAZON WEB SERVICES`, and `GITHUB SPONSORS`.
  - Configurable `subscriptions.icon_overrides` merchant-to-icon mappings.
  - Safe handling of bundled slugs and `/static/icons/brands/...` paths.
  - Automatic fallback to initials for unknown, missing, or invalid icons.
- Subscription cards:
  - Active and dormant cards render official brand icons in a consistent
    44x44 rounded container.
  - Initials fallback keeps the exact same dimensions and layout.
  - Icons are served locally from `GET /static/icons/brands/{name}.svg`.
  - CSP remains compatible through the existing `img-src 'self' data:` policy.
- Demo and privacy:
  - All seeded demo subscriptions resolve to brand icons, including a
    briefcase override for the fiscal advisor.
  - README and privacy documentation describe the offline, embedded catalog.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
- Demo smoke test verified brand SVG URLs, initials fallback, SVG content type,
  and CSP headers on `/subscriptions`.

## v1.3.0 - 2026-10-24

Intelligent budget advisory release.

### Added

- Budget Advisory Engine:
  - 50/30/20 normative model, historical heuristic model, and hybrid mode.
  - Rolling net operational inflow baseline from confirmed booked credits,
    excluding internal transfers and loans.
  - Estimated tax reserve provisions are deducted in business and hybrid
    profiles.
  - Category mapping into Needs, Wants, Savings, and Unallocated buckets.
  - Historical monthly mean and standard deviation per category.
  - Conservative, realistic, normative, and recommended category ceilings.
  - Over-allocation warnings and under-budget surplus notices.
- Budgets workspace:
  - Dedicated `/budgets` page and Budget Advisor card with 50/30/20 bars.
  - Active/paused toggle persisted in SQLite.
  - One-click **Apply Recommended Budgets** action for existing category caps.
  - Per-category current budget, historical average, recommendation, and status.
- CLI:
  - `fiscus budgets recommend`
  - `fiscus budgets recommend --model=normative|heuristic|hybrid`
  - `fiscus budgets recommend --apply`
- Telegram:
  - `/recomendar_presupuestos` executive advisory summary with net income,
    50/30/20 capacities, over-allocation warnings, and surplus notices.
- Config:
  - `budgets.items` and `budgets.advisory` mapping.
  - Legacy plain-list budget configurations remain supported.
  - Advisory settings for model, split percentages, window, and thresholds.
- Persistence:
  - SQLite `budget_overrides` and `app_settings` tables.
  - Latest schema version is 12.
- Demo data:
  - Restaurants budget 450.00 EUR vs a 280.00 EUR recommendation.
  - Groceries kept in a healthy balance.
  - Personal advisory shows an approximately 180.00 EUR Wants surplus.
  - Dedicated `/budgets` view populated with advisory signals.

### Changed

- Budget threshold alerts read the effective budgets, including SQLite
  overrides.
- Telegram `/presupuesto` reads effective budgets from the same manager.
- Subscription-style icon/card styling and budget advisor cards share the
  refreshed dashboard visual language.
- README and configuration examples cover the advisory models, CLI, API, and
  Telegram command.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -v ./cmd/fiscus`
- Demo smoke test verified:
  - `/budgets` renders the advisor card.
  - Personal advisory reports 2,800 EUR income, 280 EUR Restaurants
    recommendation, over-allocation warning, healthy Groceries, and ~180 EUR
    Wants surplus.
  - Toggle and apply endpoints persist settings and overrides.
  - CLI recommend and apply flows work against the demo database.

## v1.2.2 - 2026-10-24

Demo UI polish, analytics reliability, and subscription experience release.

### Fixed

- Version labels are normalized before rendering, so the dashboard shows
  `v1.2.2` rather than `vv1.2.2`.
- Analytics charts no longer remain stuck on loading:
  - Analytics initializers now wait for `DOMContentLoaded`, after every deferred
    analytics module has registered.
  - Late-registered initializers run immediately.
  - Fetch and Chart.js failures are logged and replace loading placeholders
    with clear unavailable/error messages.
  - Chart.js load failures are surfaced instead of leaving the page waiting.
- The redundant "Open analytics workspace" link is hidden on the Analytics page.
- The demo seeder now associates the Revolut emergency fund with a separate
  Revolut Ltd connection instead of Santander.
- Demo subscription data excludes recurring groceries, restaurants, rent,
  shopping, travel, meals, and other non-subscription categories.
- Demo merchant names no longer create accidental recurring “subscriptions”
  from taverns, cafes, or grocery stores.
- Price-increase detection now finds the most recent actual price change even
  after several months at the new price, and the alert de-duplicates against the
  change date rather than the latest charge date.
- Subscription view spacing now separates summary cards, subscription cards,
  the audit table, and bank fees consistently.

### Added

- Modern active-subscription cards with brand-colored service avatars,
  category badges, price and cadence, price-hike indicators, status pills, and
  next/last charge dates.
- A separate dormant / potential-ghost card section.
- A collapsible recurring-transaction audit table.
- Seeded demo subscriptions now include Spotify, Netflix, GitHub Copilot, AWS,
  JetBrains, Cloudflare, Vodafone, Gestoría Fiscal, and dormant Adobe.

### Changed

- Content-Security-Policy now permits inline scripts/styles and data-image URIs
  while keeping scripts, styles, and fetch/XHR restricted to the same origin.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.2.2'" ./cmd/fiscus`
- Demo smoke test verified version rendering, analytics APIs, subscriptions
  cards, ghost-state cards, CSP headers, and removal of non-subscription
  merchants.

## v1.2.1 - 2026-10-24

Interactive demo and zero-configuration evaluation release.

### Added

- `fiscus demo` command and `fiscus serve --demo` mode.
  - Creates an isolated temporary SQLite database and runs all migrations.
  - Seeds six months of coherent personal and business data across five
    accounts and three institutions.
  - Starts the dashboard without a bank connection, Telegram, Discord,
    webhooks, or scheduled backups.
  - Prints the demo URL and database path, and shuts down cleanly on
    SIGINT/SIGTERM.
- Demo controls:
  - `--dry-run` seeds and exits without starting the server.
  - `--dir` keeps the demo database and attachments on disk.
  - `--address` overrides the listen address.
- Realistic seed coverage:
  - Personal checking, emergency savings, business treasury, and Wise USD/GBP
    accounts.
  - Payroll, client invoices, monthly subscriptions, a Spotify price increase,
    dormant Adobe subscription, groceries, utilities, restaurants, software,
    rent, bank fees, internal transfers, pending card holds, and recurring
    Uncategorized merchants.
  - Paired AWS, MacBook, and JetBrains receipts plus an unpaired hotel receipt.
  - Restaurants and groceries budgets at approximately 85% and 60% utilization.
  - Business tax shield with granular category rates and realistic runway.
  - Seeded alerts for price increases, fees, budget warnings, reconciliation,
    historical discrepancies, and missing receipts.
- README "Quick Demo in 10 Seconds" badge and instructions.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.2.1'" ./cmd/fiscus`
- Manual demo smoke test across dashboard, transactions, alerts, analytics,
  projections, and unpaired attachment APIs.

## v1.2.0 - 2026-10-24

Advanced tax intelligence and cross-platform distribution release.

### Added

- Category-level tax rules:
  - `tax.categories` assigns a VAT rate and income-tax deductibility to each
    transaction category.
  - VAT collected and deductible VAT are calculated per line item using the
    assigned category rate.
  - Non-deductible expenses are excluded from both VAT recovery and the income
    tax base according to `deductible_percent`.
  - Unlisted categories fall back to `vat_rate_default` with full deductibility.
- VAT carry-forward:
  - Negative VAT quarters produce a credit that offsets later quarters instead
    of being clamped to zero.
  - Tax shields and exports expose the carried-forward credit.
- Tax filing export:
  - `fiscus export --format=tax-summary --year=2026 --quarter=Q1`.
  - Web download from `/transactions/export?format=tax-summary&year=2026&quarter=Q1`.
  - Category-level rows plus per-currency totals with taxable bases, rates,
    deductible expenses, VAT collected/paid, carry-forward, and estimated tax.
- Multi-architecture builds:
  - CGO-free GitHub Actions matrix for Linux x86-64, Linux ARM64, macOS Apple
    Silicon, and Windows x86-64.
  - Release workflow attaches every binary, per-file SHA-256 files, and a
    combined checksum file.

### Changed

- Tax configuration validation covers category names, rate ranges,
  deductibility ranges, and duplicate categories.
- README documents category-level tax rules, carry-forward credits, the tax
  summary export, and the supported release platforms.

### Verification

- `go test -v -race ./...`
- `golangci-lint run ./...`
- `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/fiscus`
- `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X 'main.version=v1.2.0'" ./cmd/fiscus`

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
