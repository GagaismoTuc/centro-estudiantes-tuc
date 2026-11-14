# Coding standards

Judgment rules for writing code in this repo. Architecture decisions and their reasons live in `docs/adr/`; product and privacy rules live in `AGENTS.md`. Each rule lives in one file only.

Tickets and reviews cite rules by ID (`CS-5`). The **Check** column says how `main` and reviewers verify a rule without rereading the code: `CI` means a CI job fails when it is broken; anything else is what the reviewer looks for in the diff or in `## Evidencia`.

## Both sides

| ID | Rule | Check |
|---|---|---|
| CS-1 | **Contract first.** An API change starts in `openapi/openapi.yaml`; regenerate with `go generate ./...` (in `api/`) and `pnpm gen:api` (in `web/`). Never hand-edit `api/internal/oapi/api.gen.go` or `web/src/api/schema.gen.ts`. | CI ("Generated code is up to date", "Generated client is up to date") |
| CS-2 | **Test first.** For a behavior change, write the failing test, see it fail for the expected reason, then make it pass. One test per ticket rule, asserting every observable effect (status, body, stored rows), through the public interface. | `## Evidencia`: one row per ticket rule with the RED message and the test name |
| CS-3 | **Machine-readable errors.** The API returns stable codes (`{"error": "<code>"}`, `RowError.code`); the web maps codes to Spanish text and never matches English text. | Diff: new error paths add a code to the contract enum and a test for it |
| CS-4 | **Smallest correct change.** No speculative abstraction, dependency or option. A known limit gets a `shortcut: <limit>, <when to upgrade>` comment. | Diff: no new module or option the ticket did not ask for |

## Go API

| ID | Rule | Check |
|---|---|---|
| CS-5 | **Tenant isolation through `db` only.** Tenant-owned queries run inside `db.WithTenant` (or `db.WithUser` / `db.WithCodeHash` where ADR 0001 allows). Never set `app.tenant_id` by hand or connect with the owner role at runtime; every new tenant-owned table gets RLS and composite foreign keys per ADR 0001. | Diff touches `riesgo` (`api/internal/db/**`, `api/migrations/**`): RDD or pi review with the `risk` lens; a cross-tenant test for each new table |
| CS-6 | **Standard library first.** `net/http`, `pgx`, `goose`; a new module needs a reason in the PR. | `api/go.mod` is `riesgo`: the PR names the reason |
| CS-7 | **Real PostgreSQL in tests** (testcontainers), always with `-race`. A migration ships with its `Down` and a test that applies it. | CI ("Test (race detector, real PostgreSQL)") |
| CS-8 | **Logs never carry secrets or personal data:** no tokens, codes, passwords, names or DNI. | Diff: every new log call lists only ids, codes and counts |

## Web

| ID | Rule | Check |
|---|---|---|
| CS-9 | **Accessible by default.** Labels on every field; errors announced (`role="alert"` or `aria-live`) and tied with `aria-invalid` + `aria-describedby`; focus moves to the first invalid field or the message, never stays on `body`. | Tests assert `document.activeElement` and `aria-invalid` |
| CS-10 | **No native English UI.** Forms use `noValidate` with the shared Spanish validation in `components/Form.tsx`; no `window.confirm`, use `ConfirmDialog`. | Diff: no `window.confirm`, no `required`/`pattern` without `noValidate` |
| CS-11 | **Works at 360 px**, light and dark. No horizontal overflow on any screen; grid columns use `minmax(0, 1fr)`. | Browser check at 360 px by `gentle-ai-verify` (ego-browser) for UI tickets |
| CS-12 | **Modern Web Guidance** (`npx modern-web-guidance@1.0.0`) for forms, dialogs, tables and dark mode. | PR names the guide ids used |
