# Deployment

The [Start Here](../README.md#start-here) guide covers `docker run` end-to-end. This page is the reference for everything past the first run: pinning images, the compose profile, the binary install, storage tiers, and rate limits.

Hecate defaults to `127.0.0.1:8765` and enforces same-origin for browser requests, but same-origin is not a network security boundary. If you change `HECATE_ADDRESS` to expose Hecate beyond the local machine, startup now requires `HECATE_ALLOW_NON_LOOPBACK_BIND=1`; set it only with your own access controls, firewall, or reverse proxy in front. The current security model is documented in [Security](security.md).

For exposed provider-compatible inference (`/v1/models`, `/v1/chat/completions`,
and `/v1/messages`), `HECATE_INFERENCE_TOKEN` adds an optional shared-token
gate. Clients can send the token with the `Authorization: Bearer ...` header or
the `x-api-key` header. It is a local deployment guard, not user management;
keep using a reverse proxy, firewall, VPN, or equivalent control for anything
reachable beyond your own machine.

Minimum exposed-instance guardrail set:

```bash
HECATE_ADDRESS=0.0.0.0:8765
HECATE_ALLOW_NON_LOOPBACK_BIND=1
HECATE_ALLOWED_ORIGINS=https://hecate.example.com
HECATE_RUNTIME_TOKEN=replace-with-at-least-24-random-characters
HECATE_INFERENCE_TOKEN=replace-with-at-least-24-random-characters
```

Use `HECATE_RUNTIME_TOKEN` for Hecate-native clients such as the operator UI,
MCP tools, and chat/task control-plane calls. Use `HECATE_INFERENCE_TOKEN` for
OpenAI- or Anthropic-shaped SDK clients pointed at `/v1/*`. Keep `/healthz`
private to the host, load balancer, or orchestrator health check.

When a self-hosted runtime binds beyond loopback and has configured provider
credentials, startup logs a warning for each missing token so exposed
deployments do not silently leave credential-spending routes unguarded. Remote
runtime mode is different: valid `X-Hecate-Remote-*` identity from the trusted
control-plane proxy is the auth boundary there, and the shared local tokens are
bypassed after that identity check succeeds. The self-hosted warning is
advisory and intentionally conservative; it can still appear when an
authenticating reverse proxy is your access-control boundary.

## Contents

- [Image pinning](#image-pinning)
- [Remote runtime mode](#remote-runtime-mode)
- [Remote supervision from another device](#remote-supervision-from-another-device)
- [Binary install](#binary-install)
- [Desktop app](#desktop-app)
- [Resetting state](#resetting-state)
- [Bootstrap key and backups](#bootstrap-key-and-backups)
- [Storage backend](#storage-backend)
- [External-agent startup knobs](#external-agent-startup-knobs)
- [Rate limiting](#rate-limiting)

## Remote runtime mode

The Docker runtime image is shared by local/self-host deployments and remote
runtimes. It includes the Hecate binary, embedded UI, git/ssh, and the supported
External Agent CLIs; Hecate-owned ACP adapters are compiled into the binary.
The image defaults to local/self-host mode;
remote deployments opt into the remote security posture with runtime env. Start
the runtime with:

```bash
HECATE_REMOTE_RUNTIME_MODE=1
HECATE_REMOTE_RUNTIME_SECRET=replace-with-at-least-24-random-characters
```

Treat this mode as a single-operator personal remote runtime boundary unless
the surrounding product has its own account model, authorization checks, audit,
credential separation, and vendor-supported External Agent credentials. Remote
runtime mode is not multi-user authentication by itself.

In this mode every non-`/healthz` request must arrive through the trusted
control-plane proxy. The proxy sends `X-Hecate-Remote-Runtime-Secret` plus
`X-Hecate-Remote-Actor-ID`, `X-Hecate-Remote-Org-ID`,
and `X-Hecate-Remote-Runtime-ID`; it may send
`X-Hecate-Remote-Project-ID` when the operator is project-scoped. An omitted
project header represents **No project**. Hecate records the actor identity on
supported audit and telemetry paths. The legacy
`HECATE_RUNTIME_TOKEN` and `HECATE_INFERENCE_TOKEN` guards are bypassed once a
request has valid remote identity, so the trusted proxy is the authentication
boundary for remote runtimes.

Remote mode also blocks local-only operations over the remote surface:
workspace picker/open-in-editor, reset-data, shutdown, MCP probe, and local
provider discovery and MCP registry discovery. Hecate-native `/hecate/v1/*`
routes are classified for remote mode, and route coverage tests fail when a new
registered route is not explicitly marked remote-safe or local-only. Keep the
runtime network-private even with this mode enabled; the header secret is the
internal proxy contract, not a public internet authentication system.

The desktop app's outbound Cloud connector uses a mixed local/remote posture
instead of enabling global remote runtime mode. It creates an ephemeral secret
for the sidecar process, stamps only relayed requests with the authenticated
Cloud owner identity, and leaves ordinary loopback requests local. Both
postures enter the same remote endpoint guard after identity validation.

Remote deployments using the published image must supply container- or
VM-level isolation around each instance. The image intentionally includes a
POSIX shell, git/ssh, common project-dependency tools (`build-essential`,
Python/pip/venv, `pkg-config`, `ripgrep`, `jq`, and archive/process helpers),
and External Agent CLIs. The Codex and Claude Code ACP bridges are compiled
into the Hecate binary; the image only needs their vendor CLIs. The image does
not bundle language servers or `ast-grep`; native `code_intelligence` reports
those optional capabilities as missing until the operator adds trusted global
executables to `PATH` or configures the exact `HECATE_CODEINTEL_*_PATH`, while
`grep` remains available. Installing a language
server alone does not enable semantic queries under the image's normal
read-only or network-denied task policy. The image also does not install
`bwrap` by default, so those calls remain denied until the deployment supplies
a compatible OS sandbox; an explicitly write-enabled, network-enabled task can
run a trusted server without one. Capability discovery and optional
`ast-grep` structural search do not start a language server and remain usable.
Hecate still applies its process policy, environment sanitisation, and approval
gates, but filesystem/network isolation inside the stock container is normally
reported as `none`.

Remote mode also disables local model providers by default. Local presets are
hidden, `kind=local` provider creates/updates are rejected, env-preconfigured
local providers are skipped, and pre-existing local provider rows are not loaded
into the runtime registry. Leave this default unless the runtime deliberately
attaches an isolated local-model sidecar.
Private deployments that deliberately attach an isolated Ollama/vLLM-compatible
sidecar can opt in with:

```bash
HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1
```

The local-provider switch is based on Hecate provider `kind`, not URL
classification. A custom `kind=cloud` provider keeps its configured `base_url`;
network destination policy belongs in the remote deployment boundary.

External Agent sessions in remote mode use a fail-closed credential policy even
though the image contains the agent CLIs. By default, Hecate only accepts
vendor-supported remote credentials: `OPENAI_API_KEY` / `CODEX_API_KEY`,
`ANTHROPIC_API_KEY`, `CURSOR_API_KEY`, or `XAI_API_KEY` respectively. The
adapter catalog reports the declared credential modes and marks an adapter
unauthenticated until one of its remote-safe env keys is present. When Hecate
starts an External Agent for a remote request, the child process receives only
runtime essentials, the matching credential family, and an ephemeral `HOME` /
XDG config directory.

For a single-user personal remote runtime, an operator may opt into
runtime-local External Agent login state:

```bash
HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS=1
```

Only use this when the runtime's `HOME` / XDG config/cache/data directories live
on that runtime's persistent volume and the runtime is owned by one person. The
login should be created inside that runtime through Terminal or SSH. Keep this
unset unless that one-person ownership boundary is true. External Agent vendors
may restrict or forbid shared/account-delegated use of browser or CLI login
state; Hecate does not interpret, bypass, or relax those terms. For anything
beyond personal remote use, use vendor-supported API keys, team/project
credentials, enterprise tokens, or future vendor auth flows.

## Remote supervision from another device

A phone or another browser can act as an operator surface for a Hecate runtime
running on a laptop, workstation, VM, or container. The remote browser does not
receive that host's filesystem, credentials, or processes. It sends supervised
operations to the named runtime host, and work continues to execute there.

```text
Phone or remote browser
        |
        | authenticated operator request
        v
Hecate runtime host: MacBook
        +-- chats, tasks, approvals, and traces
        +-- workspaces, credentials, and External Agents
        +-- embedded Cairnline project coordination
```

Set `HECATE_RUNTIME_HOST_LABEL` to a short operator-facing name such as
`MacBook`. Hecate gives the runtime a stable opaque ID in
`hecate.runtime-host.json`; the ID follows that Hecate data directory across
restarts, while changing the label does not change the ID. The operator shell
shows `On MacBook` for local access and `Supervising MacBook` when the request
arrives through trusted remote-runtime identity. `GET /hecate/v1/whoami`
exposes the same runtime identity and supervision posture to other clients.

Hecate does not currently provision a public endpoint, tunnel, device pairing,
or account system. The operator must provide the authenticated proxy, private
network, VPN, or surrounding control plane. For Hecate's explicit remote-safe
route policy, use remote runtime mode behind a trusted proxy as described
above; do not expose the header secret directly to a browser. A self-hosted
non-loopback bind with shared tokens remains an operator-managed deployment and
does not activate trusted remote-runtime identity or its local-only route
blocks.

This is remote supervision of one runtime host, not process migration. Chats,
tasks, approvals, credentials, workspaces, execution references, and running
External Agents remain attached to that host. Cairnline owns the portable
project coordination model, but Hecate currently uses its embedded local store;
moving or sharing that graph between runtime hosts still requires an explicit
coordination transport and host-readiness checks.

## Image pinning

`docker-compose.yml` references `ghcr.io/hecatehq/hecate:latest`, a multi-arch
(`linux/amd64`, `linux/arm64`) runtime image published from this repo on every
`v*` tag. A fresh host can `docker compose pull` and start without a build step.
The published image has the same runtime posture as local source builds from
`Dockerfile`: Hecate plus a shell, git/ssh, common dependency-install tooling,
and the supported External Agent CLIs; Hecate-owned ACP adapters are compiled
into that binary. Local/self-host behavior
remains the default unless `HECATE_REMOTE_RUNTIME_MODE=1` is set.

To pin to a specific release, replace `:latest` with the published tag (no `v` prefix — goreleaser uses the bare semver as the docker tag). Example for the current alpha:

```yaml
# docker-compose.yml
image: ghcr.io/hecatehq/hecate:0.5.0-alpha.3
```

Pinning is recommended for any deployment beyond local experimentation — `:latest` floats over alpha increments that may include schema or config changes.

When the working tree is a checkout of the source, `docker compose up` rebuilds
locally from the bundled `Dockerfile` instead of pulling. Useful for testing
changes; remove the `image:` line or run `docker compose build` first if you
want the local build to be the canonical artifact.

The Docker image starts `hecate` by default. The container sets
`HECATE_PUBLIC_URL=http://127.0.0.1:8765`, which is the host URL users normally
reach through `-p 8765:8765` or `docker compose`. The image also sets
`HECATE_ALLOW_NON_LOOPBACK_BIND=1` because container processes must listen on
`0.0.0.0` for the published port to work. Treat the host-side published port as
the exposed surface and protect it with host firewall rules or a reverse proxy
when it is reachable beyond your own machine.

For container deployments reachable by other machines, use the same baseline as
the top-level guardrail set: private `/data` volume, `HECATE_BACKEND=sqlite` or
Postgres, platform-managed secrets for provider keys and bootstrap overrides,
`HECATE_RUNTIME_TOKEN` for Hecate-native UI/API clients, and
`HECATE_INFERENCE_TOKEN` for provider-compatible SDK clients. Do not put tokens
or provider keys in image layers.

The runtime stage defaults to `node:24-trixie-slim` through the `NODE_IMAGE`
build arg so the image has a maintained Debian userspace for shell, npm, and
project dependency workflows.

Hecate can expose opt-in operator terminal sessions over the local runtime API
with `HECATE_OPERATOR_TERMINALS=1`. Keep this disabled for remote runtime mode:
startup rejects it there, and the terminal endpoints are local-only. Configure
container and remote instances through environment variables, mounted secrets,
the persisted settings UI/API, or your deployment platform's own administrative
shell (`ssh`, `docker exec`, `kubectl exec`, provider console, or equivalent).
Direct platform shells bypass Hecate approvals and audit trails; use
task-runtime tools when command execution should remain governed by Hecate.
External Agent ACP terminal callbacks are separate: they are scoped to the
selected workspace, owned by the supervised adapter session, disabled unless
`HECATE_AGENT_ADAPTER_TERMINALS=1`, and approval-gated before command spawn.
Remote runtime mode also requires `HECATE_REMOTE_ALLOW_ACP_TERMINALS=1`.

The bundled External Agent CLIs are pinned by Docker build args so a Hecate
release does not silently move to a newer top-level agent package. The Cursor
Agent installer is fetched from Cursor's official install URL and checked
against a pinned SHA-256 before it runs; update the checksum only after
reviewing the new installer contents.

If a `docker run` (or `docker compose up`) errors with `bind: address already in use` on `:8765`, a previous `just dev` / `just run` / `./hecate serve` is still listening from another shell. Free the port with `just stop` and retry; `just dev`, `just run`, and `just serve` also auto-run `stop` before starting so successive launches don't pile up.

## Binary install

The release workflow publishes static, single-file binaries for `linux+darwin × amd64+arm64` to GitHub Releases. Skip Docker if you'd rather run Hecate directly from the terminal:

```bash
# pick the right tarball for your OS / arch
curl -LO https://github.com/hecatehq/hecate/releases/download/v0.5.0-alpha.3/hecate_0.5.0-alpha.3_linux_amd64.tar.gz
tar -xzf hecate_0.5.0-alpha.3_linux_amd64.tar.gz
./hecate serve
```

The `hecate serve` command starts the gateway service, embeds the React operator UI, listens on `127.0.0.1:8765` by default, and stores state under `HECATE_DATA_DIR` (default `.data/`). No additional runtime dependencies — the binaries are statically linked and CGO-free.

To bind the bare binary beyond loopback, set both variables and provide your own network protection:

```bash
HECATE_ADDRESS=0.0.0.0:8765 HECATE_ALLOW_NON_LOOPBACK_BIND=1 ./hecate serve
```

To pin the data directory to a known location:

```bash
HECATE_DATA_DIR=/var/lib/hecate ./hecate serve
```

For systemd, launchd, or supervisor wrappers, the only requirements are: the working directory is writable for `HECATE_DATA_DIR`, port 8765 is available, and `.env` (if used) sits in the working directory or is sourced into the unit file. The binary path itself can live anywhere on `$PATH`.

Available tarballs for `v0.5.0-alpha.3`:

- `hecate_0.5.0-alpha.3_linux_amd64.tar.gz`
- `hecate_0.5.0-alpha.3_linux_arm64.tar.gz`
- `hecate_0.5.0-alpha.3_darwin_amd64.tar.gz`
- `hecate_0.5.0-alpha.3_darwin_arm64.tar.gz`

Each tarball includes `hecate`, `LICENSE`, and `README.md`.
Verify integrity against `checksums.txt` published alongside the release.

The gateway writes `hecate.runtime.json` into `HECATE_DATA_DIR` on startup for
runtime diagnostics.

## Desktop app

A third install path for personal use on a laptop. Same release, different artifacts:

| Platform              | Bundle                                                  |
| --------------------- | ------------------------------------------------------- |
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg`                              |
| Linux x86_64          | `Hecate_X.Y.Z_amd64.deb`, `Hecate_X.Y.Z_amd64.AppImage` |
| Windows x86_64        | `Hecate_X.Y.Z_x64_en-US.msi`                            |

The bundle is a Tauri 2.x chrome around the same `hecate` runtime binary used in Docker and the tarballs. On launch the app spawns it as a gateway sidecar on a free loopback port, polls `/healthz`, then loads the embedded UI directly.

State lives in the platform data dir, not next to the binary:

| Platform | Data dir                                       |
| -------- | ---------------------------------------------- |
| macOS    | `~/Library/Application Support/sh.hecate.app/` |
| Linux    | `~/.local/share/sh.hecate.app/`                |
| Windows  | `%APPDATA%\sh.hecate.app\`                     |

First-launch behavior depends on the platform and on how the bundle was built:

- **macOS Apple Silicon** is the maintainer-tested desktop path. Release bundles are signed with a Developer ID Application certificate and notarized when produced by a release-workflow run (`.github/workflows/release.yml` — tag push or manual `workflow_dispatch`) with the `APPLE_*` / `KEYCHAIN_PASSWORD` repo secrets configured (see [`macos-signing.md`](macos-signing.md)). Such bundles launch with no Gatekeeper warning and drag-install to `/Applications` cleanly. Earlier alpha bundles, plus any release built before the secrets landed (or any future fork build that doesn't have access to them), remain unsigned — Gatekeeper blocks the first launch with "Apple cannot check it for malicious software." Right-click `Hecate.app` → **Open**, confirm in the dialog. Subsequent launches work normally.
- **Windows** bundles are CI-built but have not yet been manually installed or exercised. They are not code-signed, so SmartScreen is expected to show a "Windows protected your PC" warning once tested. Full Authenticode signing is roadmap.
- **Linux** bundles are CI-built but have not yet been manually launched or exercised. `.deb` installs as a normal package; `.AppImage` needs `chmod +x` before running. Expect bugs until real-machine smoke coverage exists.

For Linux or Windows today, Docker or the standalone binary tarballs are the
more predictable alpha paths.

Desktop app distinct from `docker run` / bare binary:

- No port conflict with a separately-running gateway — the app picks a free loopback port at launch.
- Quitting the app via `cmd+Q` (macOS) / **File → Quit** (Windows / Linux) is designed to stop the sidecar; this is smoke-tested on macOS and still untested on Linux/Windows.
- Multi-machine users keep separate config per OS — settings on macOS don't migrate to Linux even on the same release.

Full state, footguns, and roadmap: [`desktop-app.md`](desktop-app.md).

## External Agent startup knobs

Web, Docker, tarball, and native-app launches use the same gateway env vars for
chat sessions and External Agent integrations. The native app spawns the bundled
`hecate` runtime in gateway mode with these env vars inherited from the app
process; Docker reads them from `.env` / compose; bare binaries read the shell
environment.

| Env var                             | Default | Applies to                                           |
| ----------------------------------- | ------- | ---------------------------------------------------- |
| `HECATE_CHAT_MAX_TURNS_PER_SESSION` | `0`     | Per-session user→assistant turn ceiling              |
| `HECATE_CHAT_MAX_SESSION_DURATION`  | `0s`    | Wall-clock age ceiling before new turns are rejected |
| `HECATE_CHAT_IDLE_TIMEOUT`          | `0s`    | Background idle auto-close sweeper                   |

Codex and Claude Code use Hecate's built-in Go ACP adapter libraries, which
launch their vendor CLIs as supervised child processes. Cursor Agent and Grok
Build expose ACP modes directly in their vendor CLIs.
Opening Connections performs passive path discovery only. The operator-owned
**Run diagnostics** action calls `POST
/hecate/v1/agent-adapters/{id}/probe`, which starts the discovered app and opens
a disposable ACP session for troubleshooting. It is optional: **New chat**
re-resolves the executable and prepares the real ACP session. Direct ACP peers
start during setup; embedded bridges may defer vendor CLI execution and auth
until the first message.

## Resetting state

The running `POST /hecate/v1/system/reset-data` endpoint is intentionally
unavailable and returns `409 conflict` before deleting anything. Stop the
runtime completely before using one of these deployment-specific reset paths;
do not remove a live SQLite or Cairnline database.

To wipe the stack back to first-run — removes the `hecate-data` volume (SQLite db) and regenerates state on the next `docker compose up`:

```bash
just reset-docker
```

For local (non-Docker) development resets, see [`development.md`](../contributor/development.md#reset-state).

For Postgres-backed deployments, removing only Hecate's local data directory
does not clear Postgres. With the runtime fully stopped, clear the configured
Postgres state and any local Cairnline/bootstrap files that belong to the same
deployment according to its backup and recovery policy.

## Bootstrap key and backups

Encrypted local credentials depend on two pieces of state:

- the settings database, usually `hecate.db` when the relevant storage backend
  is `sqlite`, or the configured Postgres database when the backend is
  `postgres`;
- the bootstrap control-plane key, loaded from
  `HECATE_CONTROL_PLANE_SECRET_KEY`, `HECATE_BOOTSTRAP_FILE`, or
  `hecate.bootstrap.json` in the data directory.

Back up and restore those pieces together when you want provider keys, external
agent credentials, or encrypted MCP literals to remain usable. Restoring the
database without the matching bootstrap key leaves encrypted secrets
undecryptable; restore the old key or re-enter the credentials from the
operator UI.

Docker's default `/data` volume stores both `/data/hecate.db` and the default
bootstrap file, so backing up the volume is enough for the default SQLite path.
For Postgres, back up the database and the bootstrap file or
`HECATE_CONTROL_PLANE_SECRET_KEY` secret together. If you override
`HECATE_CONTROL_PLANE_SECRET_KEY`, treat the env var as the source that seeds
the bootstrap key, not as env-only storage: Hecate persists that key to
`HECATE_BOOTSTRAP_FILE` or the default bootstrap file on startup, overwriting
any existing bootstrap key, and the bootstrap path must be writable. Back up the
env/secret-manager value and the bootstrap file with the same care as the
database; if they diverge, the env value wins on the next startup.

Runtime host identity is separate, non-secret state in
`HECATE_DATA_DIR/hecate.runtime-host.json`. Include that file when restoring the
same named runtime host. Omitting it creates a new runtime ID on the next start
without changing the bootstrap encryption key or stored credentials.

## Storage backend

Hecate keeps one process-wide backend selector for Hecate-owned state.

| Env var          | `memory`                         | `sqlite`                                         | `postgres`                                               |
| ---------------- | -------------------------------- | ------------------------------------------------ | -------------------------------------------------------- |
| `HECATE_BACKEND` | local default; resets on restart | Docker default; persists to `HECATE_SQLITE_PATH` | Remote runtime option; persists to `HECATE_POSTGRES_URL` |

This backend covers settings, encrypted provider credentials, audit and usage
history, Agent Presets, chat sessions, external-agent approvals and grants,
chat attachment bodies, Tasks, the task queue, Task Schedules and their
occurrence ledger, and Hecate's project-runtime overlays.

Portable Projects coordination is stored by embedded
[Cairnline](https://github.com/hecatehq/cairnline) in
`<HECATE_DATA_DIR>/cairnline/embedded/projects.db`. Cairnline owns project
identity, roots, source and skill metadata, roles, work items, assignments,
collaboration artifacts, handoffs, accepted project memory, memory candidates,
and Project Assistant proposal records. There is no Projects backend selector,
dual-write mirror, or migration/rollback control plane.

Hecate continues to expose its `/hecate/v1/projects*` API and Projects UI.
Those surfaces combine Cairnline coordination records with Hecate-owned runtime
policy and evidence such as provider/model resolution, task or chat references,
context snapshots, approvals, sandboxing, and traces.

Deployment-specific notes:

- The Docker image defaults to `sqlite` for Hecate-owned state at
  `HECATE_SQLITE_PATH` (default `/data/hecate.db`) and keeps Cairnline's
  database under the same mounted `HECATE_DATA_DIR`.
- To make Hecate-owned Docker state ephemeral, set `HECATE_BACKEND=memory`.
  Cairnline remains SQLite-backed; remove or replace the data volume for a fully
  fresh local Projects graph.
- Hosted runtimes can use Postgres for Hecate-owned state with
  `HECATE_BACKEND=postgres` and `HECATE_POSTGRES_URL=postgres://...` (or
  `DATABASE_URL`). Cairnline remains an embedded local SQLite dependency in
  the current integration.
- When `HECATE_BACKEND=sqlite` or `postgres`, Hecate reconciles pending
  external-agent approvals from a prior process to `status=timed_out`,
  `path=startup_reconcile` before serving requests.
- Task Schedules use the same backend as their Tasks. SQLite and Postgres keep
  Schedule configuration and occurrence claims across restarts; memory loses
  both when the process exits. An atomic occurrence key and claim-owner fence
  coordinate runtimes that share the store, but the runtime process still has
  to be running to dispatch due work. Missed recurring times coalesce at
  startup rather than becoming a backfill queue.
- Every backend runs Chat attachment recovery before serving: pending
  message-id claim fences are resolved from transcript metadata and a
  metadata-only session sweep removes bodies whose owning chat no longer
  exists. Conflicting metadata remains claimed and is reported rather than
  released. Run a single Chat API writer against a backend; task queue leases
  support multiple workers, but this attachment reconciliation slice does not
  make Chat transcript writes active-active.
- Durable queued-message submissions use the backend's
  `chat_message_requests` ledger. The session-scoped request key and SHA-256
  payload fingerprint are committed atomically with the user transcript row;
  raw prompts, MCP configuration, and attachment bodies are not duplicated in
  that table. A bounded pending-owner lease prevents an interrupted runtime
  from blocking the key forever. This protects retries across browser tabs and
  runtime replacement, but it does not make unrelated Chat transcript writes
  generally active-active.
- Attachment bodies are bounded to 512 MiB per chat. Across draft and linked
  rows, the memory backend is capped at 512 MiB and SQLite/Postgres at 4 GiB.
  Postgres serializes cross-session quota admission so concurrent uploads
  cannot exceed the aggregate cap. Any new upload reclaims drafts older than 24
  hours across the attachment store; linked rows remain until the owning chat
  is deleted.
- Attachment upload validation has a fixed two-request admission gate per Hecate
  process. Additional uploads fail before their bodies are read with
  `429 chat.attachment_upload_busy` and `Retry-After: 1`; this guard is
  independent of the optional general request rate limiter below. An admitted
  upload body has 60 seconds to finish reading; a route-local socket deadline
  closes a stalled body and its expired HTTP/1 connection, then returns
  `408 chat.attachment_upload_timeout`. This does not set a global server read
  timeout or constrain streaming endpoints.
- Attachment content responses have an independent fixed four-request gate.
  The permit spans the scoped store lookup, integrity check, and body write;
  additional downloads fail before hydration with
  `429 chat.attachment_content_busy`, `Retry-After: 1`, and typed
  concurrency/retry fields. Each admitted write has a route-local 30-second
  socket deadline, so stalled clients release both the permit and the chat
  lifecycle operation without setting a global server write timeout. If a
  proxy/wrapper does not expose socket write deadlines, Hecate closes that
  response and returns a fixed `500` before committing the image `200` rather
  than falling back to an unbounded download.
- Image-bearing direct-model turns have a separate fixed two-request admission
  gate per Hecate process. The permit spans attachment claim, historical body
  hydration, base64 expansion, provider serialization, and the provider call.
  Additional image turns fail before claim or transcript mutation with
  `429 chat.image_turn_busy`, `Retry-After: 1`, and typed concurrency/retry
  fields. Text-only turns remain outside this gate.
- File-bearing External Agent turns have their own fixed two-request admission
  gate per Hecate process. The permit spans attachment claim, hydration,
  private staging, and the synchronous ACP prompt. Additional file turns fail
  before claim or transcript mutation with
  `429 chat.external_file_turn_busy`, `Retry-After: 1`, and typed
  concurrency/retry fields. Text-only External Agent turns remain outside this
  gate.
- Hecate does not encrypt attachment blobs at the application layer. Protect
  `/data`, SQLite files, Postgres roles and transport, snapshots, and backups
  with the deployment's filesystem/volume/database controls.

## Rate limiting

Rate limiting is a per-process token bucket. It is disabled by default so first-run local testing does not surprise users.

| Variable                    | Default | Notes                                             |
| --------------------------- | ------: | ------------------------------------------------- |
| `HECATE_RATE_LIMIT_ENABLED` | `false` | Enables request rate limits.                      |
| `HECATE_RATE_LIMIT_RPM`     |    `60` | Steady-state refill rate.                         |
| `HECATE_RATE_LIMIT_BURST`   |     `0` | Optional burst capacity. `0` means "same as RPM". |

Over-limit requests return `429 Too Many Requests` with `code: "rate_limit_exceeded"` and standard `X-RateLimit-*` headers.
