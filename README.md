# Fiscus

Fiscus is a self-hosted treasury monitoring daemon for personal and small-business
bank accounts. It ingests read-only account data through the GoCardless Bank
Account Data API (PSD2 AIS), stores transactions in SQLite, evaluates a set of
cash-flow rules, and delivers alerts to Telegram and Discord. A compact web
dashboard is embedded in the same binary.

Fiscus is written in pure Go and requires no CGO. All monetary values are stored
as integer minor units (for example, cents); binary floating point is never used
for money.

## Features

- PSD2 account information ingestion via GoCardless (token refresh, requisitions,
  balances, booked and pending transactions)
- Idempotent SQLite persistence with embedded, versioned migrations
- Alert rules for duplicate charges, spending spikes, and low-balance liquidity
- Multi-channel notifications with retries and alert de-duplication
- Embedded HTMX dashboard with Basic Auth protection
- `/healthz`, `/readyz`, and Prometheus `/metrics` endpoints
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

Open <http://localhost:8080>. Create a bank connection with:

```sh
go run ./cmd/fiscus institutions -country ES
go run ./cmd/fiscus connect -institution <institution-id> -name "My bank"
```

Open the printed link, complete the consent flow at your bank, then run
`go run ./cmd/fiscus sync` or trigger a sync from the Connections page.

## Commands

| Command | Description |
| --- | --- |
| `fiscus serve` | Start the dashboard and background sync worker (default) |
| `fiscus connect` | Create a GoCardless consent and print the authorization link |
| `fiscus institutions` | List institutions for a country |
| `fiscus sync` | Run one synchronization cycle and exit |
| `fiscus version` | Print build information |

Every command accepts `-config <path>` (default `config.yaml`, or
`FISCUS_CONFIG`). `connect` accepts `-institution`, `-name`, and `-reference`.
`institutions` accepts `-country`.

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
| `FISCUS_DATABASE_PATH` | SQLite database file (default `data/fiscus.db`) |
| `FISCUS_SERVER_ADDRESS` | HTTP listen address (default `:8080`) |
| `FISCUS_SERVER_USERNAME` / `FISCUS_SERVER_PASSWORD` | Dashboard Basic Auth |
| `FISCUS_GOCARDLESS_SECRET_ID` / `..._SECRET_KEY` | GoCardless API credentials |
| `FISCUS_GOCARDLESS_REDIRECT_URL` | OAuth redirect registered with GoCardless |
| `FISCUS_GOCARDLESS_COUNTRY` | Institution country code (default `ES`) |
| `FISCUS_TELEGRAM_BOT_TOKEN` / `..._CHAT_ID` | Telegram alerts |
| `FISCUS_DISCORD_WEBHOOK_URL` | Discord alerts |
| `FISCUS_LOG_LEVEL` | `debug`, `info`, `warn`, or `error` |

Durations use Go syntax (`15m`, `24h`, `500ms`). Liquidity thresholds are
configured per currency as decimal strings in major units, for example
`EUR: "500.00"` in YAML or `FISCUS_LIQUIDITY_THRESHOLDS=EUR:500.00` in the
environment. They are parsed into integer minor units at startup and must be
positive.

Set either `server.password` or `server.password_hash` (bcrypt), never both. If
neither a username nor a password is set, the dashboard is unauthenticated, which
is only appropriate behind a trusted reverse proxy. `CONNECT` requests to the
dashboard endpoints are ignored; state-changing sync requests require an
`X-Fiscus-Request: 1` header.

`/healthz`, `/readyz`, and `/metrics` are intentionally unauthenticated so
orchestrators and scrapers can reach them. Bind the service to a private network
or protect those paths at the proxy if they must not be public.

## Alert rules

1. **Duplicate charge**: the same counterparty and exact amount within the
   configured window (default 24h).
2. **Spike**: a debit that exceeds the rolling average of prior debits by more
   than the configured percentage (default 30%) with enough history.
3. **Liquidity guard**: available balance below the configured per-currency
   threshold.

Alerts are persisted before dispatch. A deterministic de-duplication key prevents
the same condition from notifying twice. Telegram and Discord deliveries are
retried with exponential backoff.

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
  fiscus:local
```

The database, configuration, and environment files are git-ignored. Back up the
SQLite file (or use a volume snapshot) while the daemon is stopped. `/healthz`
reports process liveness, `/readyz` verifies database access, and `/metrics` is
the Prometheus endpoint. To expose the dashboard publicly, terminate TLS at a
reverse proxy and keep `FISCUS_SERVER_ADDRESS` bound to the private network.

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

## License

MIT. See [LICENSE](LICENSE).
