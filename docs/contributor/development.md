# Development

This guide covers the source-build toolchain, local-build path, UI hot reload,
website development, the test surface, and the screenshot tooling. For the
simplest get-it-running flow, see [Start Here](../README.md#start-here) — the
desktop app is the recommended on-ramp for macOS personal use; Docker is the
more predictable path for server use and for Linux/Windows until those desktop
bundles are manually tested.
The Tauri desktop app's local build (`just tauri-dev`) lives in
[`docs-ai/skills/tauri/SKILL.md`](../../docs-ai/skills/tauri/SKILL.md).

## Contents

- [Toolchain](#toolchain)
- [Local build](#local-build)
- [UI hot reload](#ui-hot-reload)
- [Website](#website)
- [Reset state](#reset-state)
- [Testing](#testing)
- [CI workflow](#ci-workflow)
- [Project layout](#project-layout)
- [Capturing documentation screenshots](#capturing-documentation-screenshots)

## Toolchain

Required for the main runtime + embedded UI:

- **Go** — pinned via `go.mod`; `just build` runs `go build`.
- **Bun** — pinned via `ui/package.json` and `website/package.json`
  (`packageManager: "bun@..."`); used for UI/website installs, scripts, tests,
  screenshots, and CI.
- **just** — task runner for local build/test/dev recipes; replaces Make.

Required only for the native desktop app:

- **Rust + Cargo** — installed via `rustup`; needed for `just tauri-dev`, `just tauri-build`, and native smoke tests.

Optional:

- **Docker** — only required for the docker-smoke test job and container workflows; not needed for the local runtime itself.
- **Node.js 20+** — required only by the checked-in iOS and Android native build hooks; the UI and website still use Bun.
- **RTK** — optional local helper used by Hecate Chat's per-chat “compact command output” setting. It is off by default; when the `rtk` command is present in the gateway `PATH`, the UI offers an opt-in hint. Hecate still applies policy validation, env sanitisation, output caps, timeouts, and the OS sandbox wrapper.

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

The generated Xcode and Gradle projects are the exception: their native build
hooks invoke `node` directly, so install Node.js 20 or newer before using the
mobile build recipes.

The `hecate` runtime embeds the React UI via `//go:embed ui/dist`. There's no separate UI deployment.

## Local build

1. Optionally copy env defaults if you want local overrides or secrets:

   ```bash
   cp .env.example .env
   # Edit .env for local overrides, or configure providers later in Connections.
   ```

2. Build `hecate` with the UI bundled in:

   ```bash
   just                    # lists available project recipes
   just ui-install         # installs UI dependencies (bun install)
   just build              # ui-build + go build → ./hecate
   just serve              # run prebuilt ./hecate; sources .env if present
   just serve --reset      # same, but wipe .data/ first
   ```

The gateway and the operator UI are both served from `http://127.0.0.1:8765`. `just serve` stops any earlier `./hecate` process still bound to that port before starting, so re-running it is always safe.

For iterative changes that don't touch the embed boundary, skip the binary build and run from source: `just run` is `go run` with quick defaults; `just dev` is the same but sources `.env` when present so provider keys are available.

## Cairnline-backed Projects

Normal `just dev` runs Hecate with embedded Cairnline as the Projects
coordination authority; no feature flags or alternate recipe are required. Use
`just reset-dev` when you intentionally want a clean Hecate and Cairnline
local state directory.

For focused API confidence, run:

```bash
just test-projects-cairnline
```

That journey creates project coordination through the normal Hecate facade,
launches a Hecate task, and verifies runtime references and context evidence
without configuring Hecate-native portable stores. Backend/runtime changes must
still run the normal race suite.

The standalone Cairnline repository owns MCP-server interoperability tests.
Hecate currently embeds the Go service; it does not expose a selectable
standalone sidecar connector.

## UI hot reload

For live UI iteration, run `just dev` (gateway on `:8765`) and the Vite dev server side by side:

```bash
just ui-dev       # Vite on :5173, proxying API calls to :8765
```

Default addresses:

- gateway + bundled UI (production): `http://127.0.0.1:8765`
- Vite dev server (UI hot reload): `http://127.0.0.1:5173`

The Vite dev server proxies every `/hecate/*`, `/v1/*`, and `/healthz` request to `:8765`, so the UI runs hot while the gateway runs as-is.
`just dev` allows the two default Vite origins (`http://127.0.0.1:5173` and `http://localhost:5173`) through the same-origin guard. If you use a different dev server or port, set `HECATE_ALLOWED_ORIGINS` to that exact origin before starting the gateway.

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
just go-format-check   # Go gofmt -s formatting check
just go-format         # format Go source with gofmt -s
just ui-lint           # UI oxlint checks
just ui-format-check   # UI Oxfmt formatting check
just ui-format         # format UI source with Oxfmt
just ui-test           # UI unit tests (vitest)
just ui-test-e2e       # UI end-to-end tests (Playwright)
just website-lint      # website oxlint checks
just website-format-check # website Oxfmt formatting check
just website-format    # format website source with Oxfmt
just docs-format-check # Markdown and .mdc Oxfmt formatting check
just docs-format       # format tracked Markdown and .mdc docs with Oxfmt
just format-check      # Go + UI + website + docs formatting check
just format            # auto-format Go + UI + website + docs
just website-build     # Astro website check + production build
just ui-coverage       # UI coverage report (vitest --coverage)
just test-docker-smoke # boots the production image and probes /healthz, /v1/models
just test-tauri-smoke  # macOS native app smoke: build .app, probe /healthz, quit
just tauri-ios-build-debug # unsigned Apple Silicon iOS Simulator companion
just tauri-android-build-debug aarch64 # Android arm64 debug APK; requires SDK/NDK/JDK env
just verify            # full gate: docs/env check, Go, Docker, UI, build
just verify-desktop    # desktop-specific Rust/Tauri check
just release vX.Y.Z    # verify, then run the release script
```

The race detector is the strongest correctness check (and the slowest); CI runs it on every push. The Go e2e suite also includes binary-level External Agent approval smokes for SQLite startup reconcile and durable grant persistence; run them with `go test -tags e2e -run 'TestApproval' ./e2e` when touching approval storage or cmd/hecate startup wiring. Gateway e2e helpers auto-set `PROVIDER_<NAME>_PRECONFIGURED=1` for providers described with `PROVIDER_<NAME>_*` vars; when a test posts an explicit model that the fake `/v1/models` endpoint does not advertise, seed it with `PROVIDER_<NAME>_MODELS` instead of reintroducing provider default-model env vars. `test-docker-smoke` requires Docker but doesn't need any other infrastructure — it spins up its own compose project to avoid colliding with a developer's running stack. `verify-desktop` runs the Tauri Rust test suite and requires Rust/Cargo. `test-tauri-smoke` builds only the packaged macOS `.app`, waits for the sidecar gateway to answer `/healthz`, quits Hecate, and confirms the sidecar exits. The native smoke is opt-in because it opens a real GUI window.

Before cutting a public tag, run `just verify` and follow the checklist in [Release](release.md).

TypeScript linting uses Oxc with type-aware checks through `oxlint-tsgolint`.
`just ui-lint`, `just website-lint`, and `just format-check` are part of
`just verify` and CI. `just format` is the local auto-format pass for Go, UI,
website, and docs; use it before pushing when CI reports formatting drift, then
review the resulting diff like any other code change. The shared
`.oxlintrc.json` enables React, accessibility, Vitest, import, TypeScript,
Unicorn, and Oxc rules for both UI surfaces. Markdown and `.mdc` docs use
Oxfmt for formatting; lychee still validates links and fragments.

## CI workflow

GitHub Actions is split by surface so small changes do not wake the whole
project:

| Workflow                  | Trigger                                                                    | Purpose                                                                                                  |
| ------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `test.yml`                | Every PR; pushes to `master` except markdown-only and website-only changes | Main quality gate: Go, UI, e2e, Docker smoke, Tauri Rust tests, and gated desktop bundle validation.     |
| `website.yml`             | Website changes and release-manifest updates                               | Astro check/build and GitHub Pages deploy for [hecate.sh](https://hecate.sh).                            |
| `links.yml`               | PRs and pushes                                                             | Markdown formatting, link, fragment, and Mermaid validation.                                             |
| `maintenance.yml`         | Nightly and manual dispatch                                                | Repeatable maintenance and race-test report, with external link drift kept informational.                |
| `cursor-agent-update.yml` | Weekly and manual dispatch                                                 | Validate official Cursor Agent artifacts and open a human-reviewed two-Dockerfile pin update.            |
| `release.yml`             | `v*` tags and manual dispatch                                              | Goreleaser artifacts, Docker images, signed desktop bundles, updater manifest, website manifest publish. |
| `tauri-build.yml`         | Manual dispatch only                                                       | Explicit desktop bundle rebuild/debug run from the Actions tab.                                          |

The main `Test` workflow starts every PR with a path filter. Go, TypeScript,
Docker, and Tauri Rust jobs run only when their inputs changed, while workflow
edits force the full matrix so CI changes test themselves. The TypeScript job
runs Oxc lint and Oxfmt format checks before build and Vitest, so mechanical
issues fail before the slower unit suite. Its always-present `Required checks`
job fails when any selected job fails or is cancelled and is the stable context
to require in the default-branch ruleset. Even documentation-only PRs run the
lightweight detector and aggregator so that required context is never absent.

The Website workflow runs Oxc lint and Oxfmt format checks before Astro /
TypeScript checks and the production build.

The Cursor Agent updater uses a dedicated GitHub App so its PRs trigger the
same CI as maintainer-authored PRs without enabling the repository-wide Actions
setting that also permits workflow tokens to approve PRs. Before creating,
installing, or storing credentials for that App, create an active branch
[ruleset](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/managing-rulesets-for-a-repository)
for `master` that:

- blocks branch deletion and force-pushes;
- requires a PR, at least one approving review, approval of the latest push,
  and stale-review dismissal after any new push;
- requires strict status checks, including the stable `Required checks` job
  from `test.yml`; and
- gives the updater App no bypass, including no pull-request-only bypass.

Only after those rules are active, install the App on `hecatehq/hecate`, grant
repository **Contents: read/write** and **Pull requests: read/write** with no
other write permissions, store its client ID as the repository variable
`CURSOR_UPDATE_APP_CLIENT_ID`, and store one private key as the repository
secret `CURSOR_UPDATE_APP_PRIVATE_KEY`. The workflow verifies the effective
rule types on `master` before minting a write token, but GitHub does not expose
private bypass actors to its read-only workflow token, so confirming the App is
absent from the ruleset bypass list remains a mandatory setup review. The
workflow requests only those two App permissions, scopes the installation token
to the current repository, checks out and validates with the read-only workflow
token, mints the write token only when a real update exists, pins every action
in that privileged workflow to an immutable commit, and never approves or
merges its generated PR.

Desktop packaging is intentionally gated inside `test.yml`: the
`Tauri desktop bundles` matrix waits for the cheaper Go, TypeScript, e2e,
Docker smoke, and Tauri Rust jobs to pass (or skip by path filter) before
building macOS, Linux, and Windows bundles. PR bundle validation does **not**
upload unsigned artifacts; it only proves the bundles build. It does not
manually launch Linux or Windows desktop artifacts. Release tags still upload
signed/notarized artifacts through `release.yml`.

This shape keeps PR feedback fast for normal code changes and avoids burning
desktop runner minutes when a cheaper test has already failed. If a desktop
packaging issue needs an explicit rerun, use the manual `tauri-build` workflow
from the Actions tab. If a reviewer needs a pre-merge bundle to test-launch,
dispatch `tauri-build.yml` manually from the PR branch.

### Skipping CI for inert changes

Pure markdown commits already skip CI via `paths-ignore: '**/*.md'` in the workflow triggers. For inert changes inside source files (e.g. fixing a typo in a `//` comment, renaming an unexported test helper that no test references), include one of GitHub's [skip-ci markers](https://docs.github.com/en/actions/managing-workflow-runs/skipping-workflow-runs) in the commit message and Actions will skip the run:

```bash
git commit -m "chore: fix typo in agent_loop comment [skip ci]"
```

Recognized markers: `[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`, `[actions skip]`. Use sparingly — when in doubt, let CI run.

## Project layout

Top-level entry points:

```
cmd/hecate/            # main runtime entry point (gateway service, embedded UI, CLI commands)
ui/                     # React app (Vite + Bun); src/ is the source, dist/ is the embed target
website/                # Astro homepage for hecate.sh
tauri/                  # Tauri 2.x desktop sidecar app plus Cloud-only iOS/Android companion
e2e/                    # Go end-to-end tests (build tag: e2e; sub-tags: ollama, docker)
scripts/                # release tooling (release.ts, stamp-version.ts) + documentation tooling (capture-screenshots)
docs/                   # markdown references + screenshots
pkg/types/              # public types shared with external Go code
```

Internal packages (each `internal/<name>/` is a single Go package):

```
agentadapters           # external coding-agent adapter framework (Codex, Claude Code, Cursor Agent, Grok Build)
api                     # HTTP handlers — chat, messages, tasks, settings, telemetry
bootstrap               # first-run secret-key generation and persistence
catalog                 # provider/model discovery and registration
chat                    # chat session storage and replay (memory / sqlite / postgres)
config                  # env-driven config loading
controlplane            # persisted providers, secrets, and policy settings
eventprotocol           # typed agent-event envelope + emitter (see docs/event-protocol-v1.md)
gateway                 # request lifecycle: policy, router, retry/fallback
governor                # policy rules, route gates, and append-only usage events
mcp                     # Hecate-as-MCP-server implementation (the `hecate mcp serve` command)
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
storage                 # shared SQLite/Postgres clients and SQL dialect helpers
taskstate               # task / run / step / artifact / approval persistence
telemetry               # OTel attribute keys, metrics, structured logging
version                 # build-time version metadata
```

## External-agent adapter smoke states

External-agent onboarding depends on vendor CLIs (`codex`, `claude`,
`cursor-agent`, `grok`) and their local authentication. The Codex and Claude
ACP adapters are Go libraries compiled into Hecate. For manual UI smoke
tests and Playwright fixtures, you can force the visual state without
uninstalling anything. These
fixture env vars are intentionally not listed in `.env.example`; keep them in
one-off test commands or `just` recipes instead of normal operator config.

```bash
just dev-no-agent-adapters
```

That starts the gateway with every external adapter connector reported as
missing, which is useful for checking first-run onboarding copy.

For finer control, pass a comma-separated override list:

```bash
just dev-agent-adapters 'codex=ready,claude_code=no_auth,cursor_agent=app_missing,grok_build=ready'
```

The backing env var is `HECATE_AGENT_ADAPTER_DEV_OVERRIDES`. It accepts
`all=...` or per-adapter entries using:

- `missing` / `connector_missing` / `acp_missing` — ACP connector unavailable.
- `app_missing` / `cli_missing` — connector exists, but the underlying agent CLI
  is missing.
- `no_auth` / `auth_required` / `unauthenticated` — local CLI auth missing.
- `ready` / `ok` — startup/auth/ACP session visually ready.
- `billing` or `error` — billing/usage-limit or generic probe failure.

This is visual-only: it changes Connections and Chats readiness UI and fake
probe results, but it does not start adapter runtimes or make a chat send
succeed.

The built-in Go ACP adapters can be checked against authenticated local vendor
CLIs:

```bash
just test-acp-real-embedded
```

That opt-in smoke runs Hecate's built-in adapter, probe, session preparation,
and prompt path against the installed `codex` and `claude` CLIs. It sends a
minimal text turn followed by a privately staged text-file turn on the same
native session. It uses local vendor authentication, may consume provider
quota, and is intentionally outside the normal unit-test ladder. For Claude
Code, it also waits for the provider-owned available-command replacement
snapshot. Discovery is best-effort and an explicit empty catalog is valid; it
uses Claude Code's safe bare/minimal startup boundary, not an unrestricted
workspace/plugin inventory. Hermetic
integration coverage uses strict fake vendor CLIs and includes private
image/file links and environment isolation.

Cursor and Grok expose ACP directly from their vendor CLIs. To verify Hecate's
live probe, session creation, prompt, and prepared-session reuse against both
installed CLIs, run:

```bash
just test-acp-real-direct
```

Pass `cursor_agent` or `grok_build` as the optional argument to select one.
This smoke uses the operator's local CLI authentication and sends a minimal text
turn followed by a privately staged text-file turn on the same native session
for each selected adapter, so it may consume provider quota and remains outside
the default verification ladder.

There is also a narrower discovery-only fixture env var,
`HECATE_AGENT_ADAPTER_DISCOVERY_OVERRIDES`, used by backend tests that only
need catalog states (`all=missing`, `codex=available`). Prefer
`just dev-agent-adapters` for UI work because it keeps catalog and probe visuals
aligned.

## Capturing documentation screenshots

```bash
just screenshots
```

That's the whole command. The target resets dev state, builds the binary if needed, boots the gateway in the background, waits for `/healthz`, walks the operator UI through every documented surface (seeding chat sessions / a task via the public API), snapshots each route, optimizes the PNGs in parallel, and shuts the gateway down. End-to-end: ~13 seconds on a warm machine.

Optional inputs:

- **Ollama on `:11434`** with `ollama pull llama3.1:8b` — optionally seeds one real trace row for the Observability screenshot. The primary Chats screenshots are fixture-backed and remain populated without Ollama. Set `HECATE_SKIP_OLLAMA=1` to skip the live request explicitly.
- **A PNG optimizer on `PATH`** — the script auto-detects in preference order `pngquant` > `oxipng` > `magick`. Without one, captures are 3× larger. Recommended: `brew install pngquant` — lossy palette quantization with Floyd-Steinberg dithering, perceptually indistinguishable from the source on UI screenshots. `HECATE_SKIP_OPTIMIZE=1` skips the optimize pass entirely.

Outputs land in `docs/screenshots/`.
