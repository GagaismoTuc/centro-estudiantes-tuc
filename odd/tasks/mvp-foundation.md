# Feature: mvp-foundation

Objective: write the MVP specification and stack ADR, scaffold the repo (Go API, React web, OpenAPI contract) and prove tenant isolation with PostgreSQL Row-Level Security.
Branch: `feat/mvp-foundation` · Delivery: ask-on-risk, `chain_strategy=feature-branch-chain` (forecast ~900 authored lines) · Runners: `go test ./...` (api/, Docker required for Postgres tests), `pnpm test` (web/).

## Specs

- **S1 — Stack.** User: "Podemos hacer el backend con Go y frontend con typescript". Agreed in conversation: PostgreSQL with shared tables + `tenant_id` + Row-Level Security; OpenAPI contract-first; React + Vite + TypeScript frontend; Docker Compose deployment, usable as SaaS or on-prem. Recorded as an ADR in `docs/adr/0001-stack.md`.
- **S2 — Tenant isolation.** A school (tenant) never reads or writes another tenant's rows, enforced by the database even if application code forgets a filter. With no tenant set, queries return zero rows (fail closed). The application connects with a role that does not own the tables and lacks `BYPASSRLS`; tables use `FORCE ROW LEVEL SECURITY`; the tenant is set per transaction (`set_config(..., true)` / `SET LOCAL`). Check: Go integration tests against a real PostgreSQL container.
- **S3 — MVP specification document** (`docs/spec/mvp.md`): vertical = centros de estudiantes (finances, delegate elections/voting, formal note templates); municipal indicators later on the same base. Must include:
  - Login, user's words: "Casi todos tienen telefono, mail, o una computadora en la casa. La opcion de cargar el padron y entregar el codigo obviamente debe estar."
  - Notes output, user's words (PDF and Word): "Idealmente ambas."
  - Voting, user's words: "Cada uno desde su celular"; election validity: "eso dependerá de la politica de cada escuela, para nosotros es una funcionalidad del sistema para ellos puede ser otra cosa."
  - Libraries, user's words: "Respecto a las librerias, podriamos crearlas nosotros cuando haga falta."
  - Interface: "editable, escalable, facil de usar, con una interfaz facil de entender pero que no pierda lo clean (te pasare referencias luego)."
- **S4 — Skeleton.** `api/` (Go module, stdlib `net/http`, `pgx`, `goose` migrations, OpenAPI-generated server), `web/` (React + Vite + TS, typed client generated from the same OpenAPI file), `openapi/openapi.yaml`, `compose.yaml` with PostgreSQL for development. Check: `GET /api/health` returns `{"status":"ok"}` via a Go test; web unit test passes; `go vet`, `tsc` clean.

## Tasks

- T1 — S1, S3 — inline — docs: MVP spec + ADR (README moves to T4) — done (commit below)
- T2 — S4 — inline — OpenAPI contract + Go API skeleton with health endpoint (test-first) — done: RED `status = 404, want 200`, GREEN `go test ./...` ok, `go vet` clean
- T3 — S2 — gentle-ai-worker — migration organizations/roster_members + RLS, WithTenant helper, 11 isolation tests — done: RED `setup: migrate: not implemented` (9 fail), GREEN `go test -count=1 ./...` ok (Postgres 18 container)
- T4 — S4 — gentle-ai-worker — web skeleton (React+Vite+TS, openapi-fetch typed client), compose.yaml, README — done: RED `Unable to find an element with the text: API: ok.` (4 fail) → GREEN `Tests 4 passed (4)`, typecheck/build ok, `docker compose config` ok

## Log

