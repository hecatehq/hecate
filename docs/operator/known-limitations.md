# Known Limitations

Hecate is public-alpha software. This page is the plain-language list of what
operators should not assume yet.

The current quality gate for leaving alpha is tracked in the
[alpha-to-beta roadmap](../contributor/beta-roadmap.md). Until that gate closes, Hecate keeps
shipping `v0.1.0-alpha.N` releases from reviewed PRs merged into `master`.

## API And Schema Stability

- Public APIs are designed to be stable, but pre-1.0 changes are still possible.
- Persisted SQLite and Postgres schemas are young. Back up data before
  upgrading.
- There is not yet a dedicated migration CLI or rollback workflow.
- The gateway defaults to `127.0.0.1:8765` and enforces same-origin browser
  requests, but same-origin is not a network security boundary. If you bind it
  beyond the local machine, Hecate requires `HECATE_ALLOW_NON_LOOPBACK_BIND=1`;
  bring your own access controls, firewall, or reverse proxy. The practical
  threat model lives in [Security](security.md).

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
  agent-reported usage. Treat it as operator visibility, not billing
  enforcement.
- External Agent sessions often run through the agent's own subscription or
  account. Hecate labels those values as agent-reported and does not enforce
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
  `docs/runtime/sandbox.md`).
- Long-lived terminals own a Windows Job Object or a dedicated Unix process
  group and retain workspace admission until descendants/output are observed
  drained. Unix preflight and interactive-code checks reject known detachment
  patterns, but sourced/generated/encoded code, arbitrary wrappers, custom
  binaries, and external service managers can escape that group. This is
  best-effort trusted-subprocess supervision, not hard descendant containment.
- Network allowlisting for task tools is best-effort static command parsing.
  When the Layer 2 wrapper is active (Linux with `bwrap` installed, or macOS),
  the network half is also enforced at the kernel — `bwrap --unshare-net` or
  Seatbelt `(deny network*)`. Windows runs without the wrapper; pattern-match
  is the only check there.
- Approval policies cover shell, git, file, and network pre-execution gates plus
  per-tool `agent_loop` gating (`read_file`, `network_egress` for `http_request`
  and configured `web_search`, and `all_tools`). Unknown policy names are
  rejected at startup. The per-MCP-server `approval_policy` axis
  (`auto` / `require_approval` / `block`) is separate.
- Native project-assignment launches enforce Agent Preset surface, tools, write,
  and network posture. The task snapshots the preset id, tools setting, and
  effective sandbox flags. Tools-disabled tasks run as supervised model-only
  tasks: they expose no native or MCP tools, start no MCP host, and reject
  unexpected tool calls before dispatch. Legacy tasks without the tools snapshot
  retain their prior catalog behavior. Network-disabled preset tasks neither
  advertise nor dispatch Hecate's native HTTP or web-search tools. Read-only
  tasks omit broad shell, Git, file-write,
  and interactive-terminal tools; structured inspection and proposal-only
  patch creation remain available. Legacy/manual tasks without a preset snapshot
  retain their prior native-network behavior. Preset-wide `approval_policy`
  remains inspection metadata in this alpha; global task approval policy and
  per-MCP-server policy remain authoritative and cannot be weakened by a preset.
- The built-in `workflow_mode="qa"` slice is report-only inspection, not a
  test runner or general workflow engine. It is available only to native
  `agent_loop` Tasks and forces an ephemeral read-only/native-network-tool-disabled posture. It
  rejects MCP servers and blocks shell/terminal commands, writes, patch or
  proposal creation, semantic and structural code intelligence, native
  HTTP/search, and browser automation. Hecate writes a
  `workflow_manifest` at Run start and a `workflow_report` only after the
  agent returns a final response. The report's narrative is agent-reported; it
  is not proof that tests or browser checks ran. QA excludes repository
  `CLAUDE.md` / `AGENTS.md` from system prompt composition, uses a safe local
  directory copy rather than Git checkout for local Git sources, excludes every
  `.git` entry from its evidence snapshot, and skips automatic Git-summary
  capture. `git_status` / `git_diff` report Git evidence unavailable in QA v0
  rather than invoking Git. A separate constrained test runner would need
  its own explicit permission model.
- QA v0 blocks browser inspection. The public QA Task surface does not yet
  select a browser-evidence assignment posture, so QA does not claim a
  conditional browser capability that operators cannot launch. It has no
  browser, URL-check endpoint, or interactive browser controls.
