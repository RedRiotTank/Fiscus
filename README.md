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
