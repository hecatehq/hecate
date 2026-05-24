# Go runtime recipes: build, test, vet, format, plus the `run`/`serve`/`dev`
# convenience entry points and local-state lifecycle (stop, reset-dev).

# Build a single self-contained `hecate` binary with the UI bundle embedded.
# The UI is built first so //go:embed picks up the real assets; without this
# step the binary still runs but serves the "UI not built" fallback page.
# Build the embedded-UI runtime binary.
build: ui-build _go-cache
	{{go_env}} go build -o hecate ./cmd/hecate

# Run all Go unit tests.
test: _go-cache
	{{go_env}} go test ./...

# Run all Go tests with the race detector.
test-race: _go-cache
	{{go_env}} go test -race ./...

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
	{{go_env}} go run ./cmd/hecate

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
	./hecate

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
	{{go_env}} go run ./cmd/hecate

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
	HECATE_AGENT_ADAPTER_DEV_OVERRIDES="{{overrides}}" {{go_env}} go run ./cmd/hecate

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