- Native browser evidence is a deliberately narrow alpha capability, not
  browser automation. It is available only to a local native project-assignment
  task whose Agent Preset snapshots an exact-origin allowlist and only when the
  operator configured a local Chromium-compatible executable. Every call needs
  approval and creates a fresh script-disabled browser profile; Hecate does not import host
  cookies, reuse logins, keep profile state, allow downloads, or retain
  screenshots. The artifact is bounded text evidence only. Hecate Chat,
  External Agent sessions, legacy/manual tasks, and remote runtime do not get
  this tool.
- A fresh Hecate browser profile does not prevent machine or enterprise
  Chromium policy from supplying integrated authentication or a client
  certificate outside profile storage. For identity-sensitive sites, use a
  dedicated unmanaged browser/container with the required OS/network controls.
- Browser evidence permits only selected-origin `GET`/`HEAD` URL-loader
  requests with page scripts and service workers disabled; it exposes no
  WebSocket/WebTransport/WebRTC, click/type/upload/device-control primitive.
  Capture has one timeout across preflight, startup, and page load, and
  cancels after CDP observes 4 MiB of aggregate streamed response data
  (including unknown-length streams). Browser/socket buffering can overshoot
  before Chromium receives that cancellation. That prevents ordinary browser writes, but it cannot make an
  application-specific `GET` endpoint side-effect-free. Private-IP rejection is an initial application-level
  preflight (with an explicit opt-in), and hostname origins are pinned to that
  selected address for one inspection. Neither is an OS/network firewall, VM,
  or complete browser-process sandbox. Treat an approval as permission to load
  the requested page and rely on host/network controls for stronger isolation.
- Hecate has a registry-only plugin metadata slice for native manifests. It can
  validate and show plugin-declared MCP server mount candidates, but it does not
  execute plugin code, start plugin-declared MCP servers, mount plugin tools,
  grant plugin secrets, or call connector APIs yet. Interactive browser
  automation, WASM plugins, arbitrary plugin hooks, and broad tool
  marketplaces remain out of scope for the current alpha. Design notes live in
  `docs/design/proposals/plugin-architecture.md`.

## Hecate Chat

- Hecate Chat can mix tools-off direct model turns and tools-on task-backed
  turns in one transcript. Message-level runtime snapshots are
  persisted so old turns keep their original provider/model/task context even
  when the header selection changes later.
- Only one task-backed segment can be active in a Hecate Chat session at a time.
  The HTTP API rejects new turns with `409 chat.agent_session_busy` while
  the backing task is queued, running, or awaiting approval. The operator UI
  turns this into a local **Queued next** composer FIFO and sends the prompt
  after the active backing Task Run settles. Queue entries are synchronously persisted in
  independent per-item records before dispatch. Same-origin tabs in that
  browser profile merge those records and preserve another tab's in-flight
  delivery fence, but the queue is not server-owned or portable to another
  profile, browser, or device until the transcript commit succeeds.
  Browser-storage write or removal failures keep affected work blocked and
  visible, warn before reload, and may require clearing site data manually.
  In-process data reset is currently unavailable. The reserved API returns
  `409 conflict` without deleting server or browser state, because Hecate cannot
  yet quiesce every background writer. Stop the runtime before following the
  deployment-specific data-directory reset procedure; restart a memory-backed
  runtime to clear its state.
- Tools-on Hecate Chat needs a model known to support tools
  (`tool_calling="basic"` or `parallel`). If the selected model is unknown or
  explicitly does not support tools, the operator UI keeps the transcript
  usable by sending the next prompt as a direct model turn and showing a
  capability repair hint. Ollama models can be enriched from their native
  capability metadata; generic OpenAI-compatible local models often remain
  `unknown` until the provider reports richer metadata.
