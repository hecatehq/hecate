SHELL := /bin/sh

GOCACHE_DIR := $(CURDIR)/.gocache

.PHONY: test test-race vet coverage ui-coverage build build-acp run serve dev stop ui-install ui-dev ui-build ui-test ui-test-e2e test-acp-smoke test-docker-smoke docs-env-check check-links verify-alpha reset-dev reset-docker screenshots tauri-install tauri-version tauri-sidecar tauri-dev tauri-build tauri-build-app test-tauri-smoke test-tauri-acp-smoke

# build produces a single self-contained `hecate` binary with the UI bundle
# embedded. The UI is built first so //go:embed picks up the real assets;
# without this step the binary still runs but serves a "UI not built"
# fallback page instead of the React app.
build: ui-build
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go build -o hecate ./cmd/hecate

build-acp:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go build -o hecate-acp ./cmd/hecate-acp

test:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go test ./...

test-race:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go test -race ./...

vet:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go vet ./...

coverage:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go test -coverprofile=coverage.out ./...
	GOCACHE="$(GOCACHE_DIR)" go tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html for line-level coverage."

ui-coverage:
	test -d ui/node_modules/@vitejs/plugin-react || (echo "UI dependencies are out of date. Run 'make ui-install' first." && exit 1)
	cd ui && bun run test:coverage

# stop frees :8765 by killing whatever is listening there. Useful before
# `docker run -p 8765:8765 …` (a stale `make dev` / `make run` / `./hecate`
# from a previous shell will otherwise produce "address already in use") or
# any time the operator wants to make sure the dev gateway is down.
stop:
	@pid=$$(lsof -ti:8765 2>/dev/null); \
	if [ -z "$$pid" ]; then \
	  echo ":8765 already free"; \
	else \
	  echo "stopping gateway on :8765 (pid $$pid)"; \
	  kill $$pid; \
	  sleep 0.3; \
	fi

run: stop
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go run ./cmd/hecate

# serve runs the pre-built ./hecate binary. The `stop` prerequisite frees
# :8765 if a stale process is still listening, so a forgotten Ctrl-C never
# blocks a restart. It also sources .env so providers configured there are
# available, matching the `make dev` workflow.
serve: stop
	@test -x ./hecate || (echo "hecate binary not found — run 'make build' first." && exit 1)
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	./hecate

dev: stop
	mkdir -p "$(GOCACHE_DIR)"
	set -a; \
	. ./.env; \
	set +a; \
	GOCACHE="$(GOCACHE_DIR)" go run ./cmd/hecate

ui-install:
	cd ui && bun install

ui-dev:
	test -d ui/node_modules/@vitejs/plugin-react || (echo "UI dependencies are out of date. Run 'make ui-install' first." && exit 1)
	cd ui && bun run dev

ui-build:
	test -d ui/node_modules/@vitejs/plugin-react || (echo "UI dependencies are out of date. Run 'make ui-install' first." && exit 1)
	cd ui && bun run build
	# Vite empties ui/dist before building, which deletes the tracked
	# .gitkeep placeholder. Restore it exactly as git has it so the next
	# `git status` stays clean. Fall back to touch outside a git repo
	# (CI build steps, fresh checkouts before the first commit).
	@git restore ui/dist/.gitkeep 2>/dev/null || touch ui/dist/.gitkeep

ui-test:
	test -d ui/node_modules/@vitejs/plugin-react || (echo "UI dependencies are out of date. Run 'make ui-install' first." && exit 1)
	cd ui && bun run test

ui-test-e2e:
	test -d ui/node_modules/@vitejs/plugin-react || (echo "UI dependencies are out of date. Run 'make ui-install' first." && exit 1)
	cd ui && bun run test:e2e

test-acp-smoke:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" bun e2e/acp-smoke.ts

# test-docker-smoke spins up `docker compose` with the production image
# and verifies /healthz, /v1/models auth, and the bootstrap volume round
# trip. Runs against a separate compose project name so it can't collide
# with a developer's already-running `docker compose up`. Requires Docker.
test-docker-smoke:
	mkdir -p "$(GOCACHE_DIR)"
	GOCACHE="$(GOCACHE_DIR)" go test -tags 'e2e docker' -count=1 -timeout 5m ./e2e/...

