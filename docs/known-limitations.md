# Known Limitations

Hecate is public-alpha software. This page is the plain-language list of what
operators should not assume yet.

The current quality gate for leaving alpha is tracked in the
[alpha-to-beta roadmap](beta-roadmap.md). Until that gate closes, Hecate keeps
shipping `v0.1.0-alpha.N` releases from reviewed PRs merged into `master`.

## API And Schema Stability

- Public APIs are designed to be stable, but pre-1.0 changes are still possible.
- Persisted SQLite schemas are young. Back up data before upgrading.
- There is not yet a dedicated migration CLI or rollback workflow.
- The gateway defaults to `127.0.0.1:8765` and enforces same-origin browser
  requests, but same-origin is not a network security boundary. If you bind it
  beyond the local machine, bring your own access controls, firewall, or reverse
  proxy. The practical threat model lives in [Security](security.md).

## Provider Lifecycle

- Operators add providers from the built-in preset catalog or an
  OpenAI-compatible custom endpoint flow. `PROVIDER_<NAME>_*` env vars
  also auto-import into the Connections view on first boot; subsequent
  boots leave operator UI edits untouched. See [providers.md](providers.md)
  for the full env-vs-UI lifecycle.
- Credentials, base URLs, and provider defaults are managed through
  the persisted settings store. Taking a provider out of rotation is done by
  deleting it — there is no enable/disable toggle.
- Custom clients are supported separately: external callers can use Hecate's
  OpenAI-compatible or Anthropic-compatible endpoints without requiring a custom
  provider record.
- Provider model discovery depends on each upstream's OpenAI-compatible or
  Anthropic-compatible catalog behavior; local runtimes can differ.
- Provider-specific response extensions are not all preserved yet. For example,
  Perplexity's `citations` and `search_results` fields are currently consumed
  by the upstream adapter but not forwarded through Hecate's normalized chat
  response.

## Usage And Cost Reporting

- Hecate records token usage events for gateway-controlled model calls, but it
  does not enforce global spend controls.
- Provider cost is shown only when it is known from provider-reported fields or
  adapter-reported usage. Treat it as operator visibility, not billing
  enforcement.
- External Agent sessions often run through the adapter's own subscription or
  account. Hecate labels those values as adapter-reported and does not enforce
  external spend.
- Local models report tokens when the runtime provides them; host, GPU, and
  electricity cost are not measured.

## Task Runtime And Sandbox

- `agent_loop` and MCP integration are alpha. They are useful for controlled
  workflows, but the behavior surface is still expanding.
- `agent_loop` tasks require `requested_model` on the task. A missing model
  is caught at start time and returns a 422 `model_not_configured` error; the
  run is never created. Tool support is still ultimately enforced by the
  provider at call time, so stale or incomplete capability metadata can still
  surface as a model/tool error during the first LLM call.
- Runs that are stuck in `running` state (e.g. after a worker crash or process
  restart) are recovered automatically by the periodic reconciler and re-queued
  without operator intervention. The recovery window is three times the
  configured lease duration (`HECATE_TASK_QUEUE_LEASE_SECONDS`); the scan
  cadence is `HECATE_TASK_RECONCILE_INTERVAL` (default `30s`).
- The sandbox is a per-call `sh` subprocess with env sanitisation, output cap,
  and wall-clock timeout applied inline by the gateway. It is not a container,
  chroot, or VM; the subprocess runs as the same OS user as the gateway.
  Where `bwrap` (Linux) or `sandbox-exec` (macOS) is available, every shell /
  git / file call is additionally wrapped for filesystem and network
  confinement — auto-detected at startup, no opt-in flag (see
  `docs/sandbox.md`).
- Network allowlisting for task tools is best-effort static command parsing.
  When the Layer 2 wrapper is active (Linux with `bwrap` installed, or macOS),
  the network half is also enforced at the kernel — `bwrap --unshare-net` or
  Seatbelt `(deny network*)`. Windows runs without the wrapper; pattern-match
  is the only check there.
- Approval policies cover shell, git, file, and network pre-execution gates plus
  per-tool `agent_loop` gating (`read_file`, `all_tools`). Unknown policy names
  are rejected at startup. The per-MCP-server `approval_policy` axis
  (`auto` / `require_approval` / `block`) is separate.
