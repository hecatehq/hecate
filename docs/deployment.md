# Deployment

The [Quick Start](../README.md#quick-start) covers `docker run` end-to-end. This page is the reference for everything past the first run: pinning images, the compose profile, the binary install, storage tiers, and rate limits.

Hecate defaults to `127.0.0.1:8765` and enforces same-origin for browser requests; there is no built-in auth layer. If you change `GATEWAY_ADDRESS` to expose Hecate beyond the local machine, put your own auth, firewall, or reverse proxy in front.

## Contents

- [Image pinning](#image-pinning)
- [Binary install](#binary-install)
- [Desktop app](#desktop-app)
- [Resetting state](#resetting-state)
- [Storage backends](#storage-backends)
- [External-agent startup knobs](#external-agent-startup-knobs)
- [Rate limiting](#rate-limiting)

## Image pinning

`docker-compose.yml` references `ghcr.io/chicoxyzzy/hecate:latest`, a multi-arch (`linux/amd64`, `linux/arm64`) image published from this repo on every `v*` tag. A fresh host can `docker compose pull` and start without a build step.

To pin to a specific release, replace `:latest` with the published tag (no `v` prefix — goreleaser uses the bare semver as the docker tag). Example for the current alpha:

```yaml
# docker-compose.yml
image: ghcr.io/chicoxyzzy/hecate:0.1.0-alpha.15
```

Pinning is recommended for any deployment beyond local experimentation — `:latest` floats over alpha increments that may include schema or config changes.

When the working tree is a checkout of the source, `docker compose up` rebuilds locally from the bundled `Dockerfile` instead of pulling. Useful for testing changes; remove the `image:` line or run `docker compose build` first if you want the local build to be the canonical artifact.

The Docker image starts `hecate` by default and also includes `hecate-acp` at
`/usr/local/bin/hecate-acp` so the image carries the same companion bridge as
the tarballs and native app. The container sets
`GATEWAY_PUBLIC_URL=http://127.0.0.1:8765`, which is the host URL users normally
reach through `-p 8765:8765` or `docker compose`.

If a `docker run` (or `docker compose up`) errors with `bind: address already in use` on `:8765`, a previous `make dev` / `make run` / `./hecate` is still listening from another shell. Free the port with `make stop` and retry; `make dev`, `make run`, and `make serve` also auto-run `stop` before starting so successive launches don't pile up.

## Binary install

The release workflow publishes static, single-file binaries for `linux+darwin × amd64+arm64` to GitHub Releases. Skip Docker if you'd rather run the gateway directly:

```bash
# pick the right tarball for your OS / arch
curl -LO https://github.com/chicoxyzzy/hecate/releases/download/v0.1.0-alpha.15/hecate_0.1.0-alpha.15_linux_amd64.tar.gz
tar -xzf hecate_0.1.0-alpha.15_linux_amd64.tar.gz
./hecate
```

The gateway embeds the React operator UI, listens on `127.0.0.1:8765` by default, and stores state under `GATEWAY_DATA_DIR` (default `.data/`). No additional runtime dependencies — the binaries are statically linked and CGO-free.

To pin the data directory to a known location:

```bash
GATEWAY_DATA_DIR=/var/lib/hecate ./hecate
```

For systemd, launchd, or supervisor wrappers, the only requirements are: the working directory is writable for `GATEWAY_DATA_DIR`, port 8765 is available, and `.env` (if used) sits in the working directory or is sourced into the unit file. The binary path itself can live anywhere on `$PATH`.

Available tarballs for `v0.1.0-alpha.15`:

- `hecate_0.1.0-alpha.15_linux_amd64.tar.gz`
- `hecate_0.1.0-alpha.15_linux_arm64.tar.gz`
- `hecate_0.1.0-alpha.15_darwin_amd64.tar.gz`
- `hecate_0.1.0-alpha.15_darwin_arm64.tar.gz`

Each tarball includes `hecate`, `hecate-acp`, `LICENSE`, and `README.md`.
Verify integrity against `checksums.txt` published alongside the release.

The gateway writes `hecate.runtime.json` into `GATEWAY_DATA_DIR` on startup.
Local helper processes such as `hecate-acp` use it to discover the active
gateway URL before falling back to `http://127.0.0.1:8765`.

## Desktop app

A third install path for personal use on a laptop. Same release, different artifacts:

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `Hecate_X.Y.Z_amd64.deb`, `Hecate_X.Y.Z_amd64.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

The bundle is a Tauri 2.x chrome around the same `hecate` binary used in Docker and the tarballs. On launch the app spawns Hecate as a sidecar on a free loopback port, polls `/healthz`, then loads the embedded UI directly.

State lives in the platform data dir, not next to the binary:

| Platform | Data dir |
|---|---|
| macOS | `~/Library/Application Support/io.github.chicoxyzzy.hecate/` |
| Linux | `~/.local/share/io.github.chicoxyzzy.hecate/` |
| Windows | `%APPDATA%\io.github.chicoxyzzy.hecate\` |

Bundles are not yet code-signed:

- **macOS Gatekeeper** blocks the first launch with "Apple cannot check it for malicious software." Right-click `Hecate.app` → **Open**, confirm in the dialog. Subsequent launches work normally.
- **Windows SmartScreen** shows a "Windows protected your PC" warning. Click **More info** → **Run anyway**. Reputation builds over hundreds of installs.
- **Linux** has no Gatekeeper-equivalent. `.deb` installs as a normal package; `.AppImage` needs `chmod +x` before running.

Desktop app distinct from `docker run` / bare binary:

- No port conflict with a separately-running gateway — the app picks a free loopback port at launch.
- Quitting the app via `cmd+Q` (macOS) / **File → Quit** (Windows / Linux) kills the sidecar; closing only the window does not (macOS hides the window, Windows / Linux behave per WM).
- Multi-machine users keep separate config per OS — settings on macOS don't migrate to Linux even on the same release.

Full state, footguns, and roadmap: [`desktop-app.md`](desktop-app.md).

## External-agent startup knobs

Web, Docker, tarball, and native-app launches use the same gateway env vars for
Agent Chat. The native app spawns the bundled gateway sidecar with these env
vars inherited from the app process; Docker reads them from `.env` / compose;
bare binaries read the shell environment.

| Env var | Default | Applies to |
|---|---|---|
| `HECATE_AGENT_ADAPTERS_DIR` | platform user cache | Managed Codex / Claude ACP launcher scripts |
| `GATEWAY_AGENT_CHAT_MAX_TURNS_PER_SESSION` | `0` | Per-session user→assistant turn ceiling |
| `GATEWAY_AGENT_CHAT_MAX_SESSION_DURATION` | `0s` | Wall-clock age ceiling before new turns are rejected |
| `GATEWAY_AGENT_CHAT_IDLE_TIMEOUT` | `0s` | Background idle auto-close sweeper |

Managed launchers are small wrapper scripts around a local package runner such
as `npx`; Hecate garbage-collects stale launcher names at startup. If you move
Node/npm managers, restart Hecate and use `POST
/v1/agent-adapters/{id}/refresh-launcher` to recreate the affected wrapper.
The Settings → External agents **Test** action calls `POST
/v1/agent-adapters/{id}/probe`, which re-runs discovery and performs the ACP
handshake so login/billing problems are visible before a chat fails.

## Resetting state

To wipe the stack back to first-run — removes the `hecate-data` volume (SQLite db) and regenerates state on the next `docker compose up`:

```bash
make reset-docker
```

For local (non-Docker) development resets, see [`development.md`](development.md#reset-state).

## Storage backends

Hecate keeps the storage model intentionally boring: each subsystem chooses a backend independently — either `memory` or `sqlite`.

| Subsystem | Env var | memory | sqlite |
|---|---|---:|---:|
| Control plane | `GATEWAY_CONTROL_PLANE_BACKEND` | local default | Docker default |
| Provider credentials | `GATEWAY_PROVIDER_STORE_BACKEND` | local default | Docker default |
| Pricebook | `GATEWAY_PRICEBOOK_BACKEND` | local default | Docker default |
| Budget / balances | `GATEWAY_BUDGET_BACKEND` | local default | Docker default |
| Usage ledger | `GATEWAY_USAGE_BACKEND` | local default | Docker default |
| Audit events | `GATEWAY_AUDIT_BACKEND` | local default | Docker default |
| Trace snapshots | `GATEWAY_TRACE_STORE_BACKEND` | local default | Docker default |
| Retention history | `GATEWAY_RETENTION_HISTORY_BACKEND` | local default | Docker default |
| Chat sessions + agent-chat approvals/grants | `GATEWAY_CHAT_SESSIONS_BACKEND` | local default | Docker default |
| Tasks | `GATEWAY_TASKS_BACKEND` | local default | Docker default |
| Task queue | `GATEWAY_TASK_QUEUE_BACKEND` | local default | Docker default |

Deployment-specific notes:

- The docker image **defaults to `sqlite`** for every durable subsystem, persisting state at `GATEWAY_SQLITE_PATH` (default `/data/hecate.db` on the `hecate-data` volume). This is why `docker compose up` keeps pricebook, tasks, and chat sessions across restarts with no extra config.
- To make any subsystem ephemeral in docker, override its backend via `.env` or compose env: `GATEWAY_TASKS_BACKEND=memory`, etc.
- `GATEWAY_CHAT_SESSIONS_BACKEND=sqlite` covers the full agent-chat state bundle: sessions, messages, external-adapter approvals, and operator-authored grants. On startup the gateway flips any pending approvals from a prior process to `status=timed_out`, `path=startup_reconcile` before serving requests, so an orphaned waiter never appears actionable in the operator UI. See [`runtime-api.md`](runtime-api.md) for the wire shape.

## Rate limiting

Rate limiting is a per-process token bucket. It is disabled by default so first-run local testing does not surprise users.

| Variable | Default | Notes |
|---|---:|---|
| `GATEWAY_RATE_LIMIT_ENABLED` | `false` | Enables request rate limits. |
| `GATEWAY_RATE_LIMIT_RPM` | `60` | Steady-state refill rate. |
| `GATEWAY_RATE_LIMIT_BURST` | `0` | Optional burst capacity. `0` means "same as RPM". |

Over-limit requests return `429 Too Many Requests` with `code: "rate_limit_exceeded"` and standard `X-RateLimit-*` headers.
