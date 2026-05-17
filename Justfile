set shell := ["sh", "-cu"]

gocache := ".gocache"
ui_canary := "ui/node_modules/@vitejs/plugin-react"
website_canary := "website/node_modules/astro"

# List available recipes.
default:
	just --list

_go-cache:
	mkdir -p {{gocache}}

_ui-deps:
	test -d {{ui_canary}} || (echo "UI dependencies are out of date. Run 'just ui-install' first." && exit 1)

_website-deps:
	test -d {{website_canary}} || (echo "Website dependencies are out of date. Run 'just website-install' first." && exit 1)

# Build a single self-contained `hecate` binary with the UI bundle embedded.
# The UI is built first so //go:embed picks up the real assets; without this
# step the binary still runs but serves the "UI not built" fallback page.
# Build the embedded-UI runtime binary.
build: ui-build _go-cache
	GOCACHE="$PWD/{{gocache}}" go build -o hecate ./cmd/hecate

# Build the ACP stdio bridge binary.
build-acp: _go-cache
	GOCACHE="$PWD/{{gocache}}" go build -o hecate-acp ./cmd/hecate-acp

# Run all Go unit tests.
test: _go-cache
	GOCACHE="$PWD/{{gocache}}" go test ./...

# Run all Go tests with the race detector.
test-race: _go-cache
	GOCACHE="$PWD/{{gocache}}" go test -race ./...

# Run Go vet across all packages.
vet: _go-cache
	GOCACHE="$PWD/{{gocache}}" go vet ./...

# Generate Go coverage HTML.
coverage: _go-cache
	GOCACHE="$PWD/{{gocache}}" go test -coverprofile=coverage.out ./...
	GOCACHE="$PWD/{{gocache}}" go tool cover -html=coverage.out -o coverage.html
	echo "Open coverage.html for line-level coverage."

# Generate UI coverage.
ui-coverage: _ui-deps
	cd ui && bun run test:coverage

# Free :8765 by killing whatever is listening there. Useful before
# `docker run -p 8765:8765 ...` or any local relaunch where a stale
# `just dev` / `just run` / `./hecate` would otherwise produce
# "address already in use".
# Stop any gateway process listening on :8765.
stop:
	pid=$(lsof -ti:8765 2>/dev/null); \
	if [ -z "$pid" ]; then \
	  echo ":8765 already free"; \
	else \
	  echo "stopping gateway on :8765 (pid $pid)"; \
	  kill $pid; \
	  sleep 0.3; \
	fi

