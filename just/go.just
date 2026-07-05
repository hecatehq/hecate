# Go runtime recipes: build, test, vet, format, plus the `run`/`serve`/`dev`
# convenience entry points and local-state lifecycle (stop, reset-dev).

# Build a single self-contained `hecate` binary with the UI bundle embedded.
# The UI is built first so //go:embed picks up the real assets; without this
# step the binary still runs but serves the "UI not built" fallback page.
# Build the embedded-UI runtime binary.
build: ui-build _go-cache
	{{go_env}} go build -o hecate ./cmd/hecate

# Build the runtime binary without rebuilding ui/dist.
build-go: _go-cache
	{{go_env}} go build -o hecate ./cmd/hecate

# Run all Go unit tests.
test: _go-cache
	{{go_env}} go test ./...

# Run all Go tests with the race detector.
test-race: _go-cache
	{{go_env}} go test -race -timeout 10m ./...

# Run Go end-to-end tests that do not require optional services.
test-e2e: _go-cache
	{{go_env}} go test -tags e2e -count=1 -timeout 3m ./e2e/...

# Run Go end-to-end tests that require a local Ollama runtime.
test-e2e-ollama: _go-cache
	{{go_env}} go test -tags 'e2e ollama' -count=1 -run TestOllama -timeout 10m ./e2e/...

# Run Go vet across all packages.
vet: _go-cache
	{{go_env}} go vet ./...

# Format Go source files with gofmt -s.
go-format:
	git ls-files -z '*.go' | xargs -0 gofmt -s -w

# Check Go source formatting with gofmt -s.
go-format-check:
	files="$(git ls-files -z '*.go' | xargs -0 gofmt -s -l)"; \
	if [ -n "$files" ]; then \
	  echo "Go files need gofmt -s:"; \
	  echo "$files"; \
	  exit 1; \
	fi

# Generate Go coverage HTML.
coverage: _go-cache
	{{go_env}} go test -coverprofile=coverage.out ./...
	{{go_env}} go tool cover -html=coverage.out -o coverage.html
	echo "Open coverage.html for line-level coverage."

# Free :8765 by killing whatever is listening there. Useful before
# `docker run -p 8765:8765 ...` or any local relaunch where a stale
# `just dev` / `just run` / `./hecate serve` would otherwise produce
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
	{{go_env}} go run ./cmd/hecate serve

# Run the pre-built ./hecate binary. The `stop` dependency frees :8765 if a
# stale process is still listening, so a forgotten Ctrl-C never blocks a
# restart. It sources .env when present so configured providers are available,
# but clean checkouts can run with defaults.
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
	./hecate serve

# Run the gateway from source. Sources .env when present. Optional arg: --reset.
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
	[ -f ./.env ] && . ./.env; \
	set +a; \
	HECATE_ALLOWED_ORIGINS="${HECATE_ALLOWED_ORIGINS:-http://127.0.0.1:5173,http://localhost:5173}"; \
	export HECATE_ALLOWED_ORIGINS; \
	{{go_env}} go run ./cmd/hecate serve

