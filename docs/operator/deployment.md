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
External Agent CLIs/ACP adapters. The image defaults to local/self-host mode;
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
`X-Hecate-Remote-Project-ID`, and `X-Hecate-Remote-Runtime-ID`; Hecate records the
actor identity on supported audit and telemetry paths. The legacy
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

Remote deployments using the published image must supply container- or
VM-level isolation around each instance. The image intentionally includes a
POSIX shell, git/ssh, common project-dependency tools (`build-essential`,
Python/pip/venv, `pkg-config`, `ripgrep`, `jq`, and archive/process helpers),
and External Agent CLIs. The Codex and Claude Code ACP bridges are bundled as
release-built Go binaries verified against their release checksums. The
container build args `CODEX_ACP_ADAPTER_VERSION` and
`CLAUDE_CODE_ACP_ADAPTER_VERSION` select those adapter releases. The image does
not install `bwrap` by default; Hecate still applies its process policy, env
sanitisation, and approval gates, but filesystem/network isolation inside the
container is normally reported as `none`.

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

## Image pinning

`docker-compose.yml` references `ghcr.io/hecatehq/hecate:latest`, a multi-arch
(`linux/amd64`, `linux/arm64`) runtime image published from this repo on every
`v*` tag. A fresh host can `docker compose pull` and start without a build step.
The published image has the same runtime posture as local source builds from
`Dockerfile`: Hecate plus a shell, git/ssh, common dependency-install tooling,
and the supported External Agent CLIs/ACP adapters. Local/self-host behavior
remains the default unless `HECATE_REMOTE_RUNTIME_MODE=1` is set.

To pin to a specific release, replace `:latest` with the published tag (no `v` prefix — goreleaser uses the bare semver as the docker tag). Example for the current alpha:

