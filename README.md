# Fiscus

[![Quick Demo in 10 Seconds](https://img.shields.io/badge/demo-10%20seconds-2ea44f?style=for-the-badge)](#quick-demo-in-10-seconds)

Fiscus is a self-hosted treasury intelligence platform for personal and
small-business bank accounts. It ingests read-only account data through a PSD2
account information provider (GoCardless Bank Account Data or Enable Banking),
stores transactions in SQLite, reconciles balances, estimates tax reserves,
pairs receipts, evaluates cash-flow rules, and delivers alerts to Telegram and
Discord. A compact web dashboard is embedded in the same binary.

Fiscus is written in pure Go and requires no CGO. All monetary values are stored
as integer minor units (for example, cents); binary floating point is never used
for money.

## Quick Demo in 10 Seconds

```bash
git clone https://github.com/RedRiotTank/Fiscus.git
cd Fiscus
go run ./cmd/fiscus demo
# Open http://localhost:8080
```

`fiscus demo` creates an isolated temporary SQLite database, runs all migrations,
seeds six months of coherent personal and business activity, and starts the
dashboard without any bank connection or configuration. External polling,
Telegram, Discord, webhooks, and scheduled backups are disabled in demo mode.

The seeded dataset includes personal and business accounts, subscriptions with a
price increase and a dormant Adobe subscription, payroll and client invoices,
groceries, utilities, restaurants, internal transfers, uncategorized merchants,
paired and unpaired receipts, budgets, tax shields, reconciliation history, and
alert history. Use `fiscus demo --dry-run` to seed and inspect without starting
the server, or `fiscus demo --dir ./demo-data` to keep the demo database and
attachments on disk. `fiscus serve --demo` starts the same environment using the
normal serve entrypoint.

## Features

- PSD2 account information ingestion through GoCardless or Enable Banking
  (consents, balances, booked and pending transactions)
- Idempotent SQLite persistence with embedded, versioned migrations
- Alert rules for duplicate charges, spending spikes, low-balance liquidity,
  consent expiry, subscription price increases, expected inflows, budget caps,
  and dormant (“ghost”) subscriptions
- Regex-based transaction categorization, retroactive rule re-evaluation, and
  YAML rule suggestions for frequent Uncategorized counterparties
- Monthly category budgets with threshold pacing alerts and dashboard progress
  bars
- Recurring subscription and bank-fee detection with Inactive / Potential Ghost
  marking when a charge goes quiet
- Operational burn rate, runway, and internal-transfer-aware accounting exports
- Personal, business, and hybrid workspace profiles with locked views or an
  explicit All / Personal / Business dashboard switcher
- Mathematical balance reconciliation against an opening-balance anchor, with
  discrepancy alerts that only fire when state or delta changes
- Receipt and invoice attachment storage with amount/currency/date auto-pairing,
  manual linking, a receipt inbox, and Telegram document ingestion
- Quarterly VAT and IRPF estimation for business transactions, a tax-reserve
  alert, and a true liquid operating-capital shield per currency
- SQLite FTS5 search across descriptions, counterparties, categories, notes, and
  workspace type, with structured filters and Telegram `/buscar`
- What-if cash-flow scenario simulator with monthly income/expense changes,
  one-off events, and base-vs-simulated projection overlays
- Interactive analytics workspace with category donut, income/expense trend,
  monthly cash-flow waterfall, cumulative spending pace, activity matrix,
  wealth projections, and multi-institution liquidity distribution
- Chart.js bundled locally in the binary (`embed.FS`); no CDN or runtime network
  dependency for charts
- Multi-channel notifications with retries and alert de-duplication
- Interactive Telegram bot for balances, recent movements, manual syncs,
  inline-keyboard categorization, runway, and budget status
- Gzipped online SQLite backups from the CLI and optional weekly Telegram
  delivery
- Asynchronous generic outbound webhooks with retries and optional
  HMAC-SHA256 signing
- Embedded HTMX dashboard with Basic Auth protection
- `/healthz`, `/readyz`, and Prometheus `/metrics` endpoints
- One-command interactive demo with isolated, realistic seeded data
- Single static binary; non-root container image and Compose deployment

## Quick start

```sh
git clone https://github.com/RedRiotTank/Fiscus.git
cd Fiscus
cp config.example.yaml config.yaml
cp .env.example .env
# edit config.yaml / .env with your credentials
mkdir -p data
go run ./cmd/fiscus serve
```

Open <http://localhost:8080>. Create a bank connection from the command line:

```sh
go run ./cmd/fiscus institutions -country ES
go run ./cmd/fiscus connect -institution "Banco Santander" -name "Santander"
```

Open the printed link and complete the consent flow at your bank. With Enable
Banking, keep `fiscus serve` running so the bank redirect can be completed
automatically. With GoCardless, run `go run ./cmd/fiscus sync` or trigger a sync
from the Connections page after authorizing.

See [Banking providers](#banking-providers) for setup details.

Prebuilt CGO-free binaries are published for Linux x86-64, Linux ARM64,
macOS Apple Silicon, and Windows x86-64. Every release includes per-file and
combined SHA-256 checksums.

## Commands

| Command | Description |
| --- | --- |
| `fiscus serve` | Start the dashboard and background sync worker (default) |
| `fiscus connect` | Create a bank consent and print the authorization link |
| `fiscus institutions` | List banks or institutions for a country |
| `fiscus sync` | Run one synchronization cycle and exit |
| `fiscus recategorize` | Preview or apply category rules to historical transactions |
| `fiscus backup` | Write a `.tar.gz` backup with SQLite and attachments |
| `fiscus export` | Export transactions or a tax summary |
| `fiscus demo` | Start a seeded, no-bank demo dashboard |
| `fiscus version` | Print build information |

Most commands accept `-config <path>` (default `config.yaml`, or
`FISCUS_CONFIG`). `connect` accepts `-institution`, `-name`, and `-reference`;
Enable Banking additionally accepts `-country` and `-psu`. `institutions`
accepts `-country` and, for Enable Banking, `-psu`. `demo` accepts `-address`,
`-dir`, and `-dry-run`.

### Accounting exports

`fiscus export` writes to standard output, so it composes with shell redirection:

```sh
fiscus export --format=csv --from=2026-01-01 --to=2026-12-31 > 2026.csv
fiscus export --format=hledger > fiscus.journal
fiscus export --format=beancount > fiscus.beancount
```

`--format` accepts `csv` (default), `hledger` (alias `journal`), or
`beancount`. Dates are inclusive calendar dates in `YYYY-MM-DD` form. Offsetting
equal transfers between your own accounts are paired and omitted by default to
avoid double counting; pass `--include-transfers` to keep them. The Transactions
page provides the same downloads for the current account filter.

### Retroactive categorization

After changing `rules`, re-evaluate existing transactions without touching the
database by default:

```sh
fiscus recategorize                 # preview proposed changes
fiscus recategorize --force         # commit proposed changes
fiscus recategorize --dry-run       # explicit preview
fiscus recategorize --suggest       # print YAML rules for frequent unknowns
```

The preview and apply phases run in one storage transaction. `--dry-run` and
`--force` are mutually exclusive. `--suggest` aggregates counterparties that
remain `Uncategorized` at least three times and prints quoted, case-insensitive
YAML patterns that are safe to paste under `rules`. The Transactions page
exposes the same preview/apply action.

## Analytics

Open **Analytics** in the dashboard for the interactive charts. The same data is
available as authenticated JSON under `/api/analytics`:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/analytics/spending?period=month\|quarter\|ytd` | Category shares, six-month income/expense trend, and the current-month cash-flow waterfall |
| `GET /api/analytics/pace` | Current cumulative spending curve and the previous six monthly baselines |
| `GET /api/analytics/activity` | Weekday and amount-bracket distribution over the last 90 days |
| `GET /api/analytics/projections?extra_monthly_minor=0&yield_bp=300` | Net-savings mean, sample deviation, and 1/5/10/15-year projection cones |
| `POST /api/analytics/scenarios/simulate` | Base-vs-simulated cash-flow projection for a what-if scenario |
| `GET /api/analytics/liquidity?days=90\|180` | Reconstructed per-institution balance series |

Spending, pacing, activity, and projection calculations exclude internal
transfers between owned accounts. Historical account balances for the liquidity
chart are reconstructed from current balances and transaction history; Fiscus
does not need a separate balance-snapshot table. The endpoints and all chart
assets are served by the embedded HTTP server and require the same Basic Auth as
the rest of the dashboard.

## Treasury workflows

### Workspace profiles

`profile` selects how the dashboard treats personal and business accounts:

- `personal` and `business` lock the dashboard to that workspace.
- `hybrid` (the default) supports **All / Personal / Business** views. The
  selected view is carried by `?workspace=` and persisted in the
  `fiscus_workspace` cookie.

Account types come from the optional `accounts` mapping. Unmapped accounts
default to `personal`. The Connections page also lets you switch an account
between Personal and Business at runtime; that choice is stored in SQLite and
is not overwritten by later config-driven mappings or a daemon restart.

```yaml
profile: "hybrid"
accounts:
  - id: "account-id-from-provider"
    type: "business"
```

The profile also controls business-only modules: personal profiles hide the tax
shield, reconciliation urgency, and missing-receipt alerts. Search and exports
still respect the active workspace.

### Balance reconciliation

The first successful check establishes an opening-balance anchor for each
account. Later checks compare the booked ledger movement since that anchor with
the provider's `closingBooked` or `interimBooked` balance. If the ASPSP only
exposes an available balance, pending debits and credits are added back so card
holds do not create persistent false positives. Discrepancy alerts stay
suppressed until the initial baseline has recorded transaction history, then
fire only when the state or delta changes. The Overview shows reconciled,
flagged, and total account counts; discrepancies are listed for audit.

### Receipts and invoices

Receipts can be uploaded from the Transactions page or sent as a Telegram
document/photo. Fiscus stores the file under `attachments.directory`, extracts a
date and amount from the filename/caption when possible, and tries to pair it
with a booked transaction using the exact absolute amount, currency, and a ±3
day booking window. Ambiguous matches stay unpaired for manual linking.

```yaml
attachments:
  enabled: true
  directory: "data/attachments"
  max_size_bytes: 10485760
  receipt_threshold_minor: 5000
  grace_days: 5
```

The receipt inbox lists unpaired files. After every successful bank sync,
Fiscus retries exact amount/currency/±3-day pairing for the whole inbox, so a
receipt uploaded before its transaction was ingested can be linked
automatically later. The **Re-pair inbox** button runs the same pass on demand.
Business-account debits strictly above `receipt_threshold_minor` produce one
missing-receipt alert after `grace_days`. Downloads are authenticated and forced
to an attachment disposition with a PDF/JPEG/PNG/WEBP content type; file paths
and digests are not exposed by the JSON API.

### Tax shield

Business and hybrid workspaces estimate the current quarter's VAT and income-tax
reserve from booked business-account transactions. VAT is derived from gross
amounts using `vat_rate_default`; income tax is retained on positive quarterly
profit using `income_tax_retention`. Transfers and tax-category movements are
excluded. Currencies are never mixed into a single total.

Category-level overrides let each line item use its own VAT rate and income-tax
deductibility. `deductible_percent` controls how much of an expense reduces the
income-tax base and deductible VAT; `0` means fully non-deductible. Unlisted
categories fall back to `vat_rate_default` and 100% deductibility. Negative VAT
quarters carry a credit into later quarters instead of being clamped to zero.

```yaml
tax:
  enabled: true
  vat_rate_default: 0.21
  income_tax_retention: 0.15
  quarter_start_months: [1, 4, 7, 10]
  categories:
    - name: "Software"
      vat_rate: 0.21
      deductible_percent: 1.00
    - name: "Groceries"
      vat_rate: 0.04
      deductible_percent: 0.00
    - name: "Exempt Services"
      vat_rate: 0.00
      deductible_percent: 1.00
```

Export a filing-oriented breakdown from the CLI or the Transactions page:

```sh
fiscus export --format=tax-summary --year=2026 --quarter=Q1
```

The overview card shows total business balance, the untouchable tax reserve, and
**true operating capital** (`total balance - estimated tax due`) per currency.
A warning is sent once per quarter and currency during the 14 days before
quarter-end when a reserve is due.

### Transaction search

The Transactions page and Telegram `/buscar` use SQLite FTS5 with prefix
matching across description, counterparty, category, notes, and workspace type.
Structured filters can be combined with free text:

```text
coffee amount:>10 category:Dining status:booked has:receipt type:business
```

Supported filters are `amount:>`, `amount:>=`, `amount:<`, `amount:<=`,
`amount:=`, `category:`, `status:booked|pending`, `has:receipt|no-receipt`, and
`type:personal|business`. Invalid filter values return a clear error; results
are still restricted by the active workspace.

Transaction notes can be edited inline on the Transactions page or with the
Telegram `/nota <transaction_id> <texto>` command. Updates are written through
the normal transaction update trigger, so FTS5 search reflects them immediately.

Search results are paginated with `page` (1-based) and `limit` (default 50,
maximum 200), for example `/transactions?q=coffee&page=2&limit=25`. Amount
filters accept dot or comma decimals and grouped thousands:
`amount:1234.50`, `amount:1,234.50`, and `amount:1.234,50` are equivalent.

### Scenario simulator

The Analytics workspace includes a **Simulate scenario** drawer. Adjust monthly
income, monthly expenses, a one-off cash event and its month, then compare the
baseline projection with the simulated path. The same calculation is available
as an authenticated JSON endpoint:

```sh
curl -u admin:secret -X POST http://localhost:8080/api/analytics/scenarios/simulate \
  -H 'Content-Type: application/json' \
  -d '{"months":24,"income_delta_minor":50000,"expense_delta_minor":10000,"one_off_minor":-200000,"one_off_month":6,"annual_yield_bp":300}'
```

The response contains shared labels, baseline and simulated balance arrays,
ending balances, the net change, and the effective scenario monthly cash flow.
Horizons are 1–180 months; the UI exposes 12/24/36/60 months. The scenario
drawer uses the same annual investment yield as the projection slider, so
changing that slider updates both projections and simulated scenarios.

## Budgets and expected inflows

Monthly category budgets use integer minor units. Currency defaults to EUR. The
overview dashboard shows month-to-date consumption and a progress bar for each
budget; the bar turns warning or critical as thresholds are crossed.

```yaml
budgets:
  - category: "Restaurants"
    limit: 30000          # 300.00 EUR
    currency: "EUR"
    notify_at_percent: [80, 100]
```

`notify_at_percent` is optional. The 100% threshold is always included and is
critical. Fiscus reports the highest newly crossed threshold in each evaluation,
and de-duplicates alerts per threshold, category, and calendar month so a busy
day does not produce repeated notifications.

Expected inflows catch a missing salary or other recurring credit:

```yaml
expected_inflows:
  - name: "Payroll"
    amount_min: 200000    # 2,000.00 EUR
    currency: "EUR"
    expected_days: [27, 31]
    grace_days: 2
```

A booked credit must match `name` against the counterparty or description, meet
the minimum amount, and use the configured currency inside the expected-day
window. If it has not arrived after the grace period, one warning is emitted for
that month. The default grace period is two days and the default currency is
EUR.

Detected subscriptions are marked `Inactive / Potential Ghost` and notified once
when no charge has appeared for more than 45 days for a monthly subscription or
more than 390 days for a yearly subscription.

## Banking providers

Select one provider with `provider` in `config.yaml` or `FISCUS_PROVIDER`. The
default is `gocardless` for backwards compatibility.

Both providers default to a background sync every six hours, matching the PSD2
limit of four unattended accesses per day. Account metadata such as IBAN,
currency, and holder name is fetched once and cached in SQLite; routine syncs
only request balances and transactions. If an ASPSP returns HTTP 429 or
`ASPSP_RATE_LIMIT_EXCEEDED`, Fiscus records a cooldown until the next UTC
midnight (or six hours, whichever comes first), keeps showing cached data, and
resumes automatically when the cooldown expires.

### Enable Banking

Enable Banking works with banks such as Banco Santander in Spain and does not
require a GoCardless account. A **production** application is required for real
bank accounts. Sandbox applications use simulated data and cannot access
Santander.

#### 1. Register the production application

Sign in to the [Enable Banking control panel](https://enablebanking.com/sign-in/)
and create an application with:

- **Environment:** Production.
- **Private key:** Choose **Generate in the browser (using SubtleCrypto) and
  export private key**. The browser downloads a file named
  `<application-id>.pem`. The private key stays on your machine. Generating the
  key and self-signed certificate externally with OpenSSL and importing the
  public certificate also works.
- **Application details:** A description, data protection email, privacy policy
  URL, and terms of service URL are required for production applications relying
  on Enable Banking authorization. The privacy and terms URLs must be publicly
  reachable.
- **Redirect URLs:** Use `https://localhost:8080/callback` for the local setup
  below, or an HTTPS tunnel URL. Enable Banking production rejects `http://`
  redirect URLs, including localhost. The value must match
  `enablebanking.redirect_url` exactly, including scheme, port, and path.

After registering, note the application ID and store the downloaded `.pem`
outside version control:

```sh
chmod 600 /path/to/<application-id>.pem
```

#### 2. Enable HTTPS locally

Production Enable Banking redirects must use HTTPS. Fiscus can terminate TLS
directly with a self-signed certificate. Generate one for `localhost`:

```sh
mkdir -p secrets
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout secrets/localhost-key.pem \
  -out secrets/localhost-cert.pem \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost"
```

Set `server.tls_cert_file` and `server.tls_key_file` as shown below, then open
<https://localhost:8080>. The browser will warn that the certificate is
self-signed; accept it for localhost. The bank redirect and dashboard then use
the same HTTPS origin. The certificate and key are git-ignored.

If you prefer not to use a self-signed certificate, run an HTTPS tunnel to
Fiscus and register the tunnel URL instead:

```sh
cloudflared tunnel --url http://localhost:8080
# register https://<generated-name>.trycloudflare.com/callback
```

Keep the tunnel running while authorizing the bank, and set
`enablebanking.redirect_url` to the exact tunnel callback URL. A named tunnel or
a reverse proxy with a trusted certificate is preferable for a stable URL.

#### 3. Configure Fiscus

```yaml
provider: enablebanking

server:
  address: ":8080"
  tls_cert_file: "secrets/localhost-cert.pem"
  tls_key_file: "secrets/localhost-key.pem"

enablebanking:
  app_id: "your-application-id"
  private_key_path: "/absolute/path/to/your-application-id.pem"
  redirect_url: "https://localhost:8080/callback"
  country: "ES"
  institution_name: "Banco Santander"
  psu_type: "personal"
  # Banco Santander allows up to 180 days.
  consent_validity_days: 180
```

#### 4. Activate the application

New production applications start in the **Inactive** state. Until activated,
API calls such as `fiscus institutions` return `403 Application is not active`.

In the Enable Banking control panel, click **Activate by linking accounts** and
complete the bank login. This activates the application in restricted mode:
Fiscus can then access only the accounts you linked. For personal use, this is
the correct option; **Request activation** is for general availability and
requires Enable Banking review, KYC, and a production agreement.

Activation only enables the application. It does not create the connection
Fiscus uses, so continue with the next step.

#### 5. Start Fiscus and create a consent

```sh
fiscus serve
# in another terminal
fiscus institutions -country ES
fiscus connect -institution "Banco Santander"
```

Start `fiscus serve` before opening the authorization link. If `institutions`
still returns `403 Application is not active`, the account-linking activation
has not completed yet.

For Banco Santander Spain, the bank may ask you to choose a document type:

- **TIN** for a Spanish DNI/NIF
- **NIE** for a foreigner identity number
- **CIF** for a company
- **Passport** for a passport
- **User** for an online-banking username

Choose **TIN** when you normally log in with your DNI.

#### 6. Complete and verify

Open the printed authorization link and complete the bank's authentication. The
bank redirects your browser to `/callback`; Fiscus validates the OAuth `state`,
exchanges the one-time code for a session, links the connection, and requests a
sync. Verify the result at <https://localhost:8080/connections>.

The `/callback` endpoint is intentionally reachable without dashboard Basic Auth
because the bank redirects the browser there. A random `state` stored with the
pending connection validates the callback. Keep the daemon running until the
redirect completes. If the bank redirect fails, create a new consent rather than
reusing an expired link.

The private key is never sent to the dashboard or written to the database. Keep
it outside version control, restrict permissions (`chmod 600`), and mount it
read-only in Docker. The container runs as UID/GID `10001`, so the file must be
readable by that user. Never commit the key, `.env`, or `config.yaml`.

### GoCardless

Existing GoCardless users can continue with the default provider:

```yaml
provider: gocardless

gocardless:
  secret_id: "your-secret-id"
  secret_key: "your-secret-key"
  redirect_url: "http://localhost:8080/"
  country: "ES"
```

GoCardless Bank Account Data has disabled new signups. Existing credentials
continue to work; new installations should use Enable Banking.

## Metrics

`GET /metrics` exposes Prometheus metrics, including sync outcomes and durations,
ingested transaction counts, latest balances in minor units, alerts by rule and
severity, and notification deliveries by channel. Go runtime and process metrics
are included. A scrape configuration is provided in `deploy/prometheus.yml`:

```yaml
scrape_configs:
  - job_name: fiscus
    metrics_path: /metrics
    static_configs:
      - targets: ["fiscus:8080"]
```

## Configuration

Fiscus reads an optional YAML file and then applies environment variables, which
take precedence. Unknown YAML keys are rejected so typos fail fast. See
`config.example.yaml` and `.env.example` for every option. The most important
variables are:

| Variable | Purpose |
| --- | --- |
| `FISCUS_PROVIDER` | Banking provider: `gocardless` or `enablebanking` |
| `FISCUS_PROFILE` | Workspace profile: `personal`, `business`, or `hybrid` |
| `FISCUS_DATABASE_PATH` | SQLite database file (default `data/fiscus.db`) |
| `FISCUS_SERVER_ADDRESS` | HTTP(S) listen address (default `:8080`) |
| `FISCUS_SERVER_USERNAME` / `FISCUS_SERVER_PASSWORD` | Dashboard Basic Auth |
| `FISCUS_SERVER_TLS_CERT_FILE` / `..._TLS_KEY_FILE` | Serve HTTPS directly (set both) |
| `FISCUS_GOCARDLESS_SECRET_ID` / `..._SECRET_KEY` | GoCardless API credentials |
| `FISCUS_GOCARDLESS_REDIRECT_URL` | OAuth redirect registered with GoCardless |
| `FISCUS_GOCARDLESS_COUNTRY` | Institution country code (default `ES`) |
| `FISCUS_ENABLEBANKING_APP_ID` | Enable Banking application ID |
| `FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH` | Path to the application's RSA private key |
| `FISCUS_ENABLEBANKING_REDIRECT_URL` | HTTPS redirect URL registered with Enable Banking |
| `FISCUS_ENABLEBANKING_COUNTRY` | Bank country code (default `ES`) |
| `FISCUS_ENABLEBANKING_INSTITUTION_NAME` | Default bank name for `fiscus connect` |
| `FISCUS_TELEGRAM_BOT_TOKEN` / `..._CHAT_ID` | Telegram alerts |
| `FISCUS_DISCORD_WEBHOOK_URL` | Discord alerts |
| `FISCUS_ATTACHMENTS_ENABLED` / `..._DIRECTORY` | Receipt storage and storage path |
| `FISCUS_ATTACHMENTS_RECEIPT_THRESHOLD_MINOR` / `..._GRACE_DAYS` | Missing-receipt policy |
| `FISCUS_TAX_ENABLED` | Enable the quarterly tax shield |
| `FISCUS_TAX_VAT_RATE_DEFAULT` / `..._INCOME_TAX_RETENTION` | Tax estimation rates |
| `FISCUS_TAX_QUARTER_START_MONTHS` | Comma-separated fiscal quarter starts |
| `FISCUS_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` |

Durations use Go syntax (`15m`, `24h`, `500ms`). Liquidity thresholds are
configured per currency as decimal strings in major units, for example
`EUR: "500.00"` in YAML or `FISCUS_LIQUIDITY_THRESHOLDS=EUR:500.00` in the
environment. They are parsed into integer minor units at startup and must be
positive.

Transaction categories are defined with top-level `rules` in YAML. Rules are
evaluated in order against the counterparty and description, and the first match
wins. Unmatched transactions become `Uncategorized`.

```yaml
rules:
  - category: "Utilities"
    pattern: "(?i)IBERDROLA|ENDESA|AGUAS"
  - category: "Payroll"
    pattern: "(?i)NOMINA|SALARY"
```

Set either `server.password` or `server.password_hash` (bcrypt), never both. If
neither a username nor a password is set, the dashboard is unauthenticated, which
is only appropriate behind a trusted reverse proxy. `CONNECT` requests to the
dashboard endpoints are ignored; state-changing sync requests require an
`X-Fiscus-Request: 1` header.

`/healthz`, `/readyz`, `/metrics`, and the Enable Banking `/callback` endpoint
are intentionally unauthenticated so orchestrators, scrapers, and bank redirects
can reach them. `/callback` requires a valid one-time `state` value. Bind the
service to a private network or protect those paths at the proxy if they must
not be public.

## Alert rules

1. **Duplicate charge**: the same counterparty and exact amount within the
   configured window (default 24h).
2. **Spike**: a debit that exceeds the rolling average of prior debits by more
   than the configured percentage (default 30%) with enough history.
3. **Liquidity guard**: available balance below the configured per-currency
   threshold.
4. **Consent expiration**: a linked PSD2 consent is within 14 or 3 days of
   expiring. The alert includes a renewal link and is deduplicated per
   connection, expiry, and threshold.
5. **Subscription price increase**: a detected recurring charge is higher than
   its previous charge. The alert reports the old and new amounts and is
   deduplicated per merchant and charge.
6. **Expected inflow missing**: a configured monthly credit has not arrived
   inside its expected-day window plus grace period. The alert is emitted once
   per inflow and month.
7. **Potential ghost subscription**: a recurring charge has gone quiet beyond the
   monthly or yearly stale threshold. The subscription is marked inactive in the
   dashboard and notified once per last-charge date.
8. **Budget threshold**: month-to-date spending in a configured category crosses
   a warning or critical percentage. Alerts are de-duplicated per threshold,
   category, and month.
9. **Reconciliation discrepancy**: an account's observed balance differs from
   the anchored ledger expectation, or its reconciliation state changes.
10. **Missing receipt**: a business-account debit strictly above the configured
    threshold has no attachment after the grace period. Ambiguous auto-pairs are
    never guessed.
11. **Tax reserve approaching**: a positive estimated VAT/IRPF settlement exists
    within 14 days of quarter-end. The alert reports VAT, income tax, and true
    operating capital, and is de-duplicated per quarter and currency.

The **Subscriptions & Fees** dashboard view estimates the monthly and annualized
cost of active subscriptions and lists detected bank commissions. Subscriptions
without a recent charge are labelled `Inactive / Potential Ghost`.

Alerts are persisted before dispatch. A deterministic de-duplication key prevents
the same condition from notifying twice. Telegram and Discord deliveries are
retried with exponential backoff.

## Telegram bot

When `alerts.telegram.bot_token` and `chat_id` are configured, `fiscus serve`
also starts a long-polling Telegram bot. Commands are ignored unless they come
from the configured chat, and optionally from `alerts.telegram.user_id` (useful
for group chats). Supported commands:

| Command | Action |
| --- | --- |
| `/saldo` | Consolidated balance and per-account breakdown |
| `/sync` | Run a bank synchronization and report the outcome |
| `/ultimos [n]` | Last `n` confirmed transactions with categories (max 50) |
| `/buscar <texto> [filtros]` | FTS5 search with amount, category, status, receipt, and type filters |
| `/nota <transaction_id> <texto>` | Attach or replace a note on a transaction |
| `/runway` | Liquid balance, monthly burn, and runway in months per currency |
| `/presupuesto` | Current-month category budget consumption |
| `/help` | Show available commands |

When a booked transaction cannot be categorized, Fiscus can send an inline
keyboard with up to five configured categories plus **Ignore**; tapping a button
updates the stored transaction category and answers the callback. This behavior
is controlled by `alerts.telegram.notify_uncategorized` and is enabled by
default when the bot is configured. Documents and photos sent to the bot are
stored as receipts and auto-paired with matching transactions; the reply reports
whether the file was linked or left in the receipt inbox. The polling loop stops
cleanly as part of the daemon's SIGTERM/SIGINT shutdown.

## Backups

`fiscus backup` uses SQLite's online backup API, so it is safe to run while the
daemon is serving traffic. Each run writes a single `.tar.gz` archive containing
the live database snapshot as `fiscus.db` and the receipt directory recursively
under `attachments/`:

```sh
fiscus backup --out=/var/backups/fiscus-$(date +%F).tar.gz
fiscus backup   # default: backups/fiscus-YYYYMMDD-HHMMSS.tar.gz
```

Optional weekly backup delivery to the configured Telegram chat requires both
`backups.enabled` and `backups.telegram`:

```yaml
backups:
  enabled: true
  telegram: true
  directory: "backups"
  interval: 168h
```

Each run writes a local `.tar.gz` archive and uploads the same document through
the Telegram bot. The backup directory should be on persistent storage and
protected with restrictive filesystem permissions. To restore, stop the daemon,
extract the archive, place `fiscus.db` at `database.path` (or point the
configuration at it), and restore the extracted `attachments/` directory to
`attachments.directory`.

## Webhooks

Generic webhooks POST an asynchronous JSON envelope to any HTTP(S) endpoint:

```yaml
webhooks:
  - url: "https://example.com/fiscus/events"
    secret: "replace-with-a-long-random-secret"
    events: ["transaction.created", "alert.triggered", "sync.completed"]
```

Events are `transaction.created` (a new transaction was first stored),
`alert.triggered` (an alert was persisted), and `sync.completed` (a sync cycle
finished, successfully or not). Delivery happens off the request path and retries
transient failures with exponential backoff. A 2xx response succeeds; most 4xx
responses are not retried.

When `secret` is set, each request carries:

- `X-Fiscus-Event`: the event name.
- `X-Fiscus-Timestamp`: the envelope timestamp in RFC 3339.
- `X-Fiscus-Signature`: `sha256=<hex HMAC-SHA256>` over the exact request body.

Verify the signature before trusting a payload, and keep webhook secrets out of
version control. The JSON body has the form
`{"event":"...","timestamp":"...","data":{...}}`.

## Deployment

The multi-stage `Dockerfile` produces a static binary in a minimal Alpine image
that runs as an unprivileged user and stores SQLite data in `/data`. Build and
run it with Compose:

```sh
cp .env.example .env
# edit .env
docker compose up -d --build
```

The Compose service reads `.env`, publishes the dashboard on
`127.0.0.1:8080`, persists `/data` in a named volume, and runs with a read-only
root filesystem, a temporary `/tmp`, no Linux capabilities, and
`no-new-privileges`. Set `FISCUS_SERVER_USERNAME` and
`FISCUS_SERVER_PASSWORD_HASH` before exposing the dashboard.

The image can also be built directly:

```sh
make docker-build
docker run --rm -p 8080:8080 \
  --env-file .env \
  -v fiscus-data:/data \
  -v /host/path/enablebanking.pem:/run/secrets/enablebanking.pem:ro \
  fiscus:local
```

When using Enable Banking, set
`FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH=/run/secrets/enablebanking.pem` and mount
the key read-only. The file must be readable by UID/GID `10001`. If Fiscus serves
HTTPS directly, mount the TLS certificate and key as well and set
`FISCUS_SERVER_TLS_CERT_FILE` and `FISCUS_SERVER_TLS_KEY_FILE`. The Compose file
can use the same bind mounts:

```yaml
volumes:
  - fiscus-data:/data
  - ./secrets/enablebanking.pem:/run/secrets/enablebanking.pem:ro
  - ./secrets/localhost-cert.pem:/run/secrets/localhost-cert.pem:ro
  - ./secrets/localhost-key.pem:/run/secrets/localhost-key.pem:ro
```

The database, configuration, environment files, and private keys are git-ignored.
Use `fiscus backup` for an online snapshot (or stop the daemon before taking a
volume snapshot). `/healthz` reports process liveness, `/readyz` verifies
database access, and `/metrics` is the Prometheus endpoint. To expose the
dashboard publicly, terminate
TLS at a reverse proxy and keep `FISCUS_SERVER_ADDRESS` bound to the private
network. Enable Banking's redirect URL must point at the public `/callback` path
when Fiscus is reached through a proxy.

## Development

```sh
make test        # go test ./...
make race        # go test -race ./...
make vet
make lint        # golangci-lint run ./...
make build       # bin/fiscus with version metadata
```

`config.example.yaml` contains local defaults. Tests use temporary SQLite
databases and `httptest` servers; no external services are required. The CI
workflow runs vet, the race-enabled test suite, golangci-lint, and a Docker
image build on every push to `main` and every pull request.

## Disclaimer

Fiscus is a monitoring tool, not an accounting system or financial advisor. It
uses read-only APIs and never initiates payments. Verify alerts against your bank
before acting on them.

## Privacy and terms

See [PRIVACY.md](PRIVACY.md) and [TERMS.md](TERMS.md).

## License

MIT. See [LICENSE](LICENSE).
