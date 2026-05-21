# Deployment

The [Quick Start](../README.md#quick-start) covers `docker run` end-to-end. This page is the reference for everything past the first run: pinning images, the compose profile, the binary install, storage tiers, and rate limits.

Hecate defaults to `127.0.0.1:8765` and enforces same-origin for browser requests, but same-origin is not a network security boundary. If you change `HECATE_ADDRESS` to expose Hecate beyond the local machine, put your own access controls, firewall, or reverse proxy in front. The current security model is documented in [Security](security.md).

## Contents

- [Image pinning](#image-pinning)
- [Binary install](#binary-install)
- [Desktop app](#desktop-app)
- [Resetting state](#resetting-state)
- [Bootstrap key and backups](#bootstrap-key-and-backups)
- [Storage backend](#storage-backend)
- [External-agent startup knobs](#external-agent-startup-knobs)
- [Rate limiting](#rate-limiting)

## Image pinning

`docker-compose.yml` references `ghcr.io/hecatehq/hecate:latest`, a multi-arch (`linux/amd64`, `linux/arm64`) image published from this repo on every `v*` tag. A fresh host can `docker compose pull` and start without a build step.

To pin to a specific release, replace `:latest` with the published tag (no `v` prefix — goreleaser uses the bare semver as the docker tag). Example for the current alpha:

```yaml
# docker-compose.yml
image: ghcr.io/hecatehq/hecate:0.1.0-alpha.35
```

Pinning is recommended for any deployment beyond local experimentation — `:latest` floats over alpha increments that may include schema or config changes.

When the working tree is a checkout of the source, `docker compose up` rebuilds locally from the bundled `Dockerfile` instead of pulling. Useful for testing changes; remove the `image:` line or run `docker compose build` first if you want the local build to be the canonical artifact.

The Docker image starts `hecate` by default and also includes `hecate-acp` at
`/usr/local/bin/hecate-acp` so the image carries the same companion bridge as
the tarballs and native app. The container sets
`HECATE_PUBLIC_URL=http://127.0.0.1:8765`, which is the host URL users normally
reach through `-p 8765:8765` or `docker compose`.

If a `docker run` (or `docker compose up`) errors with `bind: address already in use` on `:8765`, a previous `just dev` / `just run` / `./hecate` is still listening from another shell. Free the port with `just stop` and retry; `just dev`, `just run`, and `just serve` also auto-run `stop` before starting so successive launches don't pile up.

## Binary install

The release workflow publishes static, single-file binaries for `linux+darwin × amd64+arm64` to GitHub Releases. Skip Docker if you'd rather run Hecate directly from the terminal:

```bash
# pick the right tarball for your OS / arch
curl -LO https://github.com/hecatehq/hecate/releases/download/v0.1.0-alpha.35/hecate_0.1.0-alpha.35_linux_amd64.tar.gz
tar -xzf hecate_0.1.0-alpha.35_linux_amd64.tar.gz
./hecate
```

The `hecate` binary starts the gateway service, embeds the React operator UI, listens on `127.0.0.1:8765` by default, and stores state under `HECATE_DATA_DIR` (default `.data/`). No additional runtime dependencies — the binaries are statically linked and CGO-free.

To pin the data directory to a known location:

```bash
HECATE_DATA_DIR=/var/lib/hecate ./hecate
```

For systemd, launchd, or supervisor wrappers, the only requirements are: the working directory is writable for `HECATE_DATA_DIR`, port 8765 is available, and `.env` (if used) sits in the working directory or is sourced into the unit file. The binary path itself can live anywhere on `$PATH`.

Available tarballs for `v0.1.0-alpha.35`:

- `hecate_0.1.0-alpha.35_linux_amd64.tar.gz`
- `hecate_0.1.0-alpha.35_linux_arm64.tar.gz`
- `hecate_0.1.0-alpha.35_darwin_amd64.tar.gz`
- `hecate_0.1.0-alpha.35_darwin_arm64.tar.gz`

Each tarball includes `hecate`, `hecate-acp`, `LICENSE`, and `README.md`.
Verify integrity against `checksums.txt` published alongside the release.

The gateway writes `hecate.runtime.json` into `HECATE_DATA_DIR` on startup.
Local helper processes such as `hecate-acp` use it to discover the active
gateway URL before falling back to `http://127.0.0.1:8765`.

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

- **macOS** is signed with a Developer ID Application certificate and notarized when the bundle was produced by a release-workflow run (`.github/workflows/release.yml` — tag push or manual `workflow_dispatch`) with the `APPLE_*` / `KEYCHAIN_PASSWORD` repo secrets configured (see [`macos-signing.md`](macos-signing.md)). Such bundles launch with no Gatekeeper warning and drag-install to `/Applications` cleanly. Earlier alpha bundles, plus any release built before the secrets landed (or any future fork build that doesn't have access to them), remain unsigned — Gatekeeper blocks the first launch with "Apple cannot check it for malicious software." Right-click `Hecate.app` → **Open**, confirm in the dialog. Subsequent launches work normally.
- **Windows** bundles are not yet signed. SmartScreen shows a "Windows protected your PC" warning. Click **More info** → **Run anyway**. Reputation builds over hundreds of installs; full Authenticode signing is roadmap.
- **Linux** has no Gatekeeper-equivalent. `.deb` installs as a normal package; `.AppImage` needs `chmod +x` before running.

Desktop app distinct from `docker run` / bare binary:

- No port conflict with a separately-running gateway — the app picks a free loopback port at launch.
- Quitting the app via `cmd+Q` (macOS) / **File → Quit** (Windows / Linux) kills the sidecar; closing only the window does not (macOS hides the window, Windows / Linux behave per WM).
- Multi-machine users keep separate config per OS — settings on macOS don't migrate to Linux even on the same release.

Full state, footguns, and roadmap: [`desktop-app.md`](desktop-app.md).

## External-agent startup knobs

Web, Docker, tarball, and native-app launches use the same gateway env vars for
chat sessions and external-agent adapters. The native app spawns the bundled
`hecate` runtime in gateway mode with these env vars inherited from the app
process; Docker reads them from `.env` / compose; bare binaries read the shell
environment.

| Env var                             | Default             | Applies to                                           |
| ----------------------------------- | ------------------- | ---------------------------------------------------- |
| `HECATE_AGENT_ADAPTERS_DIR`         | platform user cache | Managed Codex / Claude ACP launcher scripts          |
| `HECATE_CHAT_MAX_TURNS_PER_SESSION` | `0`                 | Per-session user→assistant turn ceiling              |
| `HECATE_CHAT_MAX_SESSION_DURATION`  | `0s`                | Wall-clock age ceiling before new turns are rejected |
| `HECATE_CHAT_IDLE_TIMEOUT`          | `0s`                | Background idle auto-close sweeper                   |

Managed launchers are small wrapper scripts around a local package runner such
as `npx`; Hecate garbage-collects stale launcher names at startup. If you move
Node/npm managers, restart Hecate and use `POST
/hecate/v1/agent-adapters/{id}/refresh-launcher` to recreate the affected wrapper.
Connections probes adapters when the workspace opens; the probe calls
`POST /hecate/v1/agent-adapters/{id}/probe`, which re-runs discovery and
performs the ACP handshake so login/billing problems are visible before a chat
fails.

## Resetting state

To wipe the stack back to first-run — removes the `hecate-data` volume (SQLite db) and regenerates state on the next `docker compose up`:

```bash
just reset-docker
```

For local (non-Docker) development resets, see [`development.md`](development.md#reset-state).

## Bootstrap key and backups

Encrypted local credentials depend on two pieces of state:

- the settings database, usually `hecate.db` when the relevant storage backend
  is `sqlite`;
- the bootstrap control-plane key, loaded from
  `HECATE_CONTROL_PLANE_SECRET_KEY`, `HECATE_BOOTSTRAP_FILE`, or
  `hecate.bootstrap.json` in the data directory.

Back up and restore those pieces together when you want provider keys, external
agent credentials, or encrypted MCP literals to remain usable. Restoring the
database without the matching bootstrap key leaves encrypted secrets
undecryptable; restore the old key or re-enter the credentials from the
operator UI.

Docker's default `/data` volume stores both `/data/hecate.db` and the default
bootstrap file, so backing up the volume is enough for the default path. If you
override `HECATE_CONTROL_PLANE_SECRET_KEY`, treat the env var as the source
that seeds the bootstrap key, not as env-only storage: Hecate persists that key
to `HECATE_BOOTSTRAP_FILE` or the default bootstrap file on startup, overwriting
any existing bootstrap key, and the bootstrap path must be writable. Back up the
env/secret-manager value and the bootstrap file with the same care as the
database; if they diverge, the env value wins on the next startup.

## Storage backend

Hecate keeps the storage model intentionally boring: one process-wide backend
selector controls all Hecate-owned durable state.

| Env var          | `memory`                         | `sqlite`                                         |
| ---------------- | -------------------------------- | ------------------------------------------------ |
| `HECATE_BACKEND` | local default; resets on restart | Docker default; persists to `HECATE_SQLITE_PATH` |

The backend covers settings, encrypted provider credentials, audit events,
provider health history, usage events, retention history, projects, chat
sessions, external-agent approvals/grants, tasks, and the task queue.

Deployment-specific notes:

- The docker image **defaults to `sqlite`** for every durable subsystem,
  persisting state at `HECATE_SQLITE_PATH` (default `/data/hecate.db` on the
  `hecate-data` volume). This is why `docker compose up` keeps settings,
  provider credentials, projects, usage events, tasks, and chat sessions
  across restarts with no extra config.
- To make Docker ephemeral, override the backend via `.env` or compose env:
  `HECATE_BACKEND=memory`.
- Projects are the durable identity foundation for future project-scoped
  defaults, memory, and context; chats and tasks are not linked to `project_id`
  yet in this first slice.
- When `HECATE_BACKEND=sqlite`, Hecate runs a startup reconcile pass that flips
  any pending external-agent approvals from a prior process to
  `status=timed_out`, `path=startup_reconcile` before serving requests, so an
  orphaned waiter never appears actionable in the operator UI. See
  [`runtime-api.md`](runtime-api.md) for the wire shape.

## Rate limiting

Rate limiting is a per-process token bucket. It is disabled by default so first-run local testing does not surprise users.

| Variable                    | Default | Notes                                             |
| --------------------------- | ------: | ------------------------------------------------- |
| `HECATE_RATE_LIMIT_ENABLED` | `false` | Enables request rate limits.                      |
| `HECATE_RATE_LIMIT_RPM`     |    `60` | Steady-state refill rate.                         |
| `HECATE_RATE_LIMIT_BURST`   |     `0` | Optional burst capacity. `0` means "same as RPM". |

Over-limit requests return `429 Too Many Requests` with `code: "rate_limit_exceeded"` and standard `X-RateLimit-*` headers.
