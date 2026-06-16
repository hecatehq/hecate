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
and External Agent CLIs. It does not install `bwrap` by default; Hecate still
applies its process policy, env sanitisation, and approval gates, but
filesystem/network isolation inside the container is normally reported as
`none`.

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
image: ghcr.io/hecatehq/hecate:0.2.0-alpha.1
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

The runtime stage defaults to `node:24-trixie-slim` through the `NODE_IMAGE`
build arg so the image has a maintained Debian userspace for shell, npm, and
project dependency workflows.

The embedded terminal can launch inside this image because `/bin/sh`, `bash`,
and a PTY-capable userspace are present. Local runtimes accept terminal tickets
only from loopback clients. Hosted runtimes expose the same operator terminal
through the protected ticket-mint route; the WebSocket consumes that short-lived
ticket because browsers cannot attach the runtime identity headers during
upgrade. Set `HECATE_EMBEDDED_TERMINAL=false` when a deployment requires all
command execution to stay behind task-runtime approvals, sandboxing, timeouts,
and output caps. When the embedded terminal is enabled in a hosted runtime, the
container, VM, or dedicated OS-user boundary is the isolation boundary for those
operator shell commands.

The bundled External Agent CLIs are pinned by Docker build args so a Hecate
release does not silently move to a newer top-level agent package. The Cursor
Agent installer is fetched from Cursor's official install URL and checked
against a pinned SHA-256 before it runs; update the checksum only after
reviewing the new installer contents.

If a `docker run` (or `docker compose up`) errors with `bind: address already in use` on `:8765`, a previous `just dev` / `just run` / `./hecate` is still listening from another shell. Free the port with `just stop` and retry; `just dev`, `just run`, and `just serve` also auto-run `stop` before starting so successive launches don't pile up.

## Binary install

The release workflow publishes static, single-file binaries for `linux+darwin × amd64+arm64` to GitHub Releases. Skip Docker if you'd rather run Hecate directly from the terminal:

```bash
# pick the right tarball for your OS / arch
curl -LO https://github.com/hecatehq/hecate/releases/download/v0.2.0-alpha.1/hecate_0.2.0-alpha.1_linux_amd64.tar.gz
tar -xzf hecate_0.2.0-alpha.1_linux_amd64.tar.gz
./hecate
```

The `hecate` binary starts the gateway service, embeds the React operator UI, listens on `127.0.0.1:8765` by default, and stores state under `HECATE_DATA_DIR` (default `.data/`). No additional runtime dependencies — the binaries are statically linked and CGO-free.

To bind the bare binary beyond loopback, set both variables and provide your own network protection:

```bash
HECATE_ADDRESS=0.0.0.0:8765 HECATE_ALLOW_NON_LOOPBACK_BIND=1 ./hecate
```

To pin the data directory to a known location:

```bash
HECATE_DATA_DIR=/var/lib/hecate ./hecate
```

For systemd, launchd, or supervisor wrappers, the only requirements are: the working directory is writable for `HECATE_DATA_DIR`, port 8765 is available, and `.env` (if used) sits in the working directory or is sourced into the unit file. The binary path itself can live anywhere on `$PATH`.

Available tarballs for `v0.2.0-alpha.1`:

- `hecate_0.2.0-alpha.1_linux_amd64.tar.gz`
- `hecate_0.2.0-alpha.1_linux_arm64.tar.gz`
- `hecate_0.2.0-alpha.1_darwin_amd64.tar.gz`
- `hecate_0.2.0-alpha.1_darwin_arm64.tar.gz`

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

| Env var                             | Default             | Applies to                                           |
| ----------------------------------- | ------------------- | ---------------------------------------------------- |
| `HECATE_AGENT_ADAPTERS_DIR`         | platform user cache | Managed Codex / Claude ACP launcher scripts          |
| `HECATE_EMBEDDED_TERMINAL`          | `true`              | Enables the operator embedded terminal surface       |
| `HECATE_CHAT_MAX_TURNS_PER_SESSION` | `0`                 | Per-session user→assistant turn ceiling              |
| `HECATE_CHAT_MAX_SESSION_DURATION`  | `0s`                | Wall-clock age ceiling before new turns are rejected |
| `HECATE_CHAT_IDLE_TIMEOUT`          | `0s`                | Background idle auto-close sweeper                   |

Managed launchers are small wrapper scripts around a local package runner such
as `npx`; Hecate garbage-collects stale launcher names at startup. If you move
Node/npm managers, restart Hecate and use `POST
/hecate/v1/agent-adapters/{id}/refresh-launcher` to recreate the affected wrapper.
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
  broader chat/external-agent source-content policy remains follow-up work.
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