# docs-env-check catches alpha-risk documentation drift: removed env bootstrap
# surfaces sneaking back into docs, and release docs going missing.
docs-env-check:
	@test -f docs/release.md
	@test -f docs/known-limitations.md
	@! rg -n 'GATEWAY_POLICY_RULES_JSON|GATEWAY_PRICEBOOK_JSON|GATEWAY_PROVIDERS|PROVIDER_[A-Z0-9_]+_(PROTOCOL|API_VERSION|TIMEOUT)' README.md docs .env.example internal/config e2e .github

# check-links runs lychee against all markdown and .mdc files to catch broken
# relative links and dead external URLs. Mirrors the CI Links workflow.
# Install lychee via: brew install lychee  OR  cargo install lychee
check-links:
	@command -v lychee >/dev/null 2>&1 || { \
	  echo "lychee not installed."; \
	  echo "  macOS:  brew install lychee"; \
	  echo "  Cargo:  cargo install lychee"; \
	  exit 1; \
	}
	lychee --no-progress --include-fragments \
	  --exclude-path .gomodcache \
	  --exclude-path ui/node_modules \
	  --exclude-path .claude/skills \
	  './**/*.md' './**/*.mdc'

# verify-alpha is the public-alpha release gate. It intentionally runs only
# non-destructive checks, but it is not cheap: Docker and UI e2e can take a bit.
verify-alpha: docs-env-check test vet test-race test-acp-smoke test-docker-smoke ui-test ui-test-e2e build

# reset-dev wipes local dev state back to first-run: stops the gateway on
# :8765 and deletes the data directory (which holds the AES-GCM key and
# any sqlite databases) so the next start regenerates fresh state.
reset-dev:
	@pid=$$(lsof -ti:8765 2>/dev/null); \
	if [ -n "$$pid" ]; then \
	  echo "stopping existing gateway on :8765 (pid $$pid)"; \
	  kill $$pid; \
	  sleep 0.3; \
	fi
	rm -rf .data
	@echo "Local dev state reset."

# screenshots is the one-shot end-to-end capture workflow:
# reset → build → start hecate in the background → wait
# for /healthz → run the bun capture script → stop gateway. Everything
# is reset to a clean state on entry and torn down on exit, so two
# successive `make screenshots` calls always produce identical files.
#
# ollama on :11434 with `llama3.1:8b` pulled produces the realistic
# chat turn shown in the README hero; HECATE_SKIP_OLLAMA=1 lets you
# run the workflow without it (chat session will be empty).
screenshots:
	@test -d ui/node_modules/@playwright/test || (echo "UI dependencies missing. Run 'make ui-install' first." && exit 1)
	@pid=$$(lsof -ti:8765 2>/dev/null); [ -n "$$pid" ] && (echo "stopping existing :8765 (pid $$pid)"; kill $$pid; sleep 0.3) || true
	@$(MAKE) --no-print-directory reset-dev > /dev/null
	@$(MAKE) --no-print-directory build
	@mkdir -p .data
	@echo "starting gateway in background…"
	@./hecate > .data/screenshots-gateway.log 2>&1 & echo $$! > .data/screenshots-gateway.pid
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
	  curl -sf http://127.0.0.1:8765/healthz > /dev/null && break; \
	  sleep 0.3; \
	done
	@cd ui && bun run capture-screenshots; \
	  status=$$?; \
	  cd ..; \
	  kill $$(cat .data/screenshots-gateway.pid 2>/dev/null) 2>/dev/null || true; \
	  rm -f .data/screenshots-gateway.pid; \
	  echo "gateway stopped — screenshots are in docs/screenshots/"; \
	  exit $$status

# reset-docker wipes the docker compose stack: stops + removes containers
# and the hecate-data named volume so the next 'docker compose up'
# starts from scratch.
reset-docker:
	docker compose down -v --remove-orphans
	@echo "Docker stack reset."