- **L1 (2026-11-13, original request, verbatim):** "La idea es hacer un sistema para vender a municipios o escuelas/colegios (todavia no elegimos si hacer una suscripcion o vender el sistema). Quiero que pueda cumplir con estas ideas principales:
Las ideas principales de ambos audios se centran en una propuesta de negocio y gestión para desarrollar y vender software o herramientas digitales orientadas al ámbito público y educativo:Desarrollo de un producto escalable para municipios: Se sugiere crear un sistema de gestión o control de calidad (similar a un programa que mide métricas de gestión pública como árboles plantados, basurales erradicados o calles pavimentadas) que pueda ser adaptado y vendido a distintos municipios.   Adaptación para centros de estudiantes: Se propone expandir la idea hacia el ámbito escolar desarrollando un software para centros de estudiantes. Este programa incluiría funciones para organizar finanzas, gestionar elecciones y votaciones de delegados, y ofrecer modelos de notas formales para presentar a directivos o funcionarios.   Ofrecimiento de contactos y asesoramiento político: El hablante se ofrece a ayudar a armar la presentación del proyecto desde un enfoque político/comercial y a gestionar reuniones con contactos clave para concretar ventas, mencionando específicamente a una intendenta, a Melina Morgenstein (Secretaria de Juventud, organizadora de foros estudiantiles) y a la Comisión de Juventudes de la Legislatura.   

Tiene que poder ser, editable, escalable, facil de usar, con una interfaz facil de entender pero que no pierda lo clean (te pasare referencias luego). 
Es para colegios en Argentina, Tucuman.
Ayudame a elegir el stack primero.

El repo crealo en mi organizacion de github GagaismoTuc en un repo nuevo."
- **L2 (2026-11-13):** Repo created private at GagaismoTuc/centro-estudiantes-tuc, branch `main`. MVP vertical confirmed ("ok me parece bien"). Hosting: self-served (VPS / free tier pilot, on-prem for municipalities).
- **L3 (2026-11-13):** User review rejected schema-per-tenant (migrations ×N, cross-tenant reports, pooling) in favour of shared tables + RLS; secret ballot needs timing decoupling (no timestamps, random ids, shuffled batches); no formal pentest before first sales; no offline mode needed ("Cada uno desde su celular").
- **L4 (2026-11-13):** User chose "Podemos hacer el backend con Go y frontend con typescript" and answered login/notes questions (S3). Authorized: "Sí, arrancá" — spec + ADR + skeleton + first RLS isolation test, work-unit commits on the feature branch, no push.
- **L5:** Decision: no sqlc in this slice (needs cgo/gcc, unavailable on this Windows machine); plain `pgx` queries until sqlc runs via Docker in a later task.
- **L6 (2026-11-13):** T3 commit 7aa885a assessed high (agent escalation, item 2 security; native tier medium, review not due: under budget). Independent gentle-ai-verify: all S2 clauses PASS, no blockers. Follow-ups applied in commit below: rollback, UPDATE-moves-tenant and malformed-setting tests; migration refuses a pre-existing `ce_app` with SUPERUSER/BYPASSRLS (RED `Migrate succeeded with ce_app BYPASSRLS, want an error` → GREEN); ADR rules 6-8 (only WithTenant sets the tenant; organizations readable across tenants by design).
- **L7 (2026-11-13):** The harness blocks writing any `.env*` path, so `.env.example` was not created; README tells the user to create `.env` with `POSTGRES_PASSWORD`.
- **L8 (2026-11-13):** All tasks done. Assessments: 309bd50 passive; bd62591 medium (deferred to slice); 7aa885a high by escalation → independent verify PASS; ef20cea follow-ups; 52a14e1 medium, review not due (under budget). Chain slices (feature-branch-chain, not pushed): PR1 = 309bd50..bd62591 (docs + API skeleton); PR2 = 7aa885a..ef20cea (RLS tenant isolation, high risk, alone); PR3 = 52a14e1 (web + compose + README). Next: push + PRs only when the user decides; then auth (roster codes + email) per docs/spec/mvp.md.
- **L9 (2026-11-13):** User: "Dale, cuando termines las instalaciones anda con los pr nomas y empeza." GCC 16.2 (WinLibs UCRT, SHA256 verified) installed at G:\tools\mingw64 and added to the user PATH; `go test -race` works. Modern Web Guidance source cloned at G:\tools\modern-web-guidance-src; CLI pinned `modern-web-guidance@1.0.0`. Force-pushing the tracker was blocked by the safety policy, so the tracker is a new branch. PRs: #1 tracker (draft, main ← feat/mvp-foundation-tracker), #2 docs (← feat/mvp-foundation), #3 api (← -01-api), #4 RLS (← -02-rls, ~690 lines, kept whole: security unit with its tests), #5 web (← -03-web).
