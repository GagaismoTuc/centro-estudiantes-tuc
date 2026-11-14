# Coding standards

Judgment rules for writing code in this repo. Architecture decisions and their reasons live in `docs/adr/`; product and privacy rules live in `AGENTS.md`. Each rule lives in one file only.

## Both sides

1. **Contract first.** An API change starts in `openapi/openapi.yaml`; regenerate with `go generate ./...` (in `api/`) and `pnpm gen:api` (in `web/`). Never hand-edit `api/internal/oapi/api.gen.go` or `web/src/api/schema.gen.ts`; CI fails when they drift.
2. **Test first.** For a behavior change, write the failing test, see it fail for the expected reason, then make it pass. One test per rule, asserting every observable effect (status, body, stored rows), through the public interface.
3. **Machine-readable errors.** The API returns stable error codes (`{"error": "<code>"}`, `RowError.code`); the web maps codes to Spanish text. Never match on English text.
4. **Smallest correct change.** No speculative abstraction, dependency or option. A known limit gets a `shortcut: <limit>, <when to upgrade>` comment.

## Go API

5. **Tenant isolation through `db` only.** Tenant-owned queries run inside `db.WithTenant` (or `db.WithUser` / `db.WithCodeHash` where ADR 0001 allows). Never set `app.tenant_id` by hand, never connect with the owner role at runtime, and every new tenant-owned table gets RLS and composite foreign keys per ADR 0001.
6. **Standard library first.** `net/http`, `pgx`, `goose`; a new module needs a reason in the PR.
7. **Tests use real PostgreSQL** (testcontainers) and run with `-race`. A migration ships with its `Down` and a test that applies it.
8. **Logs never carry secrets or personal data:** no tokens, codes, passwords, names or DNI.

## Web

9. **Accessible by default.** Labels on every field, errors announced (`role="alert"` or `aria-live`) and tied with `aria-invalid` + `aria-describedby`, focus moved to the first invalid field, never left on `body`.
10. **No native English UI.** Forms use `noValidate` with the shared Spanish validation in `components/Form.tsx`; no `window.confirm`, use `ConfirmDialog`.
11. **Works at 360 px.** No horizontal overflow on any screen; grid columns use `minmax(0, 1fr)`.
12. **Follow Modern Web Guidance** (`npx modern-web-guidance@1.0.0`) for forms, dialogs, tables and dark mode; name the guide ids used in the PR.
