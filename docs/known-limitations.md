# Known Limitations

Hecate is public-alpha software. This page is the plain-language list of what
operators should not assume yet.

## API And Schema Stability

- Public APIs are designed to be stable, but pre-1.0 changes are still possible.
- Persisted SQLite/Postgres schemas are young. Back up data before upgrading.
- There is not yet a dedicated migration CLI or rollback workflow.
- Single-node operation is the primary tested deployment shape. Shared Postgres
  state exists, but multi-node operational guidance is still thin.

## Provider Lifecycle

- Operators add providers explicitly from the built-in preset catalog (or the
  Custom OpenAI-compatible flow); none are auto-added.
- Credentials, base URLs, defaults, and pricebook entries are managed through
  the persisted control plane. Taking a provider out of rotation is done by
  deleting it — there is no enable/disable toggle.
- Custom clients are supported separately: external callers can use Hecate's
  OpenAI-compatible or Anthropic-compatible endpoints without requiring a custom
  provider record.
- Provider model discovery depends on each upstream's OpenAI-compatible or
  Anthropic-compatible catalog behavior; local runtimes can differ.

## Pricing And Budgets

- Pricebook rows can be imported or edited, but billing-critical deployments
  should verify pricing against provider invoices.
- Unknown cloud-model prices fail closed by default so operators do not
  silently run unpriced traffic.
- Local models can be zero-cost or manually priced, but host/GPU cost is not
  automatically measured.

## Semantic Cache

- Semantic cache is disabled by default.
- Semantic cache supports `memory` and `postgres`; SQLite vector search is not
  supported by the pure-Go SQLite driver used by Hecate.
- The local simple embedder is useful for development and alpha experiments,
  not a semantic-quality benchmark.
- Cache hits are optimization hints, not correctness guarantees. Operators
  should keep exact/semantic cache behavior visible in traces for important
  workloads.

## Task Runtime And Sandbox

- `agent_loop` and MCP integration are alpha. They are useful for controlled
  workflows, but the behavior surface is still expanding.
- `agent_loop` tasks require a model to be configured — either via
  `requested_model` on the task or the gateway's default model. A missing model
  is caught at start time and returns a 422 `model_not_configured` error; the
  run is never created. There is no runtime check that the configured model
  actually supports tool-calling until the loop's first LLM call.
- Runs that are stuck in `running` state (e.g. after a worker crash or process
  restart) are recovered automatically by the periodic reconciler and re-queued
  without operator intervention. The recovery window is three times the
  configured lease duration (`GATEWAY_TASK_QUEUE_LEASE_SECONDS`); the scan
  cadence is `GATEWAY_TASK_RECONCILE_INTERVAL` (default `30s`).
- `sandboxd` provides an out-of-process execution boundary and policy checks.
  It is not a container, chroot, or VM; the subprocess runs as the same OS
  user as the gateway.  For kernel-enforced network isolation, set
  `GATEWAY_SANDBOX_OS_ISOLATION=true` (Layer 2 — Linux network namespaces,
  macOS Seatbelt; see `docs/sandbox.md`).
- Network allowlisting for task tools is best-effort static command parsing
  by default, not a hard egress firewall.  `GATEWAY_SANDBOX_OS_ISOLATION=true`
  promotes the Linux network check to a kernel guarantee (CLONE_NEWNET); on
  macOS it uses a Seatbelt profile.  Windows and other platforms remain
  pattern-match only.
- Approval policies cover shell, git, file, and network pre-execution gates plus
  per-tool `agent_loop` gating (`read_file`, `all_tools`). Unknown policy names
  are rejected at startup. The per-MCP-server `approval_policy` axis
  (`auto` / `require_approval` / `block`) is separate.
- Browser automation, WASM plugins, and broad tool marketplaces are out of
  scope for the current alpha.

## Deployment

- Single-node Docker or bare-binary deployments are the primary tested paths.
- SQLite is the pragmatic single-node durable default in Docker.
- Postgres is supported for shared durable state and multi-replica deployments.
  Basic multi-node operational guidance is in
  [`docs/deployment.md#multi-node-deployment`](deployment.md#multi-node-deployment);
  production soak testing and deeper runbooks are still thin.
- Kubernetes, Helm, Nomad, and hosted deployment matrices are not first-class
  release targets yet.

## Observability

- Hecate is OpenTelemetry-first for traces, metrics, and logs.
- The local UI trace inspector is an operator workbench, not a replacement for
  a long-term telemetry backend.
- OTLP exporter failures should be treated like deployment misconfiguration;
  Hecate keeps structured stdout logs as the local fallback.

## Operator UI

- The operator UI is usable for the main alpha workflows: provider setup,
  request inspection, budgets, tenants, keys, and task-run debugging.
- Some bulk-management and deeper artifact-inspection flows are still lighter
  than a mature control-plane product.

## Desktop App

- Bundles are not code-signed. macOS Gatekeeper warns on first launch
  ("Apple cannot check it for malicious software"); Windows SmartScreen
  shows "Windows protected your PC." Both have user-facing escapes
  (right-click → Open on macOS; "More info" → "Run anyway" on Windows)
  but are not the smooth first-run that signed apps get. See
  [desktop-app.md](desktop-app.md) for the full first-launch story.
- Platforms shipped: macOS (Apple Silicon), Linux x86_64, Windows x86_64.
  macOS Intel, Linux arm64, and Windows arm64 are not yet built.
- Auto-update is not wired. The plugin is installed but inactive until a
  signing keypair and update endpoint are decided. Operators upgrade by
  downloading the next release manually.
- Closing only the window on macOS does not quit the app — the gateway
  sidecar keeps running. Use `cmd+Q` to fully quit.
- Per-platform data dir: settings on macOS don't migrate to a Linux
  build of the same version. Multi-machine users keep separate config
  per OS.