- File attachments are available for Hecate-owned Tools-off image turns and
  External Agent turns, but not task-backed Tools-on turns. Direct-model inputs
  remain PNG/JPEG/WebP only and require confirmed model image capability;
  External Agents accept up to four non-empty files of any type within the same
  5 MiB per-file and 12 MiB combined limits. Two file-bearing External turns
  may run concurrently per Hecate process; later file turns receive a typed
  retryable `429`, while text-only turns remain available. Message
  submission accepts a session-scoped `client_request_id`, and the operator UI
  reuses its persisted queued-item id so same-payload retries cannot dispatch a
  second turn. A process failure after the user row and key commit but before a
  terminal assistant is durable can leave that turn without terminal proof;
  the UI keeps it blocked for transcript review and does not dispatch later FIFO
  work automatically. Uploads still do not accept client idempotency keys, and
  there is no draft-list recovery surface. An upload whose success response is
  lost can remain as a hidden draft until it is at least 24 hours old and a
  later upload runs stale-draft reclamation. The console restores the local
  prompt and Files with an ambiguity warning but never retries automatically.
  To avoid accumulating hidden drafts, delete the chat before uploading those
  files again, or remove them and wait until after 24 hours before a later
  upload triggers reclamation.
- Workspace modes are available for task/project starts, and named Agent
  Presets now have a core API, preset-management UI, project/role default
  selection, project-skill pickers, and preset-driven assignment context-packet
  memory/source activation. Native project assignments can include bounded
  project memory and portable `AGENTS.md` workspace-instruction bodies when the
  resolved Agent Preset explicitly asks for inclusion. Broader prompt-content policy
  for chats, external-agent starts, host-specific guidance files, and arbitrary
  project source documents is still beta-hardening work. Tools-on chat still
  uses the selected workspace with the current chat runtime posture.
- Tasks remains canonical for full run history, retry/resume, artifacts, and
  patch review. Chats projects the high-signal run activity and approval
  controls, but it is not a replacement for every Task Detail inspection flow.

## External Agents

- Codex, Claude Code, Cursor Agent, and Grok Build run as trusted local subprocesses in the
  selected workspace. Hecate supervises lifecycle, approvals, timeouts,
  diagnostics, and Git diff capture, but it does not sandbox those agents or
  own their internal runtime loops.
- External Agent resource-link file input requires a trusted temporary
  filesystem path. macOS rejects any non-local mount in the canonical temporary
  path or its ancestors. Linux accepts only ext2/3/4, XFS, Btrfs, tmpfs,
  overlayfs, ramfs, or F2FS and rejects every other model, including NFS,
  SMB/CIFS, FUSE, ZFS, AUFS, eCryptfs, and 9p. If the default or a custom
  temporary path fails this check, launch Hecate with `TMPDIR` on an allowlisted
  local filesystem whose canonical ancestors also pass. Rich inputs can still
  overflow into resource-link staging, so advertised image/resource support is
  not a reliable workaround. Cleanup transfers to a runtime-owned janitor that
  outlives the ACP session. Four persistent protected remnants block further
  file-bearing turns for that session; 16 reserved or retained stages block
  file-bearing turns process-wide. Text-only turns remain available.
- Temporary-path redaction covers complete aliases and ordinary accumulated ACP
  chunks, but it is not DLP against the selected trusted agent. A deliberately
  evasive agent can transform a path or split it into unrelated short message
  or activity records that are not individually recognizable as that alias.
- Agent auth and billing state belongs to the underlying CLI account. Hecate
  can probe common failures and surface friendly hints, but operators still
  need to use each agent's own login/status flow when credentials expire.
- Readiness fixtures are diagnostic only. They can force Connections and Chats
  status states for UI testing, but real External Agent chats still require the
  underlying CLI and ACP adapter to start successfully.
- Patch review is alpha-grade: Hecate keeps captured per-turn Git diffs as
  read-only evidence and can discard selected paths from a separately refreshed,
  revision-bound live unstaged tracked workspace patch. Staged and untracked
  changes plus a full side-by-side review workspace are not supported yet.

## Deployment

- Docker, bare-binary, and desktop deployments are the supported paths.
- SQLite is the durable default in Docker.
- Postgres is supported for hosted/cloud-runtime state, but schema migration
  tooling is still alpha.
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

- macOS Apple Silicon is the only desktop bundle maintainers currently
  launch-test. Linux `.deb` / `.AppImage` and Windows `.msi` artifacts are
  generated by CI but have not yet been manually tested on real machines, so
  expect bugs. Prefer Docker or the standalone binary tarballs on Linux and
  Windows if you need the more predictable alpha path today.
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
- Desktop artifacts currently built: macOS (Apple Silicon), Linux x86_64,
  Windows x86_64. Only macOS is manually exercised today. macOS Intel, Linux
  arm64, and Windows arm64 are not yet built.
- Per-platform data dir: settings on macOS don't migrate to a Linux
  build of the same version. Multi-machine users keep separate config
  per OS.