# Run the gateway from source. Optional arg: --reset.
run *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just run [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	GOCACHE="$PWD/{{gocache}}" go run ./cmd/hecate

# Run the pre-built ./hecate binary. The `stop` dependency frees :8765 if a
# stale process is still listening, so a forgotten Ctrl-C never blocks a
# restart. It also sources .env so configured providers are available,
# matching the `just dev` workflow.
# Serve the pre-built runtime binary. Optional arg: --reset.
serve *args:
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just serve [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	test -x ./hecate || (echo "hecate binary not found — run 'just build' first." && exit 1)
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	./hecate

# Run the gateway from source with .env loaded. Optional arg: --reset.
dev *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just dev [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	set -a; \
	. ./.env; \
	set +a; \
	GOCACHE="$PWD/{{gocache}}" go run ./cmd/hecate

# Run the gateway from source with external-agent discovery forced to a known
# state. This is discovery-only: it changes Settings/Chats readiness surfaces
# but does not create fake adapter processes. Example:
#   just dev-agent-adapters 'all=missing'
#   just dev-agent-adapters 'claude_code=missing,codex=available'
# Optional arg after the override: --reset.
dev-agent-adapters overrides *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just dev-agent-adapters '<adapter=missing|available,...>' [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	GATEWAY_AGENT_ADAPTER_DISCOVERY_OVERRIDES="{{overrides}}" GOCACHE="$PWD/{{gocache}}" go run ./cmd/hecate

# Run the gateway with all external-agent adapters shown as not installed.
# Useful for manually testing first-run External Agent onboarding.
# Optional arg: --reset.
dev-no-agent-adapters *args:
	just dev-agent-adapters all=missing {{args}}

# Install UI dependencies.
ui-install:
	cd ui && bun install

# Start the Vite UI dev server.
ui-dev: _ui-deps
	cd ui && bun run dev

# Build the React UI bundle.
# `.gitkeep` is preserved by the `preserve-dist-gitkeep` Vite
# plugin (ui/vite.config.ts) — it's emitted as a build asset so
# any caller (just, bare `bun run build`, IDE, CI without just)
# leaves the worktree clean.
ui-build: _ui-deps
	cd ui && bun run build

# Run UI unit tests.
ui-test: _ui-deps
	cd ui && bun run test

# Run UI Playwright e2e tests.
ui-test-e2e: _ui-deps
	cd ui && bun run test:e2e

# Install website dependencies.
website-install:
	cd website && bun install

# Start the Astro website dev server.
website-dev: _website-deps
	cd website && bun --bun run dev

# Typecheck the Astro website.
website-check: _website-deps
	cd website && bun --bun run check

# Build the Astro website.
website-build: _website-deps
	cd website && bun --bun run build

# Preview the built Astro website.
website-preview: _website-deps
	cd website && bun --bun run preview

# Smoke-test the ACP bridge.
test-acp-smoke: _go-cache
	GOCACHE="$PWD/{{gocache}}" bun e2e/acp-smoke.ts

# Spin up `docker compose` with the production image and verify /healthz,
# /v1/models auth, and the bootstrap volume round trip. Runs against a
# separate compose project name so it cannot collide with a developer's
# already-running `docker compose up`. Requires Docker.
# Docker smoke test for the production compose image.
test-docker-smoke: _go-cache
	GOCACHE="$PWD/{{gocache}}" go test -tags 'e2e docker' -count=1 -timeout 5m ./e2e/...

# Catch alpha-risk documentation drift: removed env bootstrap surfaces
# sneaking back into docs, and release docs going missing.
# Check docs for removed env-bootstrap surfaces.
docs-env-check:
	test -f docs/release.md
	test -f docs/known-limitations.md
	! rg -n 'GATEWAY_POLICY_RULES_JSON|GATEWAY_PRICEBOOK_JSON|GATEWAY_PROVIDERS|PROVIDER_[A-Z0-9_]+_(PROTOCOL|API_VERSION|TIMEOUT)' README.md docs .env.example internal/config e2e .github

# Run lychee against all markdown and .mdc files to catch broken relative
# links and dead external URLs. Mirrors the CI Links workflow.
# Install lychee via: brew install lychee  OR  cargo install lychee
# Check markdown links with lychee.
check-links:
	command -v lychee >/dev/null 2>&1 || { \
	  echo "lychee not installed."; \
	  echo "  macOS:  brew install lychee"; \
	  echo "  Cargo:  cargo install lychee"; \
	  exit 1; \
	}
	lychee --no-progress --include-fragments \
	  --exclude-path .gomodcache \
	  --exclude-path ui/node_modules \
	  --exclude-path website/node_modules \
	  --exclude-path .claude/skills \
	  './**/*.md' './**/*.mdc'

# Project verification gate. It intentionally runs only non-destructive
# checks, but it is not cheap: Docker and UI e2e can take a bit.
# Run the full project verification gate.
verify: docs-env-check test vet test-race test-acp-smoke test-docker-smoke ui-test ui-test-e2e build

# Run verification, then cut a release tag. Optional args pass through to
# scripts/release.ts, for example: just release vX.Y.Z --skip-snapshot.
# Verify and cut a release tag.
release version *args: verify
	bun scripts/release.ts {{version}} {{args}}

# Wipe local dev state back to first-run: stop the gateway on :8765 and delete
# the data directory, which holds the AES-GCM key and any sqlite databases, so
# the next start regenerates fresh state.
# Reset local dev state.
reset-dev:
	pid=$(lsof -ti:8765 2>/dev/null); \
	if [ -n "$pid" ]; then \
	  echo "stopping existing gateway on :8765 (pid $pid)"; \
	  kill $pid; \
	  sleep 0.3; \
	fi
	rm -rf .data
	echo "Local dev state reset."

# One-shot end-to-end screenshot workflow:
# reset -> build -> start hecate in the background -> wait for /healthz ->
# run the Bun capture script -> stop the gateway. Everything is reset on entry
# and torn down on exit, so successive `just screenshots` calls are stable.
#
# Ollama on :11434 with `llama3.1:8b` pulled seeds one realistic trace row
# for the Observability screenshot; HECATE_SKIP_OLLAMA=1 skips that optional
# live request. The primary Chats screenshots are fixture-backed.
# Capture documentation screenshots.
screenshots: _ui-deps
	test -d ui/node_modules/@playwright/test || (echo "Playwright dependencies missing. Run 'just ui-install' first." && exit 1)
	pid=$(lsof -ti:8765 2>/dev/null); [ -n "$pid" ] && (echo "stopping existing :8765 (pid $pid)"; kill $pid; sleep 0.3) || true
	just reset-dev > /dev/null
	just build
	mkdir -p .data
	echo "starting gateway in background…"
	./hecate > .data/screenshots-gateway.log 2>&1 & echo $! > .data/screenshots-gateway.pid
	for i in 1 2 3 4 5 6 7 8 9 10; do \
	  curl -sf http://127.0.0.1:8765/healthz > /dev/null && break; \
	  sleep 0.3; \
	done
	cd ui && bun run capture-screenshots; \
	  status=$?; \
	  cd ..; \
	  kill $(cat .data/screenshots-gateway.pid 2>/dev/null) 2>/dev/null || true; \
	  rm -f .data/screenshots-gateway.pid; \
	  echo "gateway stopped — screenshots are in docs/screenshots/"; \
	  exit $status

# Wipe the docker compose stack: stop + remove containers and the hecate-data
# named volume so the next `docker compose up` starts from scratch.
# Reset the docker compose stack and volume.
reset-docker:
	docker compose down -v --remove-orphans
	echo "Docker stack reset."

# ---------------------------------------------------------------------------
# Tauri native desktop app
# ---------------------------------------------------------------------------
#
# The Tauri app bundles hecate and hecate-acp as sidecar binaries. The flow:
#   1. Build the Go binaries for the current platform (`just build build-acp`).
#   2. Copy them into tauri/src-tauri/binaries/ with the platform-triple suffix
#      Tauri expects, for example hecate-aarch64-apple-darwin.
#   3. Install Tauri JS dependencies (`bun install` inside tauri/).
#   4. `tauri dev` / `tauri build` handles the Rust compile + bundle.
#
# Prerequisites:
#   rustup toolchain install stable
#   cargo install tauri-cli --version "^2"   # optional; recipes use bunx tauri
#   rustup target add aarch64-apple-darwin   # macOS arm64 when cross-building
#   # Linux: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev ...
#   # Windows: VS Build Tools (C++ workload) — see Tauri docs

# Install native app dependencies.
tauri-install:
	cd tauri && bun install

# Stamp Cargo.toml, package.json, and tauri.conf.json with the current release
# version. Resolution order: TAURI_VERSION env var -> latest git tag ->
# existing Cargo.toml value (dev/untagged builds). Called automatically by
# tauri-build; run manually when cutting a release.
# Stamp Tauri version files.
tauri-version: tauri-install
	bun scripts/stamp-version.ts

# Build hecate + hecate-acp with the same version Tauri will package. This
# keeps the native app's embedded gateway from reporting "dev" in /healthz and
# the shell status bar after a release build.
# Build versioned gateway and ACP sidecars for Tauri.
tauri-build-sidecars: ui-build _go-cache
	version=$(bun scripts/resolve-tauri-version.ts); \
	goexe=$(go env GOEXE); \
	if [ -z "$version" ]; then \
	  echo "could not resolve Tauri sidecar version" && exit 1; \
	fi; \
	ldflags="-X github.com/hecate/agent-runtime/internal/version.Version=$version"; \
	echo "building Tauri sidecars at version $version"; \
	GOCACHE="$PWD/{{gocache}}" go build -ldflags "$ldflags" -o "hecate$goexe" ./cmd/hecate; \
	GOCACHE="$PWD/{{gocache}}" go build -ldflags "$ldflags" -o "hecate-acp$goexe" ./cmd/hecate-acp

# Build hecate + hecate-acp and stage them as Tauri sidecars. Pass an explicit
# target triple in CI matrix builds; local builds auto-detect the host triple.
# Stage gateway and ACP sidecars for Tauri.
tauri-sidecar target="": tauri-build-sidecars
	rust_target="{{target}}"; \
	if [ -z "$rust_target" ]; then \
	  rust_target=$(rustc -vV 2>/dev/null | awk '/^host:/{print $2}'); \
	fi; \
	if [ -z "$rust_target" ]; then \
	  echo "rustc not found — cannot determine host triple" && exit 1; \
	fi; \
	goexe=$(go env GOEXE); \
	for name in hecate hecate-acp; do \
	  src="$name$goexe"; \
	  dest="tauri/src-tauri/binaries/$name-$rust_target$goexe"; \
	  echo "staging sidecar: $dest"; \
	  cp "$src" "$dest"; \
	done

# Hot-reload development mode. Launches the Tauri window backed by a fresh
# hecate sidecar build. The hecate binary is rebuilt first so the sidecar is up
# to date; UI changes require a fresh `just tauri-sidecar` because the gateway
# embeds the UI bundle at build time.
# Launch the Tauri app in development mode.
tauri-dev: tauri-sidecar tauri-install
	cd tauri && bunx tauri dev

# Produce a signed (or unsigned) distributable bundle for the current platform.
# Outputs land in tauri/src-tauri/target/release/bundle/.
# To cross-compile, set TAURI_TARGET:
#   TAURI_TARGET=universal-apple-darwin just tauri-build
# Build native app bundles.
tauri-build: tauri-sidecar tauri-version
	if [ -n "${TAURI_TARGET:-}" ]; then \
	  cd tauri && bunx tauri build --target "$TAURI_TARGET"; \
	else \
	  cd tauri && bunx tauri build; \
	fi

# Produce only the platform app bundle, not installers
# (.dmg/.msi/.deb/.AppImage). This is the fast path for local smoke tests:
# enough to validate sidecar launch and webview navigation without paying
# the slower and flakier installer packaging cost.
# Build only the native app bundle.
tauri-build-app: tauri-sidecar tauri-version
	if [ -n "${TAURI_TARGET:-}" ]; then \
	  cd tauri && bunx tauri build --target "$TAURI_TARGET" --bundles app; \
	else \
	  cd tauri && bunx tauri build --bundles app; \
	fi

# Build the native app bundle, launch it, wait for the hecate sidecar to answer
# /healthz, quit the app, and verify the sidecar exits. It opens a real desktop
# window, so keep it opt-in rather than part of verify.
# Smoke-test the packaged native app.
test-tauri-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts

# Extend the native app smoke by launching the bundled hecate-acp sidecar
# without HECATE_GATEWAY_URL and verifying it discovers the native app's
# dynamic gateway URL through hecate.runtime.json.
# Smoke-test native app ACP discovery.
test-tauri-acp-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts --acp
