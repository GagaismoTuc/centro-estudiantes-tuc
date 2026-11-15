# ADR 0005 — A shared Contact directory across the Schools of an installation

Status: accepted · Date: 2026-11-14

## Context

ADR 0001 isolates every Tenant with Row-Level Security, and `organizations` is the only platform-level table
(rule 8). Besides each Student center's own Contact directory, the owner wants a directory shared among
Schools (Q16, verbatim: "si y b tambien."), with sharing optional per Contact (Q19, verbatim):

> "si pero cada escuela opcionalmente puede elegir si lo comparte o no, si decide no compartirlo solamente lo pueden ver ellos."

## Decision

- A Contact is private to its Student center by default.
- An Officer may share one Contact. It stays pending until the Platform admin approves or rejects it with the
  `admin` CLI (Q18 a). Only approved Contacts are visible: to the Officers of every School of the same
  installation, and read-only to their School admins and Veedors.
- Shared fields: name, Heading, phone/WhatsApp, email, zone/city and dated Reference prices. Never the
  center's notes, and never which School shared it (Q19).
- Any Officer can copy a shared Contact into its own Contact directory as an independent copy (Q20).
- The Platform admin removes a Contact from the Shared directory on the provider's request, and a center can
  withdraw a Contact it shared; existing copies stay (Q20, A21).
- Editing an own Contact does not change the shared version; sharing it again goes through approval (A22).
- A dedicated install shares only among its own Schools.

**Storage rule.** Shared directory rows are platform-level, like `organizations` (ADR 0001 rule 8). If the link
to the sharing Tenant is stored (for withdrawal), it is never readable by other Tenants. The app role reads
only approved rows across Tenants and only inserts share requests. Approval and removal happen only through
the `admin` CLI. Implementation details belong to the ticket.

## Considered Options

- **Per-center directory only.** Rejected: the owner wanted both (Q16).
- **Publish immediately and moderate on report.** Rejected (Q18): content written by minors would be read by
  minors before anyone reviews it.
- **Show the sharing School.** Rejected (Q19).

## Consequences

- It is the second exception to tenant isolation, so it needs its own RLS policy and isolation tests.
- The Platform admin needs a moderation queue in the `admin` CLI.
- Providers' contact data becomes visible across Schools, so removal on request must exist from the first
  ticket.
