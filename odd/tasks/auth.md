# Feature: auth

Objective: let schools load their roster and hand out access codes, and let students and staff sign in (email + password, emailed links, 2FA for privileged roles), per docs/spec/mvp.md §2–3.
Branch: `feat/auth` (stacked on `feat/mvp-foundation-03-web`) · Delivery: ask-on-risk, `chain_strategy=feature-branch-chain` (cached from mvp-foundation) · Runners: `cd api && go test ./...` (Docker), `cd web && pnpm test`.

## Specs

- **S1 — Sign-in paths.** User, verbatim: "Casi todos tienen telefono, mail, o una computadora en la casa. La opcion de cargar el padron y entregar el codigo obviamente debe estar." Two paths, both tied to the roster: school-issued access code (first time) and email (afterwards). SMS/WhatsApp out of scope.
- **S2 — Accounts and sessions.** Platform-level `users` (email, argon2id password hash); tenant-owned `memberships` (role `school_admin` | `officer` | `student`, optional roster member) under RLS. Server-side sessions: random token in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie, only its SHA-256 stored, with expiry; logout deletes it. Cross-origin state-changing requests rejected (`http.CrossOriginProtection`). Login failures return one generic message and are rate limited per IP and per email. Assumption: a CLI bootstraps the first organization and school admin.
- **S3 — Roster and access codes.** A school admin imports the roster from CSV (name, document number) and issues one single-use access code per roster member. Codes are shown once, stored hashed, expire, and issuing a new one revokes the previous. All rows tenant-isolated by RLS; looking up a code before sign-in must not expose other tenants' data.
- **S4 — Claiming an account.** A student enters code + email + password; the system creates the account linked to that roster member and consumes the code. One account per roster member; voting rights belong to the roster entry, never to an email. If the email already has an account, the student signs in first and then redeems the code.
- **S5 — Email.** Verification of the email address, sign-in by emailed link, and password reset, sent through an outbox interface; development logs messages instead of sending. Provider (free tier) chosen later.
- **S6 — Two-factor authentication.** TOTP required for `school_admin` and `officer`.
- **S7 — Screens.** Sign-in, claim code, roster import and code issuing, following Modern Web Guidance (`npx modern-web-guidance@1.0.0`). Final look waits for the owner's references ("te pasare referencias luego").

## Tasks

- T1 — S2 — gentle-ai-worker — users, memberships, sessions, password login/logout/me, bootstrap CLI — done (c53dd40; native review approved+acknowledged): RED `undefined: db.WithUser` + `relation "users" does not exist`, mutation checks (no CrossOriginProtection → 204 want 403); GREEN `go test -race -count=1 ./...` ok
- T2 — S3 — gentle-ai-worker — roster CSV import + access codes — pending
- T3 — S4 — gentle-ai-worker — claim flow — pending
- T4 — S5 — gentle-ai-worker — outbox, email verification, magic link, password reset — pending
- T5 — S6 — gentle-ai-worker — TOTP 2FA for privileged roles — pending
- T6 — S7 — gentle-ai-worker — web screens — pending
- T7 — S2 — inline — rate limiter behind a trusted proxy (`X-Forwarded-For` only from configured proxies, per-IP limit sized for school NAT), expired-session cleanup, hidden password prompt in CLI; verify follow-ups: precompute dummy hash, strict login JSON (unknown fields / trailing data → 400), delete the request's old session on re-login, CheckAppRole rejects table owners, admin CLI runs CheckAppRole, document per-email lockout DoS; native review advisories: limiter must not count 500s as failures, /auth/me must not return tenantId without a role, httpapi tests must be re-runnable on the same container (no fixed emails/IDs collisions), CLI usage tests should not need Docker — pending

## Log

- **L1 (2026-11-13, verbatim):** "Dale, cuando termines las instalaciones anda con los pr nomas y empeza." Scope taken from docs/spec/mvp.md §2–3 and the user's earlier answer quoted in S1. Every task is security-sensitive (Risk item 2): each work-unit commit gets an independent verify.
- **L2 (2026-11-13):** T1 deviations accepted: two DSNs (app role vs owner) and startup role check; app role may INSERT organizations (bootstrap); read-only `memberships_own_user` policy on `app.user_id` set only by `db.WithUser`; composite FKs with tenant_id (FK checks bypass RLS); argon2id m=19MiB t=2 p=1 (OWASP). ADR rules 6, 8-10 updated. Open items moved to T7. ~1680 authored lines (870 tests): single security unit, over the 400 heuristic.
- **L3 (2026-11-13):** c53dd40 assessed high (native high + escalation). Independent verify: all S2 clauses PASS, no blockers; follow-ups folded into T7. Native review due (high_risk) — started per RDD.
- **L4 (2026-11-13):** Native review lineage review-ec62dfbaa06326a1 (base 439f78f, lenses risk+reliability) approved with 8 non-blocking advisories; acknowledged (authority burned). Advisories folded into T7.
