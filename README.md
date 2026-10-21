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
cp config.example.yaml config.yaml
cp .env.example .env
# edit config.yaml / .env with your credentials
go run ./cmd/fiscus serve
```

Open <http://localhost:8080>. Create a bank connection with:

```sh
go run ./cmd/fiscus connect -institution <institution-id> -country ES
```

Then open the printed link, complete the bank consent flow, and wait for the next
sync (or trigger a sync from the Connections page).

## Configuration

Fiscus reads an optional YAML file (`-config`, default `config.yaml`) and then
overrides values from environment variables. See `config.example.yaml` and
`.env.example` for the full set of options. Important variables:

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

Liquidity thresholds are configured per currency as decimal strings, for example
`EUR: "500.00"`. They are parsed into integer minor units at startup.

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

## Development

```sh
go test -race ./...
go vet ./...
golangci-lint run
```

`config.example.yaml` contains local defaults. Tests use temporary SQLite
databases and `httptest` servers; no external services are required.

## Disclaimer

Fiscus is a monitoring tool, not an accounting system or financial advisor. It
uses read-only APIs and never initiates payments. Verify alerts against your bank
before acting on them.

## License

MIT. See [LICENSE](LICENSE).
