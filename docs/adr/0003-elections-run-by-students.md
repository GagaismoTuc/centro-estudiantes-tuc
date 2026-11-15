# ADR 0003 — Elections are run by students through the Electoral commission

Status: accepted · Date: 2026-11-14

## Context

`docs/spec/mvp.md` (sections 2 and 4.2) has the school configuring elections. In the elections grilling (#28)
the owner reversed that, verbatim:

> "No, la idea es que los alumnos sean mas independientes de las burocracias de los colegios, por lo que un Presidente de Centro de estudiantes/Junta debe poder crear esas cosas." (S1)

> "tienes razon, deberia haber tambien una autoridad imparcial que serian las autoridades del colegio pero ese perfil quedaria optativo en casos de que haya problemas con los centros de estudiantes. Aun asi quiero que se crea, configura y anula la junta electoral. La comisión directiva no la administra." (S2)

## Decision

Students run elections. The Electoral commission creates, configures and annuls an Election; the Board never
administers it. The outgoing Officers designate the commission, and its members cannot be Candidates.

The School only bootstraps and, optionally, oversees:

- **Bootstrap:** with no Officers, the School admin (or the Veedor) designates the first Electoral commission,
  and the School admin assigns Officers by hand only before any Election or when an Election had no winner.
  Otherwise the winning List becomes the Board automatically when the result is published.
- **Oversight:** an optional Veedor, separate from the School admin, sees live counts, can annul an Election
  with a public reason and designates the commission when the Student center is headless or in conflict. The
  Veedor cannot create or configure an Election nor approve Lists.

This supersedes the "configured by the school" wording in `docs/spec/mvp.md`.

## Considered Options

- **School admin runs elections** (the original MVP spec). Rejected: it ties the Student center to school
  bureaucracy, the opposite of what the owner wants (S1).
- **The Board runs its own re-election.** Rejected: the outgoing Officers would administer the vote that
  decides whether they stay; the owner ruled it out ("La comisión directiva no la administra", S2), so they
  only designate the commission.