```yaml
# docker-compose.yml
image: ghcr.io/hecatehq/hecate:0.2.0-alpha.4
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
curl -LO https://github.com/hecatehq/hecate/releases/download/v0.2.0-alpha.4/hecate_0.2.0-alpha.4_linux_amd64.tar.gz
tar -xzf hecate_0.2.0-alpha.4_linux_amd64.tar.gz
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

Available tarballs for `v0.2.0-alpha.4`:

- `hecate_0.2.0-alpha.4_linux_amd64.tar.gz`
- `hecate_0.2.0-alpha.4_linux_arm64.tar.gz`
- `hecate_0.2.0-alpha.4_darwin_amd64.tar.gz`
- `hecate_0.2.0-alpha.4_darwin_arm64.tar.gz`

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

External Agent integrations now use direct ACP binaries only. Codex and Claude
Code use standalone Go adapter binaries, while Cursor Agent and Grok Build use
ACP modes built into their vendor CLIs.
Connections probes External Agent integrations when the workspace opens; the probe calls
`POST /hecate/v1/agent-adapters/{id}/probe`, which re-runs discovery and
performs the ACP handshake so login/billing problems are visible before a chat
fails.

## Resetting state

To wipe the stack back to first-run — removes the `hecate-data` volume (SQLite db) and regenerates state on the next `docker compose up`:

```bash
just reset-docker
```

For local (non-Docker) development resets, see [`development.md`](../contributor/development.md#reset-state).

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

## Storage backend

Hecate keeps the storage model intentionally boring: one process-wide backend
selector controls all Hecate-owned durable state.

| Env var          | `memory`                         | `sqlite`                                         | `postgres`                                               |
| ---------------- | -------------------------------- | ------------------------------------------------ | -------------------------------------------------------- |
| `HECATE_BACKEND` | local default; resets on restart | Docker default; persists to `HECATE_SQLITE_PATH` | Remote runtime option; persists to `HECATE_POSTGRES_URL` |

The backend covers settings, encrypted provider credentials, audit events,
provider health history, usage events, retention history, projects, chat
sessions, external-agent approvals/grants, tasks, and the task queue.
The project backend also carries project memory, project work, project skills,
and agent profiles. The chat backend also carries external-agent approval rows
and durable grants so transcripts and approval history move together.

`HECATE_PROJECTS_COORDINATION_BACKEND=hecate|cairnline` is separate from the
storage backend. The default is `hecate`. `cairnline` records replacement intent
for local bridge experiments. `HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded`
is the current live-route dogfood path and uses Hecate's embedded Cairnline Go
bridge. `HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar` lets operators run
`POST /hecate/v1/projects/cairnline/sidecar-probe` for a one-shot contract
check or `POST /hecate/v1/projects/cairnline/sidecar-connect` to connect a
cached standalone Cairnline MCP client. Operators can then run
`POST /hecate/v1/projects/cairnline/sidecar-read-smoke` to call the sidecar's
read-only `projects.list` MCP tool, or
`POST /hecate/v1/projects/cairnline/sidecar-detail-smoke` to call read-only
`projects.get`, through that persistent client. Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-coordination-smoke` to call the
read-only portable coordination list tools (`projects.list`, `profiles.list`,
`execution_profiles.list`, `skills.list`, `roles.list`, `work_items.list`, and
`assignments.list`) and confirm Hecate can parse their typed
`structuredContent` arrays. Projects
reads, writes, mirrors, and write-authority switchpoints still stay on
Hecate-native stores until Hecate has a sidecar backend adapter. For MCP-pull
context evidence, operators can run
`POST /hecate/v1/projects/cairnline/sidecar-assignment-context-smoke` to select
or request an assignment and confirm `assignments.context` returns typed
assignment/project/work/role metadata. Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-launch-packet-smoke` to confirm
`assignments.launch_packet` returns typed launch-packet ids, counts, and packet
warnings for the same MCP-pull path. The explicit mutation smoke at
`POST /hecate/v1/projects/cairnline/sidecar-lifecycle-smoke` requires
`confirm_mutation=true` and then proves `assignments.next`, claim,
`update_status`, launch packet read, and complete against the standalone
Cairnline sidecar database only; Hecate-native Projects stores remain
authoritative and are not mutated by that smoke. Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-write-smoke`,
`POST /hecate/v1/projects/cairnline/sidecar-setup-smoke`, and
`POST /hecate/v1/projects/cairnline/sidecar-work-smoke`, each with
`confirm_mutation=true`, to prove temporary standalone Cairnline project
identity, setup metadata, and role/work/assignment/context/launch-packet writes
respectively. Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-collaboration-smoke` with
`confirm_mutation=true` to record and verify temporary artifact, evidence,
review, and handoff metadata in the standalone Cairnline sidecar database.
Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-memory-smoke` with
`confirm_mutation=true` to create and verify accepted memory, promote one memory
candidate, reject/delete another candidate, and clean up the temporary
standalone Cairnline project. Operators can also run
`POST /hecate/v1/projects/cairnline/sidecar-assistant-smoke` with
`confirm_mutation=true` to create and verify a temporary Project Assistant
proposal ledger record, verify unconfirmed apply returns `needs_confirmation`,
apply it with explicit confirmation, verify the created role/work/assignment
side effects, and clean up the temporary standalone Cairnline project.
Those smokes delete their temporary project and verify removal.
When the embedded Cairnline read adapter is fully wired,
`GET /hecate/v1/projects/backend-status` reports
`read_model_switch_ready=true`, and project list/detail, setup readiness,
health, skills, memory entries, memory candidates, roles, work-item
list/detail, assignment-list, assignment-context, launch-readiness,
assignment-preflight, artifact-list, handoff-list, Project Assistant
context/proposal reads, project-linked Hecate Chat prelude/context reads,
closeout readiness, activity inbox, and operations brief can be served from the
Cairnline read model. Project Assistant draft generation also uses the
Cairnline-projected context in this mode, while proposal ledger writes remain
Hecate-owned unless `project-assistant-proposals` is explicitly enabled.
Confirmed Project Assistant apply routes project create, project
metadata/default, root, role, work-item, assignment, handoff, and
memory-candidate actions through the same opt-in Cairnline authority
switchpoints when those switchpoints are enabled; chat and runtime side effects
remain Hecate-owned and are best-effort mirrored into Cairnline as
replacement-readiness evidence.
Launch-readiness and assignment preflight read Cairnline coordination records
before applying Hecate runtime checks.
Assignment preflight/start context packets may include inspect-only Cairnline
launch-packet evidence for replacement review, but Hecate still owns dispatch,
task/external agent supervision, approvals, and assignment mutation. Project
reads backed by the Cairnline read model prefer the embedded Cairnline mirror
database when that database already contains the requested project or proposal
record; if the mirror database, project row, or proposal record is missing,
they fall back to the snapshot-seeded in-memory bridge projection. Strict
embedded mode reads project list/detail, setup readiness, health, skills,
roles, work-item list/detail, assignment-list, artifact-list, handoff-list,
activity, closeout-readiness, operations brief, Project Assistant
context/proposal, and project-linked Hecate Chat prelude/context directly from
the embedded Cairnline graph so dogfood routes do not require shadow
Hecate-native project rows. Route selection in this mode is configuration-driven:
direct-read routes attempt the embedded Cairnline graph without first requiring
a Hecate-native snapshot projection, and Cairnline-authoritative portable write
helpers use the embedded graph for project identity/root metadata before any
Hecate-native compatibility shadow. Set
`HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=snapshot` to force the snapshot-seeded
bridge, or `HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded` to make configured
read routes require a populated
`{HECATE_DATA_DIR}/cairnline/embedded/projects.db` and fail loudly when the
mirror database, project row, or proposal record is missing; run
`POST /hecate/v1/projects/cairnline/sync` first when dogfooding strict embedded
reads. The sync and mirror-parity responses include strict embedded smoke
evidence that exercises project list/detail, setup, health, skills, memory,
roles, work/activity/operations, assignment context/readiness, collaboration
artifact/handoff, Project Assistant context/proposal, and project-linked Hecate
Chat context reads against the embedded mirror where matching records exist.
With `HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar`,
`HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar` routes only project list/detail,
setup-readiness, health, project skill list, project memory list,
memory-candidate list, project role list, work-item list/detail,
assignment-list, assignment-context, launch-readiness, assignment preflight, artifact-list,
handoff-list, Project Assistant context/proposal record reads, activity,
project-linked Hecate Chat prelude/context reads, closeout-readiness, and
operations brief reads through the standalone Cairnline MCP client.
Proposal-record reads fall back to the Hecate-native proposal ledger only when
the sidecar reports the proposal is missing; draft/propose/apply mutations
remain Hecate-owned because write-authority switchpoints require
`HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded`.
Assignment-context reads use typed sidecar `assignments.context` data.
Launch-readiness and assignment preflight use the typed sidecar
`assignments.launch_packet` response as their coordination input, then apply
Hecate runtime validation; assignment start/prepare remains Hecate-owned.
Work-item list/detail, assignment-list,
artifact-list, handoff-list, activity, closeout-readiness, and operations brief
reads render the work graph from Cairnline service records and overlay
Hecate-only runtime refs/timestamps while Hecate still owns execution. Outside
the explicit sidecar read-source routes for project list/detail,
setup-readiness, health, skills, memory, memory candidates, roles, work items,
assignment lists, assignment context, launch-readiness, assignment preflight,
artifact lists, handoff lists, Project Assistant context/proposal reads,
activity, closeout readiness, and operations brief, project identity and some
compatibility scaffolding remain Hecate-owned until Cairnline becomes
authoritative. Project identity,
metadata/default, root,
and context-source mutations still write Hecate stores first and then
best-effort mirror into the embedded Cairnline database through
their identity/metadata/root/source/default seams unless their explicit
write-authority switchpoints are enabled; this is replacement-readiness
evidence, not write authority.
The sidecar probe/connect surfaces are configured with
`HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND`,
`HECATE_PROJECTS_CAIRNLINE_SIDECAR_ARGS`,
`HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB`, and
`HECATE_PROJECTS_CAIRNLINE_SIDECAR_PROBE_TIMEOUT`. `sidecar-probe` verifies MCP
tool presence only. `sidecar-connect` keeps the process warm in Hecate's
Cairnline-specific MCP client cache. `sidecar-read-smoke`,
`sidecar-detail-smoke`, `sidecar-coordination-smoke`,
`sidecar-assignment-context-smoke`, and `sidecar-launch-packet-smoke` use that
cached client to call read-only Cairnline MCP tools and return diagnostic
evidence. `sidecar-lifecycle-smoke` is opt-in mutation evidence for the
standalone sidecar assignment lifecycle. `sidecar-write-smoke`,
`sidecar-setup-smoke`, `sidecar-work-smoke`,
`sidecar-collaboration-smoke`, `sidecar-memory-smoke`, and
`sidecar-assistant-smoke` are opt-in mutation evidence for standalone sidecar
project identity, project setup metadata, project work coordination records,
collaboration metadata records, memory/candidate records, and Project Assistant
proposal/apply records. None of these routes operator project reads or writes
through the sidecar backend.
`HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=all-portable` expands to every
current portable write-authority dogfood switchpoint. It does not make Hecate
runtime side effects or migration cutover Cairnline-owned: root
scan/worktree creation, assignment-start dispatch, Project Assistant
chat/runtime side effects, and migration/rollback remain separate gates.
`HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=project-memory` is an alpha
write-authority dogfood switch for accepted project memory entries:
create/update/delete commits to the embedded Cairnline database first and then
best-effort shadows the row into Hecate-native memory stores. The default is
`none`. `HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=project-memory,memory-candidates`
also makes memory-candidate create/promote/reject Cairnline-first; the
`memory-candidates` switch requires `project-memory` because candidate promotion
creates accepted project memory. Hecate's live memory-candidate authority
surface is create/promote/reject; sidecar delete smoke tests are standalone
Cairnline diagnostics, not Hecate route migration evidence. All other Projects
mutations remain Hecate-owned unless one of the other alpha write-authority
switchpoints is explicitly listed.
Project metadata/default-only PATCHes also best-effort mirror after the Hecate
store commit unless `project-metadata-defaults` is enabled, in which case those
scoped updates commit portable metadata and launch defaults to Cairnline first
and then shadow Hecate's compatibility project row. Project create/delete,
roots, context sources, last-opened-only updates, and mixed metadata/root/source
replacement PATCHes remain Hecate-owned unless their separate switchpoints are
enabled.
Project create also best-effort mirrors portable identity, initial roots,
context sources, and launch defaults after the Hecate store commit unless
`project-identity` is enabled, in which case create/delete commits to Cairnline
first and then shadows Hecate's compatibility project row. Delete restores the
Cairnline snapshot if Hecate compatibility cleanup fails. Identity delete can
also target a Cairnline-only project graph and clean Hecate compatibility shadow
rows without requiring a matching native project row. When embedded replacement
mode is armed with all portable write-authority gaps closed, project-identity
create returns the Cairnline record without creating a native Hecate project
identity row; strict embedded reads then serve the new project from Cairnline.
Project root create/update/delete and root list replacement mutations also
best-effort mirror after the Hecate store commit unless `project-roots` is
enabled, in which case those root mutations plus discovery-result replacement
and worktree-created root record mutations commit to Cairnline first and then
shadow Hecate's compatibility project row. Hecate still performs the root
discovery scan and Git worktree creation side effect. In root authority mode,
discovery and worktree-created root record mutations can resolve project
identity and roots from the embedded Cairnline graph without a Hecate-native
compatibility project row.
Context-source create/update/delete and list replacement mutations likewise
mirror after Hecate commits unless
`project-context-sources` is enabled, in which case those source mutations
plus discovery-result replacement commit to Cairnline first and then shadow
Hecate's compatibility project row. Hecate still performs the workspace scan
for its operator UI. In source authority mode, context-source discovery can use
project identity, roots, and existing sources from the embedded Cairnline graph
without a Hecate-native compatibility project row.
Project skill discovery and
metadata updates also best-effort mirror metadata-only skill records after the
Hecate store commit unless
`HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=project-skills` is enabled, in which
case skill discovery/update commits metadata-only skill records to Cairnline
first and then shadows them back into Hecate. In skill authority mode, discovery
can use roots and context sources from the embedded Cairnline graph without a
Hecate-native compatibility project row. Neither path loads, injects, executes,
or grants permissions from skill bodies. Project role and work-item
mutations likewise mirror coordination metadata after Hecate commits unless
`project-roles` or `project-work-items` is enabled, respectively. Role mirroring
seeds referenced agent-profile metadata and execution posture when available,
and global agent-profile CRUD best-effort mirrors portable profile metadata and
execution posture after Hecate commits unless `agent-profiles` is enabled, in
which case profile create/update/delete commits Cairnline's separate portable
profile and execution-posture records first and shadows Hecate's combined
profile row back into Hecate for compatibility. Assignment create/update/delete
mutations likewise mirror portable metadata after Hecate commits unless
`project-assignments` is enabled, in which case assignment record mutations
commit to Cairnline first and shadow back into Hecate. When these role,
work-item, and assignment authority switches are enabled, the write routes can
validate against embedded Cairnline project/root/work/role records and do not
need a matching Hecate-native compatibility project row before the Cairnline
commit. Committed
assignment-start results, linked-chat reconciliation, collaboration artifact
creation, and handoff create/update/delete mutations also best-effort mirror
portable metadata after Hecate commits, but assignment start/dispatch remains
Hecate-owned. In strict embedded read mode, Hecate-task and external-agent
assignment start can resolve the project, work item, assignment, role, root, and
execution defaults from a Cairnline-only project graph. When the native
project-work store is absent, or when embedded replacement mode is armed,
Hecate claims and progresses the assignment in embedded Cairnline and stores
only task/run or chat-session refs, context packets, and launch timestamps in
Hecate's project assignment runtime overlay; it does not create a native Hecate
project identity row, and it does not advance compatibility assignment rows
with runtime refs. Runtime dispatch, task execution, and external-agent supervision
remain Hecate-owned. Pre-dispatch cleanup and conflict states are mirrored back
into Cairnline so replacement probes do not leave stale claimed assignment rows.
Linked external-agent chat reconciliation can also update the embedded
Cairnline assignment and Hecate runtime overlay when no native project-work row
exists.
Project
memory entries mirror after Hecate commits unless the
`project-memory` Cairnline write-authority switchpoint is enabled; memory
candidates mirror after Hecate commits unless `project-memory,memory-candidates`
is enabled, in which case candidate create/promote/reject commit to Cairnline
first and then shadow back into Hecate. In those memory authority modes,
Hecate validates project identity from the embedded Cairnline graph and does
not need a Hecate-native compatibility project row before committing the
Cairnline record. Collaboration artifact and handoff
routes mirror after Hecate commits unless `project-collaboration` is enabled,
in which case they commit to Cairnline first and shadow back into Hecate.
Collaboration authority accepts existing Cairnline work item, role, and
assignment records as dependency evidence before falling back to Hecate shadows.
Project Assistant draft/propose/apply
ledger mutations likewise best-effort mirror proposal records and apply attempts
after Hecate commits unless `project-assistant-proposals` is enabled, in which
case the proposal ledger commits to Cairnline first and shadows Hecate's
proposal store for compatibility. Confirmed apply uses the enabled Cairnline
authority seams for project create, project metadata/default, root, role,
work-item, assignment, handoff, and memory-candidate actions, but chat/runtime
effects remain Hecate-owned orchestrator capabilities outside Cairnline core.
Other live Projects reads/writes still use Hecate-native
stores until the remaining read routes, write adapter, and migration path are
ready. Current bridge write experiments cover non-authoritative
project/root/source/defaults, agent profiles, skills, roles, work items,
assignment metadata upsert/delete, assignment-start result and linked-chat
reconciliation sync, memory, memory-candidate, create-only collaboration
artifact/evidence/review, and handoff shapes. Backend status reports those
proof seams separately from the live-route `write_adapter_gaps`; only
`project-identity-live-mirror`, `project-metadata-live-mirror`,
`project-roots-live-mirror`,
`project-context-sources-live-mirror`, `project-defaults-live-mirror`,
`agent-profiles-live-mirror`, `project-skills-live-mirror`,
`project-roles-live-mirror`, `project-work-items-live-mirror`,
`project-assignments-live-mirror`,
`project-assignment-start-result-live-mirror`,
`project-assignment-chat-reconcile-live-mirror`,
`project-collaboration-live-mirror`, `project-handoffs-live-mirror`,
`project-memory-live-mirror`, and `project-memory-candidates-live-mirror`, plus
`project-assistant-proposal-ledger-live-mirror` and
`project-assistant-apply-side-effects-live-mirror` are wired to live mutations,
and they are mirror-only unless their explicit Cairnline write-authority
switchpoint is enabled. The portable switchpoints can make identity,
metadata/defaults, roots, context sources, profiles, skills, roles, work items,
assignments, collaboration records, accepted memory, memory candidates, and the
proposal ledger Cairnline-authoritative, while apply side effects become
mixed-authority through the enabled project create, project metadata/default,
root, role/work-item/assignment/handoff, and memory-candidate switchpoints. It
also reports `replacement_ready`, `next_replacement_action`,
`replacement_gates`, and `write_switchpoints` so operators can see the
suggested next step, relevant env-var hints, and the exact read-route,
strict-embedded-read-smoke, write-authority, and migration blockers without
parsing warning prose. Gates and next actions include method-aware `probes`
alongside compatibility `probe_urls`, so Settings can turn the next action into
an ordered, copyable probe checklist and distinguish POST smoke/rehearsal routes
from GET read checks. When the embedded connector, strict embedded read source,
and a configured data directory are active, the strict read-smoke gate is driven
by the same read-only mirror-parity evidence returned by
`GET /hecate/v1/projects/cairnline/mirror-parity`: a missing mirror reports
`not_run`, mirror drift reports `drift_detected`, and an exact mirror with passing
strict embedded route smoke reports `verified`. The migration/rollback gate then
reports `waiting_for_read_smoke` until that evidence is verified, and
`cutover_switch_missing` once the read evidence is clean but the explicit
authoritative storage cutover switch still does not exist. When
`HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded` is armed after strict
embedded reads are verified and all portable write-authority gaps are closed,
backend status treats that mode as the explicit embedded cutover switch, clears
the migration blocker, marks the `migration-cutover` write switchpoint as
`embedded_cutover_armed`, and reports Cairnline as authoritative for portable
Projects coordination state. In that armed mode with all portable
write-authority gaps closed, new Cairnline-authoritative project identity
creates no longer manufacture native Hecate project identity rows;
compatibility shadows remain limited to Hecate-owned runtime/workspace needs.
Once all replacement gates are ready, backend status reports
`status=cairnline_authoritative` and keeps warnings focused on Hecate-owned
runtime/workspace side effects instead of saying Hecate stores remain
authoritative.
It also groups the broad `write_adapter_gaps`
diagnostic list into
`portable_write_gaps`,
`orchestrator_capabilities`, and `migration_blockers`, so durable
coordination-state switchpoint work is separated from Hecate-owned
runtime/workspace capabilities and final cutover work. Settings shows the same
backend-status summary, shows copyable next-action configuration hints, turns
the next action's probes into a run-in-order checklist, keeps replacement gates
as supporting evidence, and lists write-switchpoint authority/state rows under
Project coordination for local operator inspection. The reported replacement
target is embedded Cairnline first: Hecate should make the embedded Cairnline
database the Projects source of truth before treating an external sidecar as the
standalone/interoperability boundary. `replacement_mode=disabled|embedded`
reports the explicit operator cutover arm; `embedded` is only valid with the
embedded connector and strict embedded read source, and it does not bypass the
read, write-authority, migration, rollback, or Hecate-owned runtime side-effect
gates. The initial embedded dogfood and sidecar-to-embedded connector next
actions point at backend-status and embedded read-model diagnostics; sidecar
probe/connect routes remain separate standalone MCP diagnostics. Once portable
write gaps are closed, the next action becomes `run-strict-embedded-read-smoke` until strict
embedded mirror parity and route-smoke evidence are verified; that action
reports strict embedded rehearsal hints for
`HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded`,
`HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded`, and
`HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=all-portable`; after strict embedded
read smoke is verified, the next action becomes implementing the missing
migration cutover switch by arming
`HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded`. That status cutover does
not move Hecate-owned runtime/workspace capabilities such as assignment dispatch
or Project Assistant apply side effects into Cairnline core. `GET
/hecate/v1/projects/cairnline/mirror-parity` compares the existing embedded
mirror database with Hecate's current stores without creating or repairing it;
`GET /hecate/v1/projects/{id}/cairnline/embedded-read-model` reads operations,
activity, and launch-packet projections directly from the existing embedded
mirror database without seeding from Hecate stores; `GET
/hecate/v1/projects/{id}/cairnline/embedded-parity-report` compares that live
mirror read model with Hecate's native cockpit projections, including rendered
work-item route shape with embedded assignments plus collaboration
artifact/handoff route-shape counts; and
`POST /hecate/v1/projects/cairnline/sync` remains the explicit all-project
rebuild/rehearsal action.

