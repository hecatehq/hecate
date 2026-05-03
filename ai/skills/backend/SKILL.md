---
name: hecate-backend
description: Use when working on the Hecate Go backend — gateway, agent runtime, providers, sandbox, storage. Keeps backend work aligned with Hecate's "operator-grade control plane, runtime-aware" thesis.
---

# Hecate backend skill

Use this skill for any work outside `ui/`. The React UI has its own skill at [`../ui/SKILL.md`](../ui/SKILL.md). For the `internal/providers/` package specifically, also reach for [`../providers/SKILL.md`](../providers/SKILL.md) — it owns the api↔providers boundary and the seven-step "add a wire field" chain.

## Canonical guidance lives here

Don't duplicate. This skill is the backend lens; the rules themselves live in:

- [`../../core/project-context.md`](../../core/project-context.md) — repo layout, rings, storage tiers, toolchain pins, risky areas.
- [`../../core/engineering-standards.md`](../../core/engineering-standards.md) — field-shape rules, parallel-struct rule, anti-patterns.
- [`../../core/workflow.md`](../../core/workflow.md) — operating loop, planning triggers, commit etiquette.
- [`../../core/verification.md`](../../core/verification.md) — verification ladders, race-suite floor, done criteria.

## Product lens

The backend should feel like:

- A single-process gateway control plane.
- A deny-by-default policy enforcer.
- A runtime-aware proxy that explains its decisions.
- A debugging surface — every request leaves a trace, every cost is itemized, every approval is logged.

It should not feel like:

- A thin pass-through with marketing on top.
- A configurable framework where you bring your own everything.
- A research demo that works in one provider's happy path.

Default to operator confidence: clear status, clear errors, deterministic state, no surprises on restart.

## Engineering thesis

Calm, durable, and explicit. Code should age well — the runtime is supposed to live for years, not iterations.

Prefer one gateway process, one port, embedded UI (`//go:embed ui/dist`); deterministic startup with env-driven config; backend tier choice surfaced as a config knob, never inferred; explicit error wrapping with cause chains; standard library first, well-known third party second, novel deps last.

## Operator priorities

Every endpoint, every config knob, every error message should answer:

1. What did the gateway just decide?
2. Why did it decide that?
3. What did it cost / how long did it take?
4. What happens if it fails next time — retry, fallback, fail?
5. How do I find the trace for this in OTel?

When choosing between "elegant" and "operationally explicit," choose explicit.

## Hecate-specific backend rules

- **Auth is a path-level decision.** `/v1/chat/completions` accepts tenant API keys; `/admin/*` requires admin bearer. `/v1/tasks/*` accepts both. Don't blur these.
- **Tenant scoping is automatic.** Once a request has a tenant principal, every subsequent store query gets `WHERE tenant = ?` injected. New endpoints must respect this — never bypass via the admin path.
- **Sandbox is per-call subprocess, applied inline.** Shell, file, git tool calls spawn a fresh `sh` from inside the gateway after policy validation + env sanitisation + output cap + wall-clock timeout. On Linux with `bwrap` installed and on macOS, the call is additionally wrapped by `bwrap` / `sandbox-exec` for fs+net confinement (auto-detected at startup, exposed on `/healthz` under `sandbox.os_isolation`). No separate sandbox daemon, no per-call rlimits — operators who want CPU/FD/memory caps run the gateway under systemd or in a container with `--cpus` / `--memory` flags. New tools follow the same `internal/sandbox/` shape.
- **Approvals are blocking.** Pre-execution and mid-loop approvals halt the run; the run record persists in `awaiting_approval` until resolved. New gates use the same `TaskApproval` shape.
- **Events are appended, not mutated.** Every state transition writes a `run_event` with a monotonic sequence. The SSE stream replays from `after_sequence`. New event types must follow the event-protocol v1 taxonomy (`run.*`, `turn.*`, `tool.*`, `policy.*`, `gap.*`, `error.*`) and be documented in `docs/events.md`.
- **Cost is in micro-USD.** All money is `int64` in micro-USD (`1_000_000` = $1). Never `float64` for money — pricebook lookups, budgets, ledger entries all stay integer.
- **OTel is first-class.** Every request gets a trace ID surfaced in the response header (`X-Trace-Id`) and persisted on the run record. New code paths add spans, not just log lines.

## Backend recipes

### Add a passthrough field end-to-end

The seven-step chain spans `pkg/types/` → `internal/api/` → `internal/providers/` and tests at every layer. Canonical version: [`../providers/SKILL.md`](../providers/SKILL.md). Forgetting to plumb the field into the streaming `wireReq` is the most common bug.

### Add an MCP tool

`internal/mcp/tools.go`:

