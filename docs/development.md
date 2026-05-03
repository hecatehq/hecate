# Development

This guide covers the local-build path (Go + Bun), UI hot reload, the test surface, and the screenshot tooling. For the simplest get-it-running flow, see [Quick Start](../README.md#quick-start) — the desktop app is the recommended on-ramp for personal use; Docker for server use. The Tauri desktop app's local build (`make tauri-dev`) lives in [`ai/skills/tauri/SKILL.md`](../ai/skills/tauri/SKILL.md).

## Contents

- [Toolchain](#toolchain)
- [Local build](#local-build)
- [UI hot reload](#ui-hot-reload)
- [Reset state](#reset-state)
- [Testing](#testing)
- [Project layout](#project-layout)
- [Capturing documentation screenshots](#capturing-documentation-screenshots)

## Toolchain

- **Go** — pinned via `go.mod` (`make build` runs `go build`)
- **Bun** — pinned via `ui/package.json` (`packageManager: "bun@..."`) and used for UI dependency install, script execution, tests, screenshot tooling, and CI
- **Docker** — only required for the docker-smoke test job; not needed for the gateway itself

Do not use npm, pnpm, yarn, Corepack, Volta, or Node-specific workflow setup
for the UI. The committed lockfile is `ui/bun.lock`, the install command is
`bun install`, and all UI scripts run through `bun run ...`.

The gateway binary embeds the React UI via `//go:embed ui/dist`. There's no separate UI deployment.

## Local build

1. Copy env defaults and configure at least one provider:

   ```bash
   cp .env.example .env
   # Edit .env — at minimum set GATEWAY_DEFAULT_MODEL plus a PROVIDER_*_API_KEY
   ```

2. Build the gateway binary with the UI bundled in (gateway binary, single port):

   ```bash
   make ui-install         # installs UI dependencies (bun install)
   make build              # ui-build + go build → ./gateway
   make serve              # run prebuilt ./gateway; sources .env; auto-stops stale :8765
   ```

The gateway and the operator UI are both served from `http://127.0.0.1:8765`. `make serve` stops any earlier `./gateway` process still bound to that port before starting, so re-running it is always safe.

For iterative changes that don't touch the embed boundary, skip the binary build and run from source: `make run` is `go run` with quick defaults; `make dev` is the same but sources `.env` so provider keys are available.

## UI hot reload

For live UI iteration, run `make dev` (gateway on `:8765`) and the Vite dev server side by side:

```bash
make ui-dev       # Vite on :5173, proxying API calls to :8765
```

Default addresses:

- gateway + bundled UI (production): `http://127.0.0.1:8765`
- Vite dev server (UI hot reload): `http://127.0.0.1:5173`

The Vite dev server proxies every `/v1/*`, `/admin/*`, and `/healthz` request to `:8765`, so the UI runs hot while the gateway runs as-is.

## Reset state

```bash
make reset-dev        # local dev: stops :8765, removes .data/
make reset-docker     # docker stack: `docker compose down -v`
```

## Testing

```bash
make test              # go test ./...
make vet               # go vet ./...
make test-race         # go test -race ./...
make coverage          # go test -coverprofile + writes coverage.html
make ui-test           # UI unit tests (vitest)
make ui-test-e2e       # UI end-to-end tests (Playwright)
make test-acp-smoke    # ACP stdio bridge smoke against fake local upstream
make ui-coverage       # UI coverage report (vitest --coverage)
make test-docker-smoke # boots the production image and probes /healthz, /v1/models
make test-tauri-smoke  # macOS native app smoke: build .app, probe /healthz, quit
make verify-alpha      # public-alpha gate: docs/env check, Go, Docker, UI, build
```

The race detector is the strongest correctness check (and the slowest); CI runs it on every push. `test-acp-smoke` starts a fake OpenAI-compatible upstream, the real gateway, and the real `cmd/hecate-acp` stdio bridge, then verifies model discovery, same-task continuation, SSE updates, and editor approval round-trip behavior. `test-docker-smoke` requires Docker but doesn't need any other infrastructure — it spins up its own compose project to avoid colliding with a developer's running stack. `test-tauri-smoke` builds only the packaged macOS `.app`, waits for the sidecar gateway to answer `/healthz`, quits Hecate, and confirms the sidecar exits; it is opt-in because it opens a real GUI window.

Before cutting a public alpha tag, run `make verify-alpha` and follow the checklist in [Release](release.md).

### Skipping CI for inert changes

Pure markdown commits already skip CI via `paths-ignore: '**/*.md'` in the workflow triggers. For inert changes inside source files (e.g. fixing a typo in a `//` comment, renaming an unexported test helper that no test references), include one of GitHub's [skip-ci markers](https://docs.github.com/en/actions/managing-workflow-runs/skipping-workflow-runs) in the commit message and Actions will skip the run:

```bash
git commit -m "chore: fix typo in agent_loop comment [skip ci]"
```

Recognized markers: `[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`, `[actions skip]`. Use sparingly — when in doubt, let CI run.

## Project layout

Top-level entry points:

```
cmd/gateway/            # gateway entry point (CLI flags + bootstrap wiring)
cmd/hecate-acp/         # ACP stdio bridge for editor agent panels
ui/                     # React app (Vite + Bun); src/ is the source, dist/ is the embed target
tauri/                  # native desktop app (Tauri 2.x); wraps `gateway` as a sidecar
e2e/                    # Go end-to-end tests (build tag: e2e; sub-tags: ollama, docker)
scripts/                # release tooling (release.ts, stamp-version.ts) + documentation tooling (capture-screenshots)
docs/                   # markdown references + screenshots
pkg/types/              # public types shared with external Go code
```

Internal packages (each `internal/<name>/` is a single Go package):

```
api                     # HTTP handlers — chat, messages, tasks, admin, control-plane, telemetry
billing                 # pricebook + cost calculation
cache                   # exact response cache (memory / sqlite)
catalog                 # provider/model discovery and registration
chatstate               # chat session storage (memory / sqlite)
config                  # env-driven config loading
controlplane            # persisted providers, pricebook CRUD
gateway                 # request lifecycle: policy, cache, router, retry/fallback
governor                # budget enforcement, rate limiting, policy rules
models                  # model identity + canonical-name resolution
orchestrator            # task runtime: queue, runner, executors, sandbox boundary
policy                  # declarative deny / rewrite policy rules
profiler                # internal trace recorder + OTel SDK adapter
providers               # provider adapters (OpenAI-compat + Anthropic Messages)
ratelimit               # token bucket for HTTP throttling
retention               # retention worker + history store
router                  # provider/model routing engine (rules, failover)
sandbox                 # sandbox-policy types used by orchestrator
secrets                 # AES-GCM provider-credential encryption
storage                 # shared SQLite client connector
taskstate               # task / run / step / artifact / approval persistence
telemetry               # OTel attribute keys, metrics, structured logging
version                 # build-time version metadata
```

## Capturing documentation screenshots

```bash
make screenshots
```

That's the whole command. The target resets dev state, builds the binary if needed, boots the gateway in the background, waits for `/healthz`, walks the operator UI through every documented surface (seeding chat sessions / a task via the public API), snapshots each route, optimizes the PNGs in parallel, and shuts the gateway down. End-to-end: ~13 seconds on a warm machine.

Optional inputs:

- **Ollama on `:11434`** with `ollama pull llama3.1:8b` — seeds a real chat turn so the README hero shows model output instead of an empty session. The capture continues without it; set `HECATE_SKIP_OLLAMA=1` to skip explicitly.
- **A PNG optimizer on `PATH`** — the script auto-detects in preference order `pngquant` > `oxipng` > `magick`. Without one, captures are 3× larger. Recommended: `brew install pngquant` — lossy palette quantization with Floyd-Steinberg dithering, perceptually indistinguishable from the source on UI screenshots. `HECATE_SKIP_OPTIMIZE=1` skips the optimize pass entirely.

Outputs land in `docs/screenshots/`.