# Run the gateway from source with embedded Cairnline as the authoritative
# Projects coordination dogfood path. This is intentionally explicit: it arms
# replacement mode for local verification without changing normal `just dev`.
# Optional arg: --reset.
# Run gateway with Cairnline-backed Projects dogfood enabled.
dev-cairnline-projects *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just dev-cairnline-projects [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	HECATE_ALLOWED_ORIGINS="${HECATE_ALLOWED_ORIGINS:-http://127.0.0.1:5173,http://localhost:5173}"; \
	export HECATE_ALLOWED_ORIGINS; \
	HECATE_PROJECTS_COORDINATION_BACKEND=cairnline \
	HECATE_PROJECTS_CAIRNLINE_CONNECTOR=embedded \
	HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=embedded \
	HECATE_PROJECTS_CAIRNLINE_WRITE_AUTHORITY=all-portable \
	HECATE_PROJECTS_CAIRNLINE_REPLACEMENT_MODE=embedded \
	{{go_env}} go run ./cmd/hecate serve

# Run the gateway from source with standalone Cairnline sidecar reads enabled.
# This is an interoperability/dev surface, not Hecate's embedded replacement
# path. Requires `cairnline` on PATH unless overridden in .env. Optional arg:
# --reset.
# Run gateway with Cairnline sidecar Projects reads enabled.
dev-cairnline-sidecar-projects *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just dev-cairnline-sidecar-projects [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	configured_sidecar_cmd="${HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND:-}"; \
	sidecar_cmd="${configured_sidecar_cmd:-cairnline}"; \
	if ! command -v "$sidecar_cmd" >/dev/null 2>&1; then \
	  if [ -z "$configured_sidecar_cmd" ] && [ -d ../cairnline/cmd/cairnline ]; then \
	    just build-cairnline-sidecar; \
	    sidecar_cmd="$PWD/.data/cairnline/bin/cairnline-dev"; \
	  else \
	    echo "cairnline sidecar command not found: $sidecar_cmd"; \
	    echo "Install Cairnline from https://github.com/hecatehq/cairnline/releases or set HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND."; \
	    exit 1; \
	  fi; \
	fi; \
	if ! "$sidecar_cmd" -version; then \
	  echo "could not read Cairnline sidecar version from: $sidecar_cmd"; \
	  echo "Install Cairnline v0.1.0-alpha.5 or newer from https://github.com/hecatehq/cairnline/releases."; \
	  exit 1; \
	fi; \
	sidecar_db="${HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB:-.data/cairnline/sidecar/projects.db}"; \
	if [ -n "$sidecar_db" ]; then mkdir -p "$(dirname "$sidecar_db")"; fi; \
	HECATE_ALLOWED_ORIGINS="${HECATE_ALLOWED_ORIGINS:-http://127.0.0.1:5173,http://localhost:5173}"; \
	export HECATE_ALLOWED_ORIGINS; \
	HECATE_PROJECTS_COORDINATION_BACKEND=cairnline \
	HECATE_PROJECTS_CAIRNLINE_CONNECTOR=sidecar \
	HECATE_PROJECTS_CAIRNLINE_READ_SOURCE=sidecar \
	HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND="$sidecar_cmd" \
	HECATE_PROJECTS_CAIRNLINE_SIDECAR_DB="$sidecar_db" \
	{{go_env}} go run ./cmd/hecate serve

# Build the sibling Cairnline repository into Hecate's local .data tree for
# sidecar dogfood. This is a contributor convenience for the multi-repo
# workspace; release binaries or HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND still
# remain the explicit operator path.
# Build local Cairnline sidecar binary from ../cairnline.
build-cairnline-sidecar: _go-cache
	if [ ! -d ../cairnline/cmd/cairnline ]; then \
	  echo "sibling Cairnline repo not found at ../cairnline"; \
	  echo "Install Cairnline from https://github.com/hecatehq/cairnline/releases or set HECATE_PROJECTS_CAIRNLINE_SIDECAR_COMMAND."; \
	  exit 1; \
	fi; \
	out="$PWD/.data/cairnline/bin/cairnline-dev"; \
	cache="$PWD/.gocache"; \
	mkdir -p "$(dirname "$out")"; \
	echo "building sibling Cairnline sidecar: $out"; \
	if ! (cd ../cairnline && GOCACHE="$cache" go build -o "$out" ./cmd/cairnline); then \
	  echo "failed to build sibling Cairnline sidecar from ../cairnline"; \
	  exit 1; \
	fi; \
	"$out" -version

# Run the focused API tests that prove Projects can be dogfooded through the
# Hecate-native flow and the embedded Cairnline replacement flow.
# Run focused Projects dogfood tests.
test-projects-dogfood: _go-cache
	{{go_env}} go test ./internal/api -run 'TestProjectJourneyAPI_(DiscoverStartInspectAndHandoff|CairnlineReplacementModeStartsTaskWithRuntimeDefaultsOverlay|CairnlineReplacementModeStartsExternalAgentWithoutAdvancingNativeShadow|CairnlineReplacementCloseoutReadinessUsesRuntimeOverlay)$|TestProjectCoordinationBackendStatus_(DefaultHecateAuthoritative|EnableDogfoodHintsEmbeddedReadSourceFromSidecar|CairnlineAllPortableWriteAuthorityAliasConfigured|CairnlineEmbeddedReplacementModeArmed|EmbeddedReplacementModeReportsCairnlineAuthoritativeAfterVerifiedCutover)$|TestProjectCairnlineMirrorParityAPI_MatchesRepresentativeLiveProjectJourney$'

# Run the focused API tests that exercise standalone Cairnline sidecar reads,
# launch-packet projection, MCP resource access, and opt-in sidecar smoke
# contracts without making sidecar mode Hecate's authoritative Projects backend.
# Run focused Cairnline sidecar interoperability tests.
test-projects-sidecar: _go-cache
	{{go_env}} go test ./internal/api -run 'Sidecar'

# Run the gateway from source with external-agent adapter visuals forced to a
# known state. This is visual-only: it changes discovery/probe readiness
# surfaces but does not create fake adapter processes or make chat sends
# succeed. Example:
#   just dev-agent-adapters 'all=missing'
#   just dev-agent-adapters 'claude_code=no_auth,codex=ready,cursor_agent=app_missing,grok_build=ready'
# Optional arg after the override: --reset.
# Run gateway with overridden agent-adapter readiness states.
dev-agent-adapters overrides *args: _go-cache
	needs_reset=0; \
	for arg in {{args}}; do \
	  case "$arg" in \
	    --reset) needs_reset=1 ;; \
	    *) echo "unknown argument: $arg"; echo "usage: just dev-agent-adapters '<adapter=missing|ready|no_auth|app_missing,...>' [--reset]"; exit 2 ;; \
	  esac; \
	done; \
	if [ "$needs_reset" = "1" ]; then just reset-dev > /dev/null; else just stop; fi
	set -a; \
	[ -f ./.env ] && . ./.env; \
	set +a; \
	HECATE_ALLOWED_ORIGINS="${HECATE_ALLOWED_ORIGINS:-http://127.0.0.1:5173,http://localhost:5173}"; \
	export HECATE_ALLOWED_ORIGINS; \
	HECATE_AGENT_ADAPTER_DEV_OVERRIDES="{{overrides}}" {{go_env}} go run ./cmd/hecate serve

# Run the gateway with all external-agent adapters shown as not installed.
# Useful for manually testing first-run External Agent onboarding.
# Run gateway with all external-agent adapters forced to not-installed.
dev-no-agent-adapters *args:
	just dev-agent-adapters all=missing {{args}}

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
