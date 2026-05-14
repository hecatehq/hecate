---
name: hecate-devops
description: Use when reviewing delivery safety — env-var changes, schema migrations, deploy risk, rollback paths, observability surfaces, release notes.
---

# Hecate devops skill

Delivery-readiness review. Surfaces risk explicitly.

## When to use

Anything with a CI/CD, environment, deploy, or migration footprint:

- New env vars or changed defaults.
- Schema changes across storage tiers (memory + sqlite).
- CI/CD workflow changes.
- New public HTTP endpoints (downstream SDKs depend on them).
- Retention worker changes (new subsystem, changed cadence, retention windows).
- OTel surface changes (new spans, new metrics, new error codes).
- Release-note drafting and tag-cutting — see [`../../tasks/release.md`](../../tasks/release.md) for the procedure (snapshot dry-run, verification gate, footguns, recovery).

## Surfaces to check

- **CI/CD.** Which workflow files run? Will `paths-ignore` skip this change accidentally? Does the change need a `[skip ci]` marker, or does it require CI to actually run?
- **Environment.** New env vars must land in `.env.example` AND the relevant `docs/<feature>.md` env-var table — same change, not as a follow-up. Stale env-var docs cause more on-call pages than missing features.
- **Config compatibility.** Does an old config still boot? If not, that's a breaking change — needs a migration note in the commit body.
- **Schema migrations.** Which storage tiers are affected? Memory is rebuilt on boot (fine). SQLite needs a forward-compatible migration and roll-forward considerations. The retention worker subsystems (`traces`, `usage_events`, `audit`, `provider_history`, `turn_events`, `agent_chat_approvals`) must keep mirroring.
- **Deploy and release risk.** Is this safe to roll out behind a flag? Does it need a flag at all? What's the blast radius if it misbehaves?
- **Rollback.** Can this change be reverted cleanly? If a schema change is involved, is the rollback path documented?
- **Observability.** New code paths get OTel spans, not just log lines. Stable error codes for new failure modes (see `internal/api/error_mapping.go`). Trace IDs surfaced.
- **Release notes.** When relevant, draft them in the commit body so they're easy to lift later.

## Output shape

1. **Surfaces affected.** Env, schema, CI, observability — name each one explicitly.
2. **Rollout risk.** Blast radius if this misbehaves; blocking vs non-blocking; flag-gateable or not.
3. **Rollback path.** Can this revert cleanly? If a schema change is involved, is the rollback documented?
4. **Doc updates required.** `.env.example`, `docs/<feature>.md`, `docs/events.md`, `docs/runtime-api.md` — whichever apply.
5. **Draft release-note line.** When relevant.

## Bias

A devops review that says "looks fine" is suspect. Name the failure modes explicitly and what catches them. If nothing catches it, that's the finding. The point of the skill is to surface risk, not to bless.
