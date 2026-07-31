# React UI recipes: deps, dev server, build, lint, format, tests, coverage.

# Install UI dependencies.
ui-install:
	cd ui && bun install

# Start the Vite UI dev server.
ui-dev: _ui-deps
	cd ui && bun run dev

# `.gitkeep` is preserved by the `preserve-dist-gitkeep` Vite
# plugin (ui/vite.config.ts) — it's emitted as a build asset so
# any caller (just, bare `bun run build`, IDE, CI without just)
# leaves the worktree clean.
# Build the React UI bundle.
ui-build: _ui-deps
	cd ui && bun run build

# Run UI lint checks.
ui-lint: _ui-deps
	cd ui && bun run lint

# Format UI source files with Oxfmt.
ui-format: _ui-deps
	cd ui && bun run format

# Check UI source formatting with Oxfmt.
ui-format-check: _ui-deps
	cd ui && bun run format:check

# Run UI unit tests.
ui-test: _ui-deps
	cd ui && bun run test

# Run UI Playwright e2e tests.
ui-test-e2e: _ui-deps
	cd ui && bun run test:e2e

# The full gate reaches UI tests after race and Docker work. Cap its adaptive
# worker counts and allow a modest local timing budget so host-wide pressure
# cannot turn unchanged assertions into timeouts; direct runs and CI retain
# their stricter normal settings.
_ui-test-verify: _ui-deps
	cd ui && workers="$(bun -e 'const { availableParallelism } = require("node:os"); console.log(Math.max(1, Math.min(2, availableParallelism() - 1)))')" && bun run test -- --maxWorkers="$workers" --testTimeout=20000

_ui-test-e2e-verify: _ui-deps
	cd ui && workers="$(bun -e 'const { availableParallelism } = require("node:os"); console.log(Math.max(1, Math.min(4, Math.floor(availableParallelism() / 2))))')" && bun run test:e2e -- --workers="$workers"

# Generate UI coverage.
ui-coverage: _ui-deps
	cd ui && bun run test:coverage
