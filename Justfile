set shell := ["sh", "-cu"]

# ─── Project-wide variables ─────────────────────────────────────────────────
gocache := ".gocache"
ui_canary := "ui/node_modules/@vitejs/plugin-react"
website_canary := "website/node_modules/astro"

# Shared Go environment prefix — every Go invocation uses the workspace-local
# build cache so CI and dev machines stay deterministic. Defined once here
# and referenced as `{{go_env}}` in the Go and Tauri modules.
go_env := 'GOCACHE="$PWD/' + gocache + '"'

# ─── Module imports ─────────────────────────────────────────────────────────
# Recipes are grouped by domain. Module files keep their recipe names
# unchanged, so `just <recipe>` invocations from CI and shell history keep
# working byte-for-byte. Run `just --list` to see the full surface.

import 'just/_helpers.just'
import 'just/go.just'
import 'just/ui.just'
import 'just/website.just'
import 'just/docs.just'
import 'just/maintenance.just'
import 'just/smoke.just'
import 'just/tauri.just'
import 'just/release.just'

# ─── Top-level umbrellas ────────────────────────────────────────────────────

# List available recipes.
default:
	just --list

# Install all third-party dependencies (UI, website, Tauri).
deps: ui-install website-install tauri-install

# Format all repo-managed source and docs surfaces.
format: go-format ui-format website-format docs-format

# Check all repo-managed source and docs formatting.
format-check: go-format-check ui-format-check website-format-check docs-format-check

# Project verification gate. It intentionally runs only non-destructive
# checks, but it is not cheap: Docker and UI e2e can take a bit.
# Run the full project verification gate.
verify: docs-check go-format-check ui-format-check website-format-check test vet test-race test-docker-smoke ui-lint website-lint ui-test ui-test-e2e build

# Run desktop-specific verification that requires Rust/Cargo and Tauri deps.
verify-desktop: tauri-rust-test

# Remove all build artifacts: Go cache, UI bundle contents, Tauri target,
# staged sidecars. Preserves `.gitkeep` markers so the worktree stays clean.
# Does not touch the top-level `hecate` binary — re-running `just build`
# overwrites it.
# Remove build artifacts.
clean:
	rm -rf {{gocache}}
	find ui/dist -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
	rm -rf tauri/src-tauri/target
	find tauri/src-tauri/binaries -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true
	echo "Cleaned: .gocache, ui/dist contents, tauri/src-tauri/target, tauri/src-tauri/binaries"

# Print toolchain versions and known-prerequisite status. Useful when
# `just verify` fails for unclear reasons or when onboarding a new machine.
# Print toolchain versions for diagnosis.
doctor:
	@echo "go:     $(go version 2>/dev/null || echo 'NOT INSTALLED — required')"
	@echo "bun:    $(bun --version 2>/dev/null || echo 'NOT INSTALLED — required')"
	@echo "rustc:  $(rustc --version 2>/dev/null || echo 'NOT INSTALLED — required for Tauri')"
	@echo "just:   $(just --version 2>/dev/null || echo 'NOT INSTALLED')"
	@echo "docker: $(docker --version 2>/dev/null || echo 'not installed (optional; needed for test-docker-smoke)')"
	@echo "lychee: $(lychee --version 2>/dev/null || echo 'not installed (optional; needed for check-links)')"