Deployment-specific notes:

- The docker image **defaults to `sqlite`** for every durable subsystem,
  persisting state at `HECATE_SQLITE_PATH` (default `/data/hecate.db` on the
  `hecate-data` volume). This is why `docker compose up` keeps settings,
  provider credentials, projects, usage events, tasks, and chat sessions
  across restarts with no extra config.
- To make Docker ephemeral, override the backend via `.env` or compose env:
  `HECATE_BACKEND=memory`.
- Hosted runtimes can use Postgres with `HECATE_BACKEND=postgres` and
  `HECATE_POSTGRES_URL=postgres://...` (or `DATABASE_URL`). Optional knobs:
  `HECATE_POSTGRES_TABLE_PREFIX`, `HECATE_POSTGRES_MAX_OPEN_CONNS`, and
  `HECATE_POSTGRES_MAX_IDLE_CONNS`.
- Postgres coverage is intentionally checked in two layers: unit tests pin the
  config fan-out, SQL-client requirement, and telemetry labels; the opt-in
  `HECATE_POSTGRES_TEST_URL=... go test ./cmd/hecate -run TestPostgresStoresMigrateWhenDatabaseURLProvided`
  smoke exercises every Postgres-backed store against a real database.
- Projects are the durable identity foundation for project-scoped history,
  defaults, memory, profiles, skills, context sources, and project work. Chat
  sessions and tasks can carry `project_id` for UI grouping and runtime
  inspection, and context packets snapshot project-scoped memory/source
  decisions. Native project assignments can include bounded project memory and
  portable `AGENTS.md` prompt context when the resolved profile asks for it;
  Hecate-owned project chat has an explicit bounded project prelude, while
  External Agent paths keep project memory/source bodies metadata-only unless a
  future adapter-specific prompt policy is added.
- When `HECATE_BACKEND=sqlite` or `postgres`, Hecate runs a startup reconcile
  pass that flips any pending external-agent approvals from a prior process to
  `status=timed_out`, `path=startup_reconcile` before serving requests, so an
  orphaned waiter never appears actionable in the operator UI. See
  [`runtime-api.md`](../runtime/runtime-api.md) for the wire shape.

## Rate limiting

Rate limiting is a per-process token bucket. It is disabled by default so first-run local testing does not surprise users.

| Variable                    | Default | Notes                                             |
| --------------------------- | ------: | ------------------------------------------------- |
| `HECATE_RATE_LIMIT_ENABLED` | `false` | Enables request rate limits.                      |
| `HECATE_RATE_LIMIT_RPM`     |    `60` | Steady-state refill rate.                         |
| `HECATE_RATE_LIMIT_BURST`   |     `0` | Optional burst capacity. `0` means "same as RPM". |

Over-limit requests return `429 Too Many Requests` with `code: "rate_limit_exceeded"` and standard `X-RateLimit-*` headers.
