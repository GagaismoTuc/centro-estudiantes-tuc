# ADR 0004 — Due payments are recorded per Roster member

Status: accepted · Date: 2026-11-14

## Context

`docs/spec/mvp.md` (section 6) asks for compliance with Argentine Law 25.326 for minors' personal data, and
`AGENTS.md` notes that the Roster holds minors' personal data. In the Finanzas grilling (#29) the planner
recommended tracking Dues only in aggregate. The owner reversed that, verbatim:

> "preferiria que si se guarde el dato personal de quien pago y quien no (aca ignorar lo de la ley de datos porque no aplica)." (Q5)

## Decision

Each Due payment and each Exemption is recorded against one Roster member. Visibility (S8):

- The Board, the School admin and the Veedor see the full list of who paid and who owes.
- Each Student sees only their own status.
- Other Students see payment Entries without the name.
- The Financial accountability report shows only totals per Due ("312 de 470 pagaron"), never names.
- An Exemption requires a reason, and only those who see the full list can see it.

This supersedes `docs/spec/mvp.md` section 6 only in its Law 25.326 objection to storing who paid; encrypted
backups and the rest of section 6 still apply.

## Considered Options

- **Aggregated dues income with no per-student data.** Rejected by the owner: the Student center needs to
  know who has not paid (S5).
- **A list visible to every Student.** Rejected (Q8): it exposes minors as debtors to their classmates.

## Consequences

- One-way door: it stores personal data about minors linked to money.
- The statement that the law objection does not apply is the owner's decision, not a legal review.
- The handling rules in `AGENTS.md` still apply: payment status never goes to logs, error messages, committed
  fixtures with real data or published screenshots.
- Only those who can see the per-student list can export it (A18).
