# ADR 0002 — One person can hold several roles in one school

Status: accepted · Date: 2026-11-14

## Context

Today a person has one membership per school with exactly one role: `memberships` has
`UNIQUE (tenant_id, user_id)`, and nothing assigns `officer` yet. Elections (#28) need the same person to be,
at once, a Student who votes, a member of the Electoral commission, and an Officer with a Position. The owner
accepted this in the elections grilling (A23, 2026-11-14).

## Decision

Roles in a school are not exclusive: one person can hold several roles in the same school at the same time.
This replaces "one role per membership". The right to vote still belongs to the Roster member, never to an
account or a role, so Officers and commission members vote as Students and cannot vote twice through a second
role (A12).

## Consequences

- One-way door: it changes the `memberships` uniqueness and every role check in `api/` and `api/migrations`.
  Where code reads "the role" of a user in a tenant, it must ask "has this role" instead.
- The two-factor requirement (ADR 0001) applies when any of the person's roles in the active school is
  privileged: School admin, Officer, Electoral commission member or Veedor (A20).
