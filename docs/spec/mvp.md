# MVP specification — Student centers (Centros de Estudiantes)

Status: draft · Market: secondary schools in Tucumán, Argentina · Owner: GagaismoTuc

## 1. Product

One multi-tenant platform sold to schools first and municipalities later. Each customer
(school or municipality) is a **tenant** and only enables the **modules** it contracted.
The MVP ships the student-center vertical; the municipal indicators module is built later
on the same base and must be able to aggregate data across the schools of a municipality.

The sale model (subscription or license) is still open, so the same code runs as:

- **SaaS**: one installation we operate, many tenants.
- **Dedicated install**: the same Docker Compose bundle on a customer's server, with an install script.

Product qualities requested by the owner, verbatim:

> "editable, escalable, facil de usar, con una interfaz facil de entender pero que no pierda lo clean (te pasare referencias luego)."

"Editable" means customers configure their own data, templates and election rules without code
changes. Visual references are pending; UI work waits for them.

## 2. Users and roles

| Role | Who | Can |
|---|---|---|
| Platform admin | Us | Create tenants, enable modules, support |
| School admin | Authority or staff of the school | Load the roster, issue access codes, configure elections and templates |
| Student-center officer | President, treasurer, secretary | Manage finances, notes and elections of their center |
| Student | Any student on the roster | Vote, read published information |

Officers and admins must use two-factor authentication.

## 3. Authentication

Owner's input, verbatim:

> "Casi todos tienen telefono, mail, o una computadora en la casa. La opcion de cargar el padron y entregar el codigo obviamente debe estar."

- The **roster (padrón)** is the source of truth for who is a student of a school.
- **Access code**: the school loads the roster and issues one single-use code per student, printable or distributable.
- **Email**: with the code, a student links an email once; afterwards they sign in with email + password or an emailed link.
- Voting rights belong to the **roster entry**, never to an email account, so a student cannot vote twice through two sign-in paths.
- SMS / WhatsApp sign-in is out of the MVP (per-message cost); possible paid add-on later.
- Transactional email goes through an external sending service with a free tier, not our own mail server.

## 4. Modules

### 4.1 Finances

- Income and expense entries per student center, with category, date, amount (ARS), description and optional receipt attachment.
- Entries are **append-only**: corrections are new reversing entries, never edits or deletes.
- Balance and period reports, exportable.

### 4.2 Elections and voting

Owner's input on usage, verbatim: "Cada uno desde su celular". Voting windows last hours or days;
a student whose signal drops retries later. No offline mode.

Owner's input on validity, verbatim:

> "eso dependerá de la politica de cada escuela, para nosotros es una funcionalidad del sistema para ellos puede ser otra cosa."

- Each election is configured by the school: candidates/lists, eligible roster, window, and **mode** (secret ballot or nominal vote). The result record states which mode was used.
- Secret ballot design:
  - The voter registry ("has voted") and the ballot box are separate.
  - Ballots carry no timestamp and use random identifiers (no sequential ids).
  - Ballots are written in randomly shuffled batches (every *k* votes or at close), so insertion time cannot be linked to a voter.
  - The voting endpoint keeps no access log that links a request to a vote.
- **Stated guarantee** (must appear in the documentation given to the school): the vote is secret from school authorities and tenant administrators. It is **not** secret from an operator with root access to the server while the election is open; cryptographic schemes (blind signatures, mixnets) are out of scope.

### 4.3 Formal notes

Templates for formal letters to school authorities or officials (requests, invitations, reports).

Owner's input on output formats (PDF and Word), verbatim: "Idealmente ambas."

- The template source is a **DOCX** file, so each school can use its own letterhead and format.
- Our own Go library fills placeholders such as `{{student_name}}` and produces the editable **DOCX**.
- A LibreOffice-based converter (Gotenberg container) turns that DOCX into **PDF**, so both outputs come from one template.

## 5. Libraries

Owner's input, verbatim: "Respecto a las librerias, podriamos crearlas nosotros cuando haga falta."

We write our own libraries for small, well-bounded needs (DOCX placeholder filling, roster CSV import).
We never write our own cryptography, password hashing or low-level session handling; those use the Go
standard library and `golang.org/x/crypto`. We do not build a PDF layout engine.

## 6. Non-functional requirements

- **Tenant isolation** enforced by PostgreSQL Row-Level Security (see ADR 0001), covered by automated tests.
- **Personal data of minors**: compliance with Argentine Law 25.326 (privacy policy, consent, encrypted off-site backups with tested restores).
- **Poor connectivity**: light pages, retry-safe requests; works on low-end phones.
- **Supply chain**: pinned dependencies, `govulncheck` and `pnpm audit` in CI, OWASP ZAP baseline scan.
- A formal external pentest is deferred until revenue or a customer requires it.

## 7. Out of scope for the MVP

- Municipal indicators module (next vertical).
- Offline operation and native mobile apps.
- SMS / WhatsApp sign-in.
- Attendance tracking.