# ---------------------------------------------------------------------------
# Tauri native desktop app
# ---------------------------------------------------------------------------
#
# The Tauri app bundles hecate and hecate-acp as sidecar binaries. The workflow:
#   1. Build the Go binaries for the current platform (make build build-acp).
#   2. Copy them into tauri/src-tauri/binaries/ with the platform-triple suffix
#      that Tauri's bundler expects (e.g. hecate-aarch64-apple-darwin).
#   3. Install Tauri JS dependencies (bun install inside tauri/).
#   4. tauri dev / tauri build handles the Rust compile + bundle.
#
# Prerequisites (one-time):
#   cargo install tauri-cli --version "^2"   # Tauri CLI
#   rustup target add aarch64-apple-darwin   # macOS arm64 (if on Intel)
#   # Linux: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev ...
#   # Windows: VS Build Tools (C++ workload) — see Tauri docs

# Detect the Rust target triple for the current host so the sidecar binary
# gets the correct suffix that Tauri's bundler expects.
RUST_TARGET := $(shell rustc -vV 2>/dev/null | awk '/^host:/{print $$2}')

# tauri-install: install JS deps (includes @tauri-apps/cli; invoked via bunx tauri).
tauri-install:
	cd tauri && bun install

# tauri-version: stamp Cargo.toml, package.json, and tauri.conf.json with the
# current release version. Resolution order: TAURI_VERSION env var → latest
# git tag → existing Cargo.toml value (dev/untagged builds).
# Called automatically by tauri-build; run manually when cutting a release.
tauri-version: tauri-install
	bun scripts/stamp-version.ts

# tauri-sidecar: build hecate + hecate-acp and stage them as Tauri sidecars.
# Called automatically by tauri-dev and tauri-build so you rarely need it
# directly. On Windows `go build -o hecate` produces hecate.exe, and the
# bundler wants hecate-{triple}.exe — handle both source and dest names.
tauri-sidecar: build build-acp
	@if [ -z "$(RUST_TARGET)" ]; then \
	  echo "rustc not found — cannot determine host triple" && exit 1; \
	fi
	@goexe=$$(go env GOEXE); \
	for name in hecate hecate-acp; do \
	  src="$$name$$goexe"; \
	  dest="tauri/src-tauri/binaries/$$name-$(RUST_TARGET)$$goexe"; \
	  echo "staging sidecar: $$dest"; \
	  cp "$$src" "$$dest"; \
	done

# tauri-dev: hot-reload development mode. Launches the Tauri window backed by
# a fresh hecate sidecar build. The hecate binary is rebuilt first so the
# sidecar is up to date; UI changes require a full `make tauri-sidecar` since
# the gateway embeds the UI bundle at build time.
tauri-dev: tauri-sidecar tauri-install
	cd tauri && bunx tauri dev

# tauri-build: produce a signed (or unsigned) distributable bundle for the
# current platform. Outputs land in tauri/src-tauri/target/release/bundle/.
# To cross-compile (e.g. universal macOS), set TAURI_TARGET:
#   make tauri-build TAURI_TARGET=universal-apple-darwin
tauri-build: tauri-sidecar tauri-version
	@if [ -n "$(TAURI_TARGET)" ]; then \
	  cd tauri && bunx tauri build --target $(TAURI_TARGET); \
	else \
	  cd tauri && bunx tauri build; \
	fi

# tauri-build-app produces only the platform app bundle, not installers
# (.dmg/.msi/.deb/.AppImage). It is the fast path for local smoke tests:
# enough to validate sidecar launch and webview navigation without paying
# the slower and flakier installer packaging cost.
tauri-build-app: tauri-sidecar tauri-version
	@if [ -n "$(TAURI_TARGET)" ]; then \
	  cd tauri && bunx tauri build --target $(TAURI_TARGET) --bundles app; \
	else \
	  cd tauri && bunx tauri build --bundles app; \
	fi

# test-tauri-smoke builds the native app bundle, launches it, waits for the
# hecate sidecar to answer /healthz, quits the app, and verifies the sidecar
# exits. It opens a real desktop window, so keep it opt-in rather than part of
# verify-alpha.
test-tauri-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts

# test-tauri-acp-smoke extends the native app smoke by launching the bundled
# hecate-acp sidecar without HECATE_GATEWAY_URL and verifying it discovers the
# native app's dynamic gateway URL through hecate.runtime.json.
test-tauri-acp-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts --acp
