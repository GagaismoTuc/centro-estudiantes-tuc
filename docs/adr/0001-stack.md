# ADR 0001 — Stack: Go API, PostgreSQL with Row-Level Security, React + TypeScript

Status: accepted · Date: 2026-11-13

## Context

We sell one platform to schools (first) and municipalities (later), either as SaaS or installed on the
customer's own server. It stores personal data of minors and runs student elections, so reliability and
security weigh more than development speed. The owner asked for a professional, conventional stack, not a
trendy one, and chose: "Podemos hacer el backend con Go y frontend con typescript".

## Decision

| Layer | Choice |
|---|---|
| Backend | Go, standard library `net/http`, `pgx` driver, `goose` migrations |
| API contract | OpenAPI 3 first: `oapi-codegen` generates the Go server interface, `openapi-typescript` the web client types |
| Database | PostgreSQL, shared tables with `tenant_id`, Row-Level Security |
| Frontend | React + Vite + TypeScript; own CSS design system; headless accessible primitives for complex widgets |
| Sessions | `HttpOnly` + `SameSite` cookies, argon2id passwords, TOTP 2FA for privileged roles |
| Documents | DOCX templates filled by our own Go library; PDF via a LibreOffice converter (Gotenberg) |
| Deployment | One Go binary that embeds the built frontend; Docker Compose with app, PostgreSQL and Caddy (automatic HTTPS); install script |
| Quality | `go test` with a real PostgreSQL (testcontainers), Vitest, Playwright; `govulncheck`, `pnpm audit`, OWASP ZAP in CI |

### Tenant isolation rules

1. Every tenant-owned table has `tenant_id` and `ENABLE` + `FORCE ROW LEVEL SECURITY`.
2. The application connects with a role that does not own the tables and has no `BYPASSRLS`.
3. The tenant is set per transaction with `set_config('app.tenant_id', <id>, true)` (equivalent to `SET LOCAL`),
   which is safe with PgBouncer in transaction mode.
4. Policies fail closed: with no tenant set they match zero rows.
5. A municipality reading its schools will get its own explicit policy when that module exists.

## Alternatives considered

- **Django + PostgreSQL + HTMX.** Strong secure defaults and an admin panel; rejected in favour of the owner's
  choice of Go + TypeScript. Cost of our choice: we write permissions, forms and back-office screens ourselves,
  so access-control and IDOR tests are mandatory from the first endpoint.
- **Schema per tenant (`django-tenants` style).** Rejected: every migration runs once per schema and can stop
  half-way, cross-school reports for municipalities need queries over many schemas, and `search_path` is
  session state that conflicts with transaction-mode pooling.
- **SQLite.** Rejected: single writer and single server, no RLS for tenant isolation.
- **MySQL / MariaDB.** Widely known locally, but without RLS isolation would depend only on application filters.
- **Reusing the TypeScript backend of `traslado-programado-sys` (Hono).** Larger npm dependency surface for the
  backend; we keep TypeScript only on the frontend.

## Consequences

- One static binary simplifies on-prem installs for municipal IT teams.
- `sqlc` (typed SQL) is planned but needs cgo; until it runs in a container, queries use `pgx` directly.
- The DOCX filler is our own code and needs tests with real Word-generated templates (Word splits text into runs).