- Browser automation, WASM plugins, and broad tool marketplaces are out of
  scope for the current alpha.

## Hecate Chat

- Hecate Chat can mix tools-off direct model turns and tools-on task-backed
  turns in one transcript. Message-level runtime snapshots are
  persisted so old turns keep their original provider/model/task context even
  when the header selection changes later.
- Only one task-backed segment can be active in a Hecate Chat session at a time.
  The HTTP API rejects new turns with `409 chat.agent_session_busy` while
  the backing task is queued, running, or awaiting approval. The operator UI
  turns this into a local **Queued next** composer FIFO and sends the prompt
  after the active run settles; queued prompts are not durable until submitted.
- Tools-on Hecate Chat needs a model known to support tools
  (`tool_calling="basic"` or `parallel`). If the selected model is unknown or
  explicitly does not support tools, the operator UI keeps the transcript
  usable by sending the next prompt as a direct model turn and showing a
  capability repair hint. Ollama models can be enriched from their native
  capability metadata; generic OpenAI-compatible local models often remain
  `unknown` until the provider reports richer metadata.
- Workspace modes and named agent profiles are still roadmap items.
  Tools-on chat uses the selected workspace with the current built-in profile.
- Tasks remains canonical for full run history, retry/resume, artifacts, and
  patch review. Chats projects the high-signal run activity and approval
  controls, but it is not a replacement for every Task Detail inspection flow.

## External Agent Adapters

- Codex, Claude Code, Cursor Agent, and Grok Build run as trusted local subprocesses in the
  selected workspace. Hecate supervises lifecycle, approvals, timeouts,
  diagnostics, and Git diff capture, but it does not sandbox those agents or
  own their internal runtime loops.
- Adapter auth and billing state belongs to the underlying CLI account. Hecate
  can probe common failures and surface friendly hints, but operators still
  need to use each agent's own login/status flow when credentials expire.
- Readiness fixtures are diagnostic only. They can force Connections and Chats
  status states for UI testing, but real External Agent chats still require the
  underlying CLI and ACP adapter to start successfully.
- Patch review is alpha-grade: Hecate captures Git diffs, exposes changed-file
  inspection, and can revert captured paths, but a full side-by-side review
  workspace is not shipped yet.

## Deployment

- Docker, bare-binary, and desktop deployments are the supported paths.
- SQLite is the durable default in Docker.
- Multi-node / clustered deployment is out of scope.
- Kubernetes, Helm, Nomad, and hosted deployment matrices are not release
  targets.

## Observability

- Hecate is OpenTelemetry-first for traces, metrics, and logs.
- The local UI trace inspector is an operator workbench, not a replacement for
  a long-term telemetry backend.
- OTLP exporter failures should be treated like deployment misconfiguration;
  Hecate keeps structured stdout logs as the local fallback.

## Operator UI

- The operator UI is usable for the main alpha workflows: provider setup,
  Chats for model and external-agent sessions, request inspection, usage,
  task approvals, and task-run debugging.
- Some bulk-management flows and deeper side-by-side artifact review are still
  lighter than a mature settings/governance product.

## Desktop App

- Windows bundles are not yet code-signed. SmartScreen shows
  "Windows protected your PC." on first launch; the user-facing
  escape is **More info → Run anyway**. macOS bundles cut by
  `release.yml` with the `APPLE_*` secrets configured are signed
  with Developer ID Application and notarized — first launch on
  a clean Mac shows no Gatekeeper warning. Earlier alpha bundles
  (pre-signing rollout) and any fork build without the secrets
  remain unsigned; right-click → Open on first launch. See
  [desktop-app.md](desktop-app.md) for the full first-launch story.
- Homebrew distribution is not published yet. When it exists, it will improve
  installation and upgrades, but it will not replace Apple Developer ID
  signing/notarization for the native macOS app.
- Platforms shipped: macOS (Apple Silicon), Linux x86_64, Windows x86_64.
  macOS Intel, Linux arm64, and Windows arm64 are not yet built.
- Per-platform data dir: settings on macOS don't migrate to a Linux
  build of the same version. Multi-machine users keep separate config
  per OS.
