# Docs recipes: oxfmt for Markdown/MDC, env-drift check, lychee link check,
# end-to-end screenshot capture.

# Format tracked Markdown and MDC docs with Oxfmt.
docs-format: _ui-deps
	git ls-files -z '*.md' '*.mdc' | xargs -0 ./ui/node_modules/.bin/oxfmt --write

# Check tracked Markdown and MDC docs formatting with Oxfmt.
docs-format-check: _ui-deps
	git ls-files -z '*.md' '*.mdc' | xargs -0 ./ui/node_modules/.bin/oxfmt --check

# Catch alpha-risk documentation drift: removed env bootstrap surfaces
# sneaking back into docs, and release docs going missing.
# Check docs for removed env-bootstrap surfaces.
docs-env-check:
	test -f docs/release.md
	test -f docs/known-limitations.md
	! rg -n 'HECATE_POLICY_RULES_JSON|HECATE_PRICEBOOK_JSON|HECATE_PROVIDERS|HECATE_[A-Z0-9_]+_BACKEND|PROVIDER_[A-Z0-9_]+_(PROTOCOL|API_VERSION|TIMEOUT)' README.md docs .env.example internal/config e2e .github

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
