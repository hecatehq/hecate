# Deployment

The [Quick Start](../README.md#quick-start) covers `docker run` end-to-end. This page is the reference for everything past the first run: pinning images, the compose profile, the binary install, the auth modes (admin token, loopback bootstrap, auth-disabled), single-user vs multi-tenant, lost-token recovery, storage tiers, and rate limits.

## Contents

- [Image pinning](#image-pinning)
- [Binary install](#binary-install)
- [Desktop app](#desktop-app)
- [Optional services (compose profiles)](#optional-services-compose-profiles)
- [Auth and generated state](#auth-and-generated-state)
  - [Bootstrap handshake (loopback only)](#bootstrap-handshake-loopback-only)
  - [Single-user vs multi-tenant](#single-user-vs-multi-tenant)
- [Recovering a lost admin token](#recovering-a-lost-admin-token)
- [Resetting state](#resetting-state)
- [Storage backends](#storage-backends)
- [Rate limiting](#rate-limiting)

## Image pinning

`docker-compose.yml` references `ghcr.io/chicoxyzzy/hecate:latest`, a multi-arch (`linux/amd64`, `linux/arm64`) image published from this repo on every `v*` tag. A fresh host can `docker compose pull` and start without a build step.

To pin to a specific release, replace `:latest` with the published tag (no `v` prefix — goreleaser uses the bare semver as the docker tag). Example for the current alpha:

```yaml
# docker-compose.yml
image: ghcr.io/chicoxyzzy/hecate:0.1.0-alpha.7
```

Pinning is recommended for any deployment beyond local experimentation — `:latest` floats over alpha increments that may include schema or config changes.

When the working tree is a checkout of the source, `docker compose up` rebuilds locally from the bundled `Dockerfile` instead of pulling. Useful for testing changes; remove the `image:` line or run `docker compose build` first if you want the local build to be the canonical artifact.

If a `docker run` (or `docker compose up`) errors with `bind: address already in use` on `:8765`, a previous `make dev` / `make run` / `./hecate` is still listening from another shell. Free the port with `make stop` and retry; `make dev`, `make run`, and `make serve` also auto-run `stop` before starting so successive launches don't pile up.

## Binary install

The release workflow publishes static, single-file binaries for `linux+darwin × amd64+arm64` to GitHub Releases. Skip Docker if you'd rather run the gateway directly:

```bash
# pick the right tarball for your OS / arch
curl -LO https://github.com/chicoxyzzy/hecate/releases/download/v0.1.0-alpha.7/hecate_0.1.0-alpha.7_linux_amd64.tar.gz
tar -xzf hecate_0.1.0-alpha.7_linux_amd64.tar.gz
./hecate
```

The binary embeds the React operator UI, listens on `:8765` by default, generates an admin bearer token on first boot (saved under `GATEWAY_DATA_DIR`, default `.data/`), and prints it once to stderr. No additional runtime dependencies — the binary is statically linked and CGO-free.

To pin the data directory to a known location:

```bash
GATEWAY_DATA_DIR=/var/lib/hecate ./hecate
```

For systemd, launchd, or supervisor wrappers, the only requirements are: the working directory is writable for `GATEWAY_DATA_DIR`, port 8765 is available, and `.env` (if used) sits in the working directory or is sourced into the unit file. The binary path itself can live anywhere on `$PATH`.

Available tarballs for `v0.1.0-alpha.7`:

- `hecate_0.1.0-alpha.7_linux_amd64.tar.gz`
- `hecate_0.1.0-alpha.7_linux_arm64.tar.gz`
- `hecate_0.1.0-alpha.7_darwin_amd64.tar.gz`
- `hecate_0.1.0-alpha.7_darwin_arm64.tar.gz`

Each tarball includes the binary plus `LICENSE` and `README.md`. Verify integrity against `checksums.txt` published alongside the release.

## Desktop app

A third install path for single-user / personal use on a laptop. Same release, different artifacts:

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `hecate-app_X.Y.Z_amd64.deb`, `hecate-app_X.Y.Z_amd64.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

The bundle is a Tauri 2.x chrome around the same `hecate` binary used in Docker and the tarballs. On launch the app spawns hecate as a sidecar on a free loopback port, polls `/healthz`, then loads the embedded UI directly — same-origin loopback means the bootstrap-token handshake auto-logs you in without a token paste prompt.

State lives in the platform data dir, not next to the binary:

| Platform | Data dir |
|---|---|
| macOS | `~/Library/Application Support/com.hecate.app/` |
| Linux | `~/.local/share/com.hecate.app/` |
| Windows | `%APPDATA%\com.hecate.app\` |

Bundles are not yet code-signed:

- **macOS Gatekeeper** blocks the first launch with "Apple cannot check it for malicious software." Right-click `Hecate.app` → **Open**, confirm in the dialog. Subsequent launches work normally.
- **Windows SmartScreen** shows a "Windows protected your PC" warning. Click **More info** → **Run anyway**. Reputation builds over hundreds of installs.
- **Linux** has no Gatekeeper-equivalent. `.deb` installs as a normal package; `.AppImage` needs `chmod +x` before running.

Desktop app distinct from `docker run` / bare binary:

- No port conflict with a separately-running gateway — the app picks a free loopback port at launch.
- Quitting the app via `cmd+Q` (macOS) / **File → Quit** (Windows / Linux) kills the sidecar; closing only the window does not (macOS hides the window, Windows / Linux behave per WM).
- Multi-machine users keep separate config per OS — settings on macOS don't migrate to Linux even on the same release.

Full state, footguns, and roadmap: [`desktop-app.md`](desktop-app.md).

## Optional services (compose profiles)

```bash
docker compose --profile postgres up    # adds Postgres on :5432 for durable state
```

Profiles are off by default so a bare `docker compose up` stays "just the gateway" with no extra containers.

To use the Postgres profile across subsystems, point each backend at it via env vars in `.env`:

```bash
GATEWAY_CONTROL_PLANE_BACKEND=postgres
GATEWAY_TASKS_BACKEND=postgres
# ... etc, see Storage backends below
POSTGRES_DSN=postgres://hecate:hecate@postgres:5432/hecate?sslmode=disable
```

## Auth and generated state

Hecate can start with almost no secrets in the environment. If `GATEWAY_AUTH_TOKEN` is unset, the gateway generates an admin bearer token on first run, prints it once, and stores bootstrap metadata under `GATEWAY_DATA_DIR`.

`PROVIDER_*_API_KEY`, `PROVIDER_*_BASE_URL`, and `PROVIDER_*_DEFAULT_MODEL` env vars seed the runtime provider registry but do not auto-add the provider to the Providers tab. Operators who want a provider visible and editable in the UI add it explicitly via the modal; env vars are a deployment-time convenience for first boot, not a UI source of truth. See [docs/providers.md](providers.md#env-configured-providers).

| Variable | Default | Notes |
|---|---|---|
| `GATEWAY_AUTH_TOKEN` | generated | Admin bearer token. Prefer the generated first-run token for local and single-host setups. |
| `GATEWAY_AUTH_DISABLED` | `false` | When `true`, the gateway accepts unauthenticated requests and reports `source=auth_disabled` on `/v1/whoami`. Use it when an upstream reverse proxy already terminates auth, or for fully-controlled local setups. |
| `GATEWAY_MULTI_TENANT` | `false` (Docker) / `false` (local) | When `true`, exposes tenant + API-key management surfaces in Settings and tenant-readable observability endpoints. Default deployments ship single-user; flip the flag when more than one consumer needs scoped access. See [`tenants.md`](tenants.md). |
| `GATEWAY_DATA_DIR` | `.data` locally, `/data` in Docker | Holds bootstrap metadata and local state files. |
| `GATEWAY_CONTROL_PLANE_SECRET_KEY` | development fallback | Encrypts persisted provider credentials. Set a strong value before sharing a deployment. |

### Bootstrap handshake (loopback only)

`GET /v1/bootstrap-token` returns the gateway-managed admin bearer token, but only when **all three** conditions hold:

- The request comes from a loopback address (`127.0.0.1`, `::1`). `X-Forwarded-For` is ignored — the check looks at the actual TCP peer.
- The `Origin` header host matches the request host (or `Origin` is absent — same-origin curl, etc).
- The gateway is exposing a gateway-managed token, i.e. `GATEWAY_AUTH_TOKEN` was *not* supplied via env.

The embedded operator UI uses this on mount when no token sits in `localStorage` — same-origin browsers on the host running the gateway pick up the bearer with no token paste. Anything cross-origin, remote, or behind a reverse proxy hits a `403` and falls back to the manual TokenGate.

The endpoint never reads or trusts `X-Forwarded-For`, so a misconfigured proxy can't trick it into handing the token to a remote browser.

### Single-user vs multi-tenant

The published Docker image ships single-user (`GATEWAY_MULTI_TENANT=false`). Flip the flag to expose tenant + API-key management surfaces and tenant-readable observability mirrors. Switching between runs is non-destructive — existing tenant/key rows stay intact, only the UI surfaces and the auth gates on `/v1/traces` etc. flip.

The README has the at-a-glance comparison; full breakdown (roles, storage notes, when to enable) in [`tenants.md`](tenants.md).

## Recovering a lost admin token

The first-run banner is the easiest path. If it's scrolled out of `docker compose logs`, the token also lives in the bootstrap file on the `hecate-data` volume. The gateway image is distroless (no shell), so use `docker compose cp` to copy the file out:

```bash
docker compose cp hecate:/data/hecate.bootstrap.json - | tar -xO | jq -r .admin_token
```

(`docker compose cp ... -` emits a tar archive, hence the `tar -xO`.)

The bootstrap file (and the SQLite database, see below) persist across container restarts as long as the `hecate-data` volume sticks around — only `docker compose down -v` (or `make reset-docker`) wipes them.

## Resetting state

To wipe the stack back to first-run — removes the `hecate-data` (admin token + SQLite db) and `postgres-data` volumes and regenerates the admin token on the next `docker compose up`:

```bash
make reset-docker
```

The next page load in the browser detects the rejected stale token and re-prompts for the regenerated one — no manual `localStorage` cleanup needed.

For local (non-Docker) development resets, see [`development.md`](development.md#reset-state).

## Storage backends

Hecate keeps the storage model intentionally boring: each subsystem chooses a backend independently, usually `memory`, `sqlite`, or `postgres`.

| Subsystem | Env var | memory | sqlite | postgres |
|---|---|---:|---:|---:|
| Control plane | `GATEWAY_CONTROL_PLANE_BACKEND` | local default | Docker default | yes |
| API key auth | `GATEWAY_AUTH_BACKEND` | local default | Docker default | yes |
| Provider credentials | `GATEWAY_PROVIDER_STORE_BACKEND` | local default | Docker default | yes |
| Pricebook | `GATEWAY_PRICEBOOK_BACKEND` | local default | Docker default | yes |
| Budget / balances | `GATEWAY_BUDGET_BACKEND` | local default | Docker default | yes |
| Usage ledger | `GATEWAY_USAGE_BACKEND` | local default | Docker default | yes |
| Audit events | `GATEWAY_AUDIT_BACKEND` | local default | Docker default | yes |
| Policy rules | `GATEWAY_POLICY_BACKEND` | local default | Docker default | yes |
| Exact cache | `GATEWAY_CACHE_BACKEND` | local default | Docker default | yes |
| Semantic cache | `GATEWAY_SEMANTIC_CACHE_BACKEND` | yes | no | yes |
| Trace snapshots | `GATEWAY_TRACE_STORE_BACKEND` | local default | Docker default | yes |
| Retention history | `GATEWAY_RETENTION_HISTORY_BACKEND` | local default | Docker default | yes |
| Chat sessions | `GATEWAY_CHAT_SESSIONS_BACKEND` | local default | Docker default | yes |
| Tasks | `GATEWAY_TASKS_BACKEND` | local default | Docker default | yes |
| Task queue | `GATEWAY_TASK_QUEUE_BACKEND` | local default | Docker default | yes |

Deployment-specific notes:

- The docker image **defaults to `sqlite`** for every durable subsystem, persisting state at `GATEWAY_SQLITE_PATH` (default `/data/hecate.db` on the `hecate-data` volume). This is why `docker compose up` keeps tenants, keys, pricebook, tasks, and chat sessions across restarts with no extra config.
- The semantic cache has no SQLite backend and stays on `memory` in the docker image. To get persistent semantic search, switch just that subsystem to Postgres with `GATEWAY_SEMANTIC_CACHE_BACKEND=postgres`.
- `POSTGRES_DSN` is required when any subsystem uses `postgres`.
- To make any subsystem ephemeral in docker, override its backend via `.env` or compose env: `GATEWAY_TASKS_BACKEND=memory`, etc.

## Rate limiting

Rate limiting is a per-key token bucket. It is disabled by default so first-run local testing does not surprise users.

| Variable | Default | Notes |
|---|---:|---|
| `GATEWAY_RATE_LIMIT_ENABLED` | `false` | Enables per-key request rate limits. |
| `GATEWAY_RATE_LIMIT_RPM` | `60` | Steady-state refill rate per API key. |
| `GATEWAY_RATE_LIMIT_BURST` | `0` | Optional burst capacity. `0` means "same as RPM". |

Over-limit requests return `429 Too Many Requests` with `code: "rate_limit_exceeded"` and standard `X-RateLimit-*` headers. Admin bearer traffic and anonymous traffic share a single `anonymous` bucket because they do not have tenant key IDs.
