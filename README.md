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
| `odd/` | Feature task documents and work log |

## Prerequisites

Go 1.26, Node 26, pnpm 11 and Docker. A C compiler on `PATH` (on Windows, MinGW-w64 GCC) is needed only for
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

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | Application connection: a login role member of `ce_app`, without `SUPERUSER` or `BYPASSRLS` |
| `MIGRATE_DATABASE_URL` | Table owner connection, used only to apply migrations at startup |
| `ACCESS_CODE_KEY` | Base64 HMAC key (at least 32 random bytes) for roster access codes; generate it with `openssl rand -base64 32`. The server refuses to start without it. Changing it invalidates every unused code |
| `ADDR` | Listen address (default `:8080`) |

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