1. Append a `s.RegisterTool(...)` call in `RegisterDefaultTools` with `Annotations` set (`ReadOnlyHint`, `DestructiveHint`, `IdempotentHint` as appropriate).
2. Add a `<name>Handler` returning `ToolHandler` further down.
3. Update the `docs/mcp.md` tool table.
4. Tests in `internal/mcp/tools_test.go` using the `fakeGateway` helper.

### Add a persisted run-event type

1. Pick an event-protocol name from the existing taxonomy before adding a new dotted name. Prefer generic families such as `tool.*`, `policy.*`, `gap.*`, and `error.*` with specific details in `data` over subsystem-specific names.
2. `internal/orchestrator/runner.go` → call `r.emitRunEvent(ctx, taskID, runID, "your.event.type", ..., extraDataMap)` at the right life-cycle moment. Emit the event **before** handing off to the queue — see the emit-before-enqueue gotcha above.
3. Document the event and its payload in `docs/events.md`.
4. If high-cardinality, wire into `internal/retention/retention.go` as a new subsystem (see `turn_events` for the pattern).

### Add a start-time validation error (HTTP 422)

For errors that should surface before a run is created (bad config, missing required field):

1. Define a sentinel error in `internal/orchestrator/runner.go`: `var ErrMyThing = errors.New("my_thing")`.
2. Return it (wrapped is fine; use `errors.Is`) from `startTaskWithOptions` before any run is created.
3. In `internal/api/handler_tasks.go` `HandleStartTask`, add an `errors.Is(err, orchestrator.ErrMyThing)` branch that returns `apiError(http.StatusUnprocessableEntity, "my_thing", err.Error())`.
4. Add the error code to `internal/api/error_mapping.go` if it has an OTel span status implication.
5. Test via `tasks.mustRequestStatus(http.StatusUnprocessableEntity, ...)` in `internal/api/server_test.go`.

## Test helper cheat-sheet

| Helper | File | Use for |
|---|---|---|
| `testRoundTripperFunc` | `internal/providers/provider_test_helpers_test.go` | Stub HTTP transport for provider tests |
| `newAnthropicTestProvider` | `internal/providers/tooluse_test.go` | Anthropic provider with cached caps (skips discovery) |
| `newTestHTTPHandler` / `*WithConfig` / `*ForProviders` | `internal/api/server_test.go` | In-process gateway handler |
| `fakeUpstreamCapturing` | `e2e/gateway_test.go` | E2E: capture what gateway forwarded to upstream |
| `hecateServer` | `e2e/gateway_test.go` | E2E: spawn the real binary on a free port |
| `startHecateProcess` | `e2e/ollama_test.go` | E2E: shared gateway binary for the Ollama suite (TestMain-driven) |
| `autoPreconfiguredEnv` | `e2e/gateway_test.go` | Inject `PROVIDER_<NAME>_PRECONFIGURED=1` for every `PROVIDER_<NAME>_*` env var; both spawn helpers call it so test sites don't repeat the gate |

## Backend gotchas

- **Emit run events before enqueue, not after.** The in-memory queue dispatches synchronously: calling `enqueueRun` can cause a worker to claim the job and emit `run.started` before `run.queued` is persisted if the emit comes after. Always write the transition event first, then hand off to the queue (see `StartTask` in `internal/orchestrator/runner.go`).
- **modernc/sqlite TIME-as-text format** — the driver writes `time.Time` using Go's default `time.Time.String()` format (`2026-04-28 02:37:38.4524 +0000 UTC`), which doesn't lex-compare with RFC3339Nano cutoffs and breaks the retention sweep silently. Always write timestamps as `t.UTC().Format(time.RFC3339Nano)` explicitly when the column is TEXT (see `internal/taskstate/sqlite.go` `AppendRunEvent`).
- **Capability cache seeding** for provider tests — see [`../providers/SKILL.md`](../providers/SKILL.md) for the snippet. Without it the discovery path panics on a nil request body.
- **Pricebook preflight** — cloud-kind providers in tests trigger a pricebook lookup. `PROVIDER_FAKE_KIND=local` bypasses it for synthetic models in e2e.
- **Env-PRECONFIGURED gate for e2e providers** — env-supplied provider credentials (`PROVIDER_<NAME>_API_KEY` / `_BASE_URL`) only auto-import into the CP store when `PROVIDER_<NAME>_PRECONFIGURED=1` is also set. Both e2e spawn helpers funnel through `autoPreconfiguredEnv` so tests don't have to repeat it. New e2e helpers that bypass `hecateServer` / `startHecateProcess` need the same call; otherwise routed requests 400 with `no provider supports model …`.

## Done criteria

See [`../../core/verification.md`](../../core/verification.md). Race suite is the floor for runtime/backend work, not a nice-to-have.
