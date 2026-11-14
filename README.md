# Centro de Estudiantes

A multi-tenant platform for schools in Tucumán, starting with student councils (centros de estudiantes):
finances, delegate elections voted from each student's phone, and formal note templates exported to PDF and
Word. Municipal indicators will follow on the same base. Every school is a tenant isolated by PostgreSQL
Row-Level Security, and the same build runs as SaaS or on the customer's own server. Product scope lives in
[docs/spec/mvp.md](docs/spec/mvp.md); stack decisions in [docs/adr/0001-stack.md](docs/adr/0001-stack.md).

## Repository layout

| Path | Contents |
|---|---|
| `api/` | Go API: stdlib `net/http`, `pgx`, `goose` migrations, server generated from the OpenAPI contract |
| `web/` | React + Vite + TypeScript frontend with a typed client generated from the same contract |
| `openapi/` | `openapi.yaml`, the single API contract shared by both sides |
| `docs/` | Product specification and architecture decision records |
| `docs/history/odd/` | Feature task documents and work log from before the orca-stack setup (tickets are the spec now) |

## Prerequisites

Go 1.26.11 (pinned by `toolchain` in api/go.mod), Node 26, pnpm 11 and Docker. A C compiler on `PATH` (on Windows, MinGW-w64 GCC) is needed only for
cgo tooling such as `sqlc` and `go test -race`.

## Development

Create a `.env` file at the repository root (never commit it) with `POSTGRES_PASSWORD=<a local password>`, then:

```sh
docker compose up -d              # PostgreSQL for development

cd api && go run ./cmd/server     # API on http://localhost:8080
cd api && go test ./...           # Postgres integration tests need Docker running; add -short to skip them
cd api && go generate ./...       # after editing openapi/openapi.yaml

cd web && pnpm install && pnpm dev  # web app; /api is proxied to the Go API on :8080
cd web && pnpm test
cd web && pnpm gen:api            # after editing openapi/openapi.yaml
```

### Server environment

Copy the example outside the repository, replace its placeholders with local values, and source it in your shell before starting the API:

```sh
mkdir -p "$HOME/centro-dev"
cp docs/dev/devvars.example.sh "$HOME/centro-dev/devvars.sh"
# Edit "$HOME/centro-dev/devvars.sh" with your local values first.
. "$HOME/centro-dev/devvars.sh"
```

