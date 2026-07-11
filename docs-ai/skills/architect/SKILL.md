---
name: hecate-architect
description: Use when planning a substantial change before coding — new wire fields, new persisted things, new endpoints, new persistent UI surfaces, cross-package refactors. Produces a structured plan, not code.
---

# Hecate architect skill

Plan-shaped responses for substantial changes. The skill produces a plan, not code. Use it when "I am about to make a substantial change; I need to think before I type."

## When to use

Any change that triggers a planning event in [`../../core/workflow.md`](../../core/workflow.md):

- Cross-package wire-field changes (the seven-step chain — see [`../providers/SKILL.md`](../providers/SKILL.md)).
- New Hecate-owned persisted things — normally mirror memory + SQLite + Postgres
  tiers. Portable Projects records belong in Cairnline instead; never add a
  second Hecate persistence tier for them.
- New HTTP endpoints (provider-compatible `/v1/...` or Hecate-native `/hecate/v1/...`).
- New approval policies or sandbox capabilities.
- New persistent UI surfaces (inspector, side rail, dashboard block, summary panel).
- Substantive refactors that cross ring boundaries or touch the api↔providers seam.

When in doubt, default to using this skill. The cost of a brief plan is far less than the cost of an unplanned change that hits the wrong seam.

## Bias

Plan first. Do not write code in this skill's response. Unless the task is trivial (typo fix, single-line dependency bump, comment touch-up), produce a plan first.

## Output

Use the canonical plan structure and anti-patterns in [`../../tasks/planning.md`](../../tasks/planning.md). That doc owns the format; this skill owns when to invoke it.

## Hand-off

The plan is the brief for a `hecate-backend`, `hecate-ui`, or `hecate-providers` skill turn. It should give the implementer everything they need to proceed without further context — including the verification ladder they'll run and the docs they'll update.

## Verification expectations

None for the plan itself. The plan must enumerate the verification ladder for the implementer.
