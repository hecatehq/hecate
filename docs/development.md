# Development

This guide covers the source-build toolchain, local-build path, UI hot reload,
website development, the test surface, and the screenshot tooling. For the
simplest get-it-running flow, see [Quick Start](../README.md#quick-start) — the
desktop app is the recommended on-ramp for personal use; Docker for server use.
The Tauri desktop app's local build (`just tauri-dev`) lives in
[`docs-ai/skills/tauri/SKILL.md`](../docs-ai/skills/tauri/SKILL.md).

## Contents

- [Toolchain](#toolchain)
- [Local build](#local-build)
- [UI hot reload](#ui-hot-reload)
- [Website](#website)
- [Reset state](#reset-state)
- [Testing](#testing)
- [Project layout](#project-layout)
- [Capturing documentation screenshots](#capturing-documentation-screenshots)

## Toolchain

Required for the gateway + embedded UI:

- **Go** — pinned via `go.mod`; `just build` runs `go build`.
- **Bun** — pinned via `ui/package.json` and `website/package.json`
  (`packageManager: "bun@..."`); used for UI/website installs, scripts, tests,
  screenshots, and CI.
- **just** — task runner for local build/test/dev recipes; replaces Make.

Required only for the native desktop app:

- **Rust + Cargo** — installed via `rustup`; needed for `just tauri-dev`, `just tauri-build`, and native smoke tests.

Optional:

- **Docker** — only required for the docker-smoke test job and container workflows; not needed for the gateway itself.

Install examples:

```bash
# macOS with Homebrew
brew install go bun just rustup-init
rustup-init

# Linux with shell installers / package manager
curl -fsSL https://bun.sh/install | bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
# Install Go and just through your distro package manager, asdf, mise, or:
cargo install just

# Verify the tools Hecate expects
go version
bun --version
just --version
cargo --version
```

If `cargo install just` fails because Cargo is not installed yet, install Rust
with `rustup` first, then retry. On macOS, `brew install just` is usually the
fastest path because it does not require compiling `just` locally.

Do not use npm, pnpm, yarn, Corepack, Volta, or Node-specific workflow setup
for the UI or website. The committed lockfiles are `ui/bun.lock` and
`website/bun.lock`, the install command is `bun install`, and all scripts run
through `bun run ...`.

The `hecate` binary embeds the React UI via `//go:embed ui/dist`. There's no separate UI deployment.

## Local build

1. Copy env defaults and configure at least one provider:

   ```bash
   cp .env.example .env
   # Edit .env — at minimum set GATEWAY_DEFAULT_MODEL plus a PROVIDER_*_API_KEY
   ```

2. Build `hecate` with the UI bundled in:

   ```bash
   just                    # lists available project recipes
   just ui-install         # installs UI dependencies (bun install)
   just build              # ui-build + go build → ./hecate
   just serve              # run prebuilt ./hecate; sources .env; auto-stops stale :8765
   just serve --reset      # same, but wipe .data/ first
   ```

The gateway and the operator UI are both served from `http://127.0.0.1:8765`. `just serve` stops any earlier `./hecate` process still bound to that port before starting, so re-running it is always safe.

For iterative changes that don't touch the embed boundary, skip the binary build and run from source: `just run` is `go run` with quick defaults; `just dev` is the same but sources `.env` so provider keys are available.

## UI hot reload

For live UI iteration, run `just dev` (gateway on `:8765`) and the Vite dev server side by side:

```bash
just ui-dev       # Vite on :5173, proxying API calls to :8765
```

Default addresses:

- gateway + bundled UI (production): `http://127.0.0.1:8765`
- Vite dev server (UI hot reload): `http://127.0.0.1:5173`

The Vite dev server proxies every `/hecate/*`, `/v1/*`, and `/healthz` request to `:8765`, so the UI runs hot while the gateway runs as-is.

## Website

The public homepage lives in `website/` and builds with Astro. It is separate
from the embedded operator UI: `website/` publishes [hecate.sh](https://hecate.sh),
while `ui/` is bundled into the `hecate` binary.

```bash
just website-install
just website-dev       # Astro dev server
just website-check     # astro check + TypeScript
just website-build     # production build
```

Website-only pull requests run the dedicated Website workflow (`astro check` +
production build) and do not wake the main Hecate Go/Rust/UI test workflow.
Pushes to `master` deploy `website/dist` to GitHub Pages.

## Reset state

```bash
just reset-dev        # local dev: stops :8765, removes .data/
just reset-docker     # docker stack: `docker compose down -v`
just dev --reset      # reset local state, then start from source
just serve --reset    # reset local state, then start the prebuilt binary
```

## Testing

```bash
just test              # go test ./...
just vet               # go vet ./...
just test-race         # go test -race ./...
just coverage          # go test -coverprofile + writes coverage.html
just ui-test           # UI unit tests (vitest)
just ui-test-e2e       # UI end-to-end tests (Playwright)
just website-build     # Astro website check + production build
just test-acp-smoke    # ACP stdio bridge smoke against fake local upstream
just ui-coverage       # UI coverage report (vitest --coverage)
just test-docker-smoke # boots the production image and probes /healthz, /v1/models
just test-tauri-smoke  # macOS native app smoke: build .app, probe /healthz, quit
just test-tauri-acp-smoke # native app + bundled ACP bridge discovery smoke
just verify            # full gate: docs/env check, Go, Docker, UI, build
just release vX.Y.Z    # verify, then run the release script
```

The race detector is the strongest correctness check (and the slowest); CI runs it on every push. `test-acp-smoke` starts a fake OpenAI-compatible upstream, the real `hecate` gateway, and the real `cmd/hecate-acp` stdio bridge, then verifies model discovery, same-task continuation, SSE updates, and editor approval round-trip behavior. The Go e2e suite also includes binary-level Agent Chat approval smokes for SQLite startup reconcile and durable grant persistence; run them with `go test -tags e2e -run 'TestApproval' ./e2e` when touching approval storage or cmd/hecate startup wiring. `test-docker-smoke` requires Docker but doesn't need any other infrastructure — it spins up its own compose project to avoid colliding with a developer's running stack. `test-tauri-smoke` builds only the packaged macOS `.app`, waits for the sidecar gateway to answer `/healthz`, quits Hecate, and confirms the sidecar exits; `test-tauri-acp-smoke` additionally runs the bundled `hecate-acp` without `HECATE_GATEWAY_URL` and verifies native runtime discovery through `hecate.runtime.json`. Both native smokes are opt-in because they open a real GUI window.

Before cutting a public tag, run `just verify` and follow the checklist in [Release](release.md).

### Skipping CI for inert changes

Pure markdown commits already skip CI via `paths-ignore: '**/*.md'` in the workflow triggers. For inert changes inside source files (e.g. fixing a typo in a `//` comment, renaming an unexported test helper that no test references), include one of GitHub's [skip-ci markers](https://docs.github.com/en/actions/managing-workflow-runs/skipping-workflow-runs) in the commit message and Actions will skip the run:

```bash
git commit -m "chore: fix typo in agent_loop comment [skip ci]"
```

Recognized markers: `[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`, `[actions skip]`. Use sparingly — when in doubt, let CI run.

## Project layout

Top-level entry points:

```
cmd/hecate/            # hecate entry point (gateway, embedded UI, `mcp-server` subcommand)
cmd/hecate-acp/         # ACP stdio bridge for editor agent panels
ui/                     # React app (Vite + Bun); src/ is the source, dist/ is the embed target
website/                # Astro homepage for hecate.sh
tauri/                  # native desktop app (Tauri 2.x); wraps `hecate` as a sidecar
e2e/                    # Go end-to-end tests (build tag: e2e; sub-tags: ollama, docker)
scripts/                # release tooling (release.ts, stamp-version.ts) + documentation tooling (capture-screenshots)
docs/                   # markdown references + screenshots
pkg/types/              # public types shared with external Go code
```

Internal packages (each `internal/<name>/` is a single Go package):

```
acp                     # ACP protocol types + dispatcher used by cmd/hecate-acp
agentadapters           # external coding-agent adapter framework (Codex, Claude Code, Cursor Agent)
agentchat               # agent chat session storage and replay
api                     # HTTP handlers — chat, messages, tasks, settings, telemetry
billing                 # pricebook + cost calculation
bootstrap               # first-run secret-key generation and persistence
catalog                 # provider/model discovery and registration
chatstate               # chat session storage (memory / sqlite)
config                  # env-driven config loading
controlplane            # persisted providers, pricebook CRUD
eventprotocol           # typed agent-event envelope + emitter (see docs/event-protocol-v1.md)
gateway                 # request lifecycle: policy, router, retry/fallback
governor                # budget enforcement, rate limiting, policy rules
mcp                     # Hecate-as-MCP-server implementation (the `hecate mcp-server` subcommand)
models                  # model identity + canonical-name resolution
orchestrator            # task runtime: queue, runner, executors, sandbox boundary
policy                  # declarative deny / rewrite policy rules
profiler                # internal trace recorder + OTel SDK adapter
providers               # provider adapters (OpenAI-compat + Anthropic Messages)
ratelimit               # token bucket for HTTP throttling
requestscope            # request-scoped routing hints carried through the gateway
retention               # retention worker + history store
router                  # provider/model routing engine (rules, failover)
sandbox                 # sandbox-policy types used by orchestrator
secrets                 # AES-GCM provider-credential encryption
storage                 # shared SQLite client connector
taskstate               # task / run / step / artifact / approval persistence
telemetry               # OTel attribute keys, metrics, structured logging
version                 # build-time version metadata
```

## External-agent adapter smoke states

External-agent onboarding depends on local tools (`codex-acp`,
`claude-agent-acp`, `cursor-agent`) that may already be installed on your
machine. For manual UI smoke tests and Playwright fixtures, you can force the
discovery result without uninstalling anything:

```bash
just dev-no-agent-adapters
```

That starts the gateway with every external adapter reported as missing, which
is useful for checking first-run onboarding copy.

For finer control, pass a comma-separated override list:

```bash
just dev-agent-adapters 'claude_code=missing,codex=available,cursor_agent=missing'
```

The backing env var is `GATEWAY_AGENT_ADAPTER_DISCOVERY_OVERRIDES`. It accepts
`all=missing` or per-adapter entries using `missing` / `available`. This is
discovery-only: it changes Settings and Chats readiness UI, but it does not
create fake adapter processes or make a chat send succeed.

## Capturing documentation screenshots

```bash
just screenshots
```

That's the whole command. The target resets dev state, builds the binary if needed, boots the gateway in the background, waits for `/healthz`, walks the operator UI through every documented surface (seeding chat sessions / a task via the public API), snapshots each route, optimizes the PNGs in parallel, and shuts the gateway down. End-to-end: ~13 seconds on a warm machine.

Optional inputs:

- **Ollama on `:11434`** with `ollama pull llama3.1:8b` — optionally seeds one real trace row for the Observability screenshot. The primary Chats screenshots are fixture-backed and remain populated without Ollama. Set `HECATE_SKIP_OLLAMA=1` to skip the live request explicitly.
- **A PNG optimizer on `PATH`** — the script auto-detects in preference order `pngquant` > `oxipng` > `magick`. Without one, captures are 3× larger. Recommended: `brew install pngquant` — lossy palette quantization with Floyd-Steinberg dithering, perceptually indistinguishable from the source on UI screenshots. `HECATE_SKIP_OPTIMIZE=1` skips the optimize pass entirely.

Outputs land in `docs/screenshots/`.