Use separate database roles as described below, and generate fresh keys rather than using the placeholders. The Compose `.env` file and this shell file serve different purposes; keep both private.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Application connection: a login role member of `ce_app`, without `SUPERUSER` or `BYPASSRLS` |
| `MIGRATE_DATABASE_URL` | Table owner connection, used only to apply migrations at startup |
| `ACCESS_CODE_KEY` | Base64 HMAC key (at least 32 random bytes) for roster access codes; generate it with `openssl rand -base64 32`. The server refuses to start without it. Changing it invalidates every unused code |
| `TOTP_ENC_KEY` | Base64 of exactly 32 random bytes: the AES-256-GCM key that encrypts two-factor (TOTP) seeds at rest, and (through HKDF-SHA256) the HMAC key of recovery codes; generate it with `openssl rand -base64 32` and keep it apart from database backups. The server refuses to start without it. Losing or changing it disables every enrolled authenticator and recovery code (each user then needs `admin reset-mfa`) |
| `APP_BASE_URL` | Public origin of the web app used in emailed links, e.g. `https://ce.example.edu.ar` (`http://localhost:5173` is accepted for development). Required |
| `SMTP_HOST` | SMTP relay of the email provider. When set, mail is sent through it and STARTTLS is mandatory (the server refuses to send without TLS) |
| `SMTP_PORT` | SMTP submission port (default `587`) |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | SMTP credentials (PLAIN auth, only over TLS). Required with `SMTP_HOST` |
| `MAIL_FROM` | Sender address, e.g. `Centro de Estudiantes <no-reply@ce.example.edu.ar>`. Required with `SMTP_HOST` |
| `MAIL_DEV_DIR` | Development only, used when `SMTP_HOST` is empty: every email is written as an `.eml` file in this directory instead of being sent. With neither variable the server refuses to start |
| `TRUSTED_PROXIES` | Comma-separated CIDRs of the reverse proxies in front of the API, e.g. `127.0.0.1/32` for a Caddy on the same host (default empty). Only when the TCP peer is inside one of them does the server read `X-Forwarded-For`, taking the right-most address that is not itself a trusted proxy as the client (ports such as `1.2.3.4:5678` or `[2001:db8::1]:443` are ignored; if every hop is trusted, the left-most one; an unparsable hop before any untrusted one puts the request in one shared `unparsable` bucket, never the proxy's); otherwise the header is ignored, so clients cannot forge their address. Write IPv4 proxies as IPv4 CIDRs: IPv4-mapped prefixes (`::ffff:…`) are refused at startup. IPv6 clients are rate limited per /64. Behind a proxy, leaving it empty makes every request share the proxy's limit |
| `ADDR` | Listen address (default `:8080`) |

The server stops gracefully on SIGINT/SIGTERM: in-flight requests get up to 15 s, then queued emails get up to 10 s before it exits (25 s in total). Docker's default grace period is 10 s, so the app service of a deployment's compose file must set `stop_grace_period: 30s` (the development `compose.yaml` only runs PostgreSQL). Queued emails are kept in memory: a crash loses them, and when the backlog (256) is full a message is dropped and logged at WARN with the running count of dropped messages (never its content). Expired sessions are deleted every 10 minutes.

Roster files are CSV UTF-8 with the header `full_name,document_number`; Excel's semicolon-separated CSV (Argentine locale) is accepted too, detected from the header line. Document numbers have 7 to 9 digits, written plain (`40111222`) or with dots in every thousands position (`40.111.222`, `9.876.543`). Names may not contain invisible format characters (bidirectional overrides, zero-width characters).

### Operator commands

Run with `DATABASE_URL` set to the application role (`cd api && go run ./cmd/admin …`); both commands refuse to run when that role is a superuser, has `BYPASSRLS`, is not a member of `ce_app` or owns a table:

| Command | Effect |
|---|---|
| `admin bootstrap --org-name NAME --email EMAIL` | Creates a school and its first school admin; the password (10 to 128 characters) is read from stdin, without echo on a terminal |
| `admin reset-mfa --email EMAIL` | Lost phone: deletes the user's TOTP and recovery codes and signs out every session. Confirm the person's identity out of band first; school admins and officers must enroll a new authenticator at their next sign-in. Migration 00008 deleted every recovery code stored before they became HMAC-keyed: an enrolled user who needs codes again goes through this command |

## Contract-first rule

Change the API in `openapi/openapi.yaml` first, then regenerate both sides (`go generate ./...` in `api/`,
`pnpm gen:api` in `web/`) and commit the generated files together with the change.

## Frontend guidance

UI work in `web/` follows Google Chrome's [Modern Web Guidance](https://github.com/GoogleChrome/modern-web-guidance-src):
accessible, Baseline-aware patterns with small fallbacks instead of heavy libraries. Before building a UI
pattern (form, dialog, select, navigation…), look up the guide and apply it:

```sh
npx modern-web-guidance@1.0.0 search "sign-in form with one-time code"   # find guide ids
npx modern-web-guidance@1.0.0 retrieve autofill-sign-in-form             # read the guide
npx modern-web-guidance@1.0.0 legacy-scan web/src                        # spot legacy patterns
```

The CLI runs offline and has no dependencies. Origin Trial and volatile features stay disabled (no `.mwgrc`).

## Continuous integration

`.github/workflows/ci.yml` runs on every push: Go generated code check, `go vet`, `go test -race` against a real PostgreSQL (testcontainers), `govulncheck`; and for the web, the generated client check, typecheck, tests, build and `pnpm audit --prod`. Actions are pinned by commit SHA.
