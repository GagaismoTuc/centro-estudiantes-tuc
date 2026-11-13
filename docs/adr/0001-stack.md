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
| Frontend | React + Vite + TypeScript; own CSS design system; headless accessible primitives for complex widgets; UI patterns follow [Modern Web Guidance](https://github.com/GoogleChrome/modern-web-guidance-src) (pinned CLI `modern-web-guidance@1.0.0`) |
| Sessions | `HttpOnly` + `SameSite` cookies, argon2id passwords, TOTP 2FA for privileged roles (own RFC 6238 code over stdlib crypto) |
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
6. Only `db.WithTenant` writes `app.tenant_id`, only `db.WithUser` writes `app.user_id` and only `db.WithCodeHash`
   writes `app.code_hash`; no other application code may call `set_config` on them, because RLS trusts whatever
   the transaction declares. `app.user_id` only grants read access to the user's own memberships (needed to pick
   the tenant at sign-in). `app.code_hash` (hex of the HMAC of an access code) only grants SELECT and UPDATE on the
   one `access_codes` row with that hash, so a code can be claimed before a tenant is known. Inside
   `db.WithCodeHash`, once that row is found, `db.SetTenant` may declare the row's tenant for the same transaction
   (to write the new membership); `db.SetTenant` is not called anywhere else except by `db.WithTenant`.
7. Migrations refuse to run if `ce_app` already exists with `SUPERUSER` or `BYPASSRLS`.
8. `organizations` is platform-level: the app role can read it across tenants (names and hierarchy only) and insert
   into it (bootstrap). Before adding sensitive columns to it, give it its own RLS policy.
9. Two connection strings: `DATABASE_URL` (login role member of `ce_app`, used by the app) and `MIGRATE_DATABASE_URL`
   (table owner, used only to migrate). The server refuses to start if the app role is superuser, has `BYPASSRLS`
   or is not a member of `ce_app`.
10. Foreign keys between tenant-owned tables include `tenant_id` (composite), because FK checks bypass RLS.

### Emailed token rules

1. Verification, sign-in and password-reset links carry a token of 32 random bytes; only its SHA-256 is stored
   (`email_tokens`, platform-level like `sessions`). Each token has one purpose and works only for it.
2. Tokens are single-use and short-lived (verify 48 h, sign-in 15 min, reset 30 min). Issuing one marks the
   user's previous unused token of the same purpose as used. The app role may only insert tokens and set
   `used_at`; it cannot delete them or change anything else.
3. The token travels in the URL fragment (`/ingresar#token=…`), so it never reaches server logs, proxies or
   `Referer`. It is never logged, not even when sending fails.
4. Requests for a link answer the same whether or not the account exists, and are rate limited per IP and per
   email; invalid tokens are rate limited per IP. Mail is sent after the transaction commits, off the request
   path, and a failed send never undoes the change.
5. A password reset revokes every session of the user. One password rule (10 to 128 characters) applies to
   bootstrap, claim and reset.

### Two-factor authentication rules

1. `school_admin` and `officer` must use TOTP (RFC 6238: HMAC-SHA1, 6 digits, 30-second step), implemented in
   `internal/totp` on top of the standard library's HMAC and AES; we never write cryptographic primitives.
   Seeds are 20 random bytes, shown once, and stored only encrypted with AES-256-GCM under `TOTP_ENC_KEY`
   (exactly 32 bytes, kept out of the database) with a random nonce and the user id as additional data, so a
   ciphertext copied to another user's row does not decrypt. The server refuses to start without the key.
2. A code is accepted for the steps now−1, now and now+1, and only if its step is later than the last
   accepted one (`user_totp.last_used_step`, updated under a row lock), so every code works once.
   Recovery codes (10 per enrollment, 50 bits each) are single-use and stored as SHA-256 only.
3. Wrong, replayed and used codes get the same `invalid_code`; failures are limited per user (5 per
   15 minutes, shared by verification and enrollment confirmation).
4. Proving the second factor (enrollment confirmation or verification) rotates the session: a new token and
   cookie with `sessions.mfa_at` set, and the old session row deleted. `mfa_at` is never updated in place
   (`ce_app` has no UPDATE on it).
5. The two-factor state is computed per request from the role in the active tenant. Every operation reserved
   to `school_admin` or `officer` calls `auth.RequireMFA` right after its role check (today
   `roster.Store.asAdmin`) and answers 403 `mfa_required` without reading or writing anything.
6. A lost phone is handled by an operator with `admin reset-mfa`, which deletes the TOTP, the recovery codes
   and every session of the user.

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
