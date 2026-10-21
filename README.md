# Fiscus

Fiscus is a self-hosted treasury monitoring daemon for personal and small-business
bank accounts. It ingests read-only account data through a PSD2 account
information provider (GoCardless Bank Account Data or Enable Banking), stores
transactions in SQLite, evaluates a set of cash-flow rules, and delivers alerts
to Telegram and Discord. A compact web dashboard is embedded in the same binary.

Fiscus is written in pure Go and requires no CGO. All monetary values are stored
as integer minor units (for example, cents); binary floating point is never used
for money.

## Features

- PSD2 account information ingestion through GoCardless or Enable Banking
  (consents, balances, booked and pending transactions)
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

## Commands

| Command | Description |
| --- | --- |
| `fiscus serve` | Start the dashboard and background sync worker (default) |
| `fiscus connect` | Create a bank consent and print the authorization link |
| `fiscus institutions` | List banks or institutions for a country |
| `fiscus sync` | Run one synchronization cycle and exit |
| `fiscus version` | Print build information |

Every command accepts `-config <path>` (default `config.yaml`, or
`FISCUS_CONFIG`). `connect` accepts `-institution`, `-name`, and `-reference`;
Enable Banking additionally accepts `-country` and `-psu`. `institutions`
accepts `-country` and, for Enable Banking, `-psu`.

## Banking providers

Select one provider with `provider` in `config.yaml` or `FISCUS_PROVIDER`. The
default is `gocardless` for backwards compatibility.

### Enable Banking

Enable Banking works with banks such as Banco Santander in Spain and does not
require a GoCardless account.

1. Sign in to the [Enable Banking control panel](https://enablebanking.com/sign-in/)
   and create an application.
2. Register a redirect URL. For local use, set it to
   `http://localhost:8080/callback`. It must match `enablebanking.redirect_url`
   exactly, including scheme, port, and path.
3. Download the application's RSA private key (`.pem`) and note the application
   ID.
4. Configure Fiscus:

   ```yaml
   provider: enablebanking

   enablebanking:
     app_id: "your-application-id"
     private_key_path: "/absolute/path/to/your-application-id.pem"
     redirect_url: "http://localhost:8080/callback"
     country: "ES"
     institution_name: "Banco Santander"
     psu_type: "personal"
   ```

5. Start the daemon, list banks, and start a consent:

   ```sh
   fiscus serve
   # in another terminal
   fiscus institutions -country ES
   fiscus connect -institution "Banco Santander"
   ```

6. Open the printed authorization link and complete the bank's authentication.
   The bank redirects your browser to `/callback`; Fiscus validates the OAuth
   `state`, exchanges the one-time code for a session, links the connection, and
   requests a sync.

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
| `FISCUS_DATABASE_PATH` | SQLite database file (default `data/fiscus.db`) |
| `FISCUS_SERVER_ADDRESS` | HTTP listen address (default `:8080`) |
| `FISCUS_SERVER_USERNAME` / `FISCUS_SERVER_PASSWORD` | Dashboard Basic Auth |
| `FISCUS_GOCARDLESS_SECRET_ID` / `..._SECRET_KEY` | GoCardless API credentials |
| `FISCUS_GOCARDLESS_REDIRECT_URL` | OAuth redirect registered with GoCardless |
| `FISCUS_GOCARDLESS_COUNTRY` | Institution country code (default `ES`) |
| `FISCUS_ENABLEBANKING_APP_ID` | Enable Banking application ID |
| `FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH` | Path to the application's RSA private key |
| `FISCUS_ENABLEBANKING_REDIRECT_URL` | Redirect URL registered with Enable Banking |
| `FISCUS_ENABLEBANKING_COUNTRY` | Bank country code (default `ES`) |
| `FISCUS_ENABLEBANKING_INSTITUTION_NAME` | Default bank name for `fiscus connect` |
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
  -v /host/path/enablebanking.pem:/run/secrets/enablebanking.pem:ro \
  fiscus:local
```

When using Enable Banking, set
`FISCUS_ENABLEBANKING_PRIVATE_KEY_PATH=/run/secrets/enablebanking.pem` and mount
the key read-only. The file must be readable by UID/GID `10001`. The Compose file
can use the same bind mount:

```yaml
volumes:
  - fiscus-data:/data
  - ./secrets/enablebanking.pem:/run/secrets/enablebanking.pem:ro
```

The database, configuration, environment files, and private keys are git-ignored.
Back up the SQLite file (or use a volume snapshot) while the daemon is stopped.
`/healthz` reports process liveness, `/readyz` verifies database access, and
`/metrics` is the Prometheus endpoint. To expose the dashboard publicly, terminate
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
