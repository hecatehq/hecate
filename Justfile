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
verify: docs-check release-workflow-check go-format-check ui-format-check website-format-check test vet test-race test-docker-smoke ui-lint website-lint ui-test ui-test-e2e build

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
	@doctor_os="$(uname -s 2>/dev/null || true)"; case "${OS:-}:$doctor_os" in Windows_NT:*|*:MINGW*|*:MSYS*|*:CYGWIN*) doctor_windows=true ;; *) doctor_windows=false ;; esac; gopls_cmd="${HECATE_CODEINTEL_GOPLS_PATH:-}"; if [ -z "$gopls_cmd" ]; then if [ "$doctor_windows" = true ]; then gopls_cmd="$(command -v gopls.exe 2>/dev/null || command -v gopls 2>/dev/null || true)"; else gopls_cmd="$(command -v gopls 2>/dev/null || true)"; fi; fi; if [ -z "$gopls_cmd" ]; then echo "gopls:  not installed (optional; enables Go code intelligence)"; exit 0; fi; if [ "$doctor_windows" = true ]; then case "$gopls_cmd" in /*|[A-Za-z]:[\\/]*) ;; *) echo "gopls:  unavailable (provider path must be absolute) [$gopls_cmd]"; exit 0 ;; esac; case "$gopls_cmd" in *.[Ee][Xx][Ee]) ;; *) echo "gopls:  unavailable (Windows providers must be native .exe files) [$gopls_cmd]"; exit 0 ;; esac; else case "$gopls_cmd" in /*) ;; *) echo "gopls:  unavailable (provider path must be absolute) [$gopls_cmd]"; exit 0 ;; esac; fi; gopls_probe="$gopls_cmd"; if [ "$doctor_windows" = true ] && command -v cygpath >/dev/null 2>&1; then converted="$(cygpath -u "$gopls_cmd" 2>/dev/null || true)"; if [ -n "$converted" ]; then gopls_probe="$converted"; fi; fi; if [ ! -f "$gopls_probe" ] || [ ! -x "$gopls_probe" ]; then echo "gopls:  unavailable (not a regular executable) [$gopls_cmd]"; exit 0; fi; if gopls_version="$("$gopls_probe" version 2>/dev/null)" && [ -n "$gopls_version" ]; then echo "gopls:  $(printf '%s\n' "$gopls_version" | head -1) [$gopls_cmd] (workspace trust checked per query)"; else echo "gopls:  unavailable (version probe failed) [$gopls_cmd]"; fi
	@doctor_os="$(uname -s 2>/dev/null || true)"; case "${OS:-}:$doctor_os" in Windows_NT:*|*:MINGW*|*:MSYS*|*:CYGWIN*) doctor_windows=true ;; *) doctor_windows=false ;; esac; tsc_cmd="${HECATE_CODEINTEL_TSC_PATH:-}"; if [ -z "$tsc_cmd" ]; then if [ "$doctor_windows" = true ]; then tsc_cmd="$(command -v tsc.exe 2>/dev/null || command -v tsc 2>/dev/null || true)"; else tsc_cmd="$(command -v tsc 2>/dev/null || true)"; fi; fi; if [ -z "$tsc_cmd" ]; then echo "tsc:    not installed (optional; TypeScript 7+ enables native LSP)"; exit 0; fi; if [ "$doctor_windows" = true ]; then case "$tsc_cmd" in /*|[A-Za-z]:[\\/]*) ;; *) echo "tsc:    unavailable (provider path must be absolute) [$tsc_cmd]"; exit 0 ;; esac; case "$tsc_cmd" in *.[Ee][Xx][Ee]) ;; *) echo "tsc:    unavailable (Windows providers must be native .exe files) [$tsc_cmd]"; exit 0 ;; esac; else case "$tsc_cmd" in /*) ;; *) echo "tsc:    unavailable (provider path must be absolute) [$tsc_cmd]"; exit 0 ;; esac; fi; tsc_probe="$tsc_cmd"; if [ "$doctor_windows" = true ] && command -v cygpath >/dev/null 2>&1; then converted="$(cygpath -u "$tsc_cmd" 2>/dev/null || true)"; if [ -n "$converted" ]; then tsc_probe="$converted"; fi; fi; if [ ! -f "$tsc_probe" ] || [ ! -x "$tsc_probe" ]; then echo "tsc:    unavailable (not a regular executable) [$tsc_cmd]"; exit 0; fi; if ! tsc_version="$("$tsc_probe" --version 2>/dev/null)" || [ -z "$tsc_version" ]; then echo "tsc:    unavailable (version probe failed) [$tsc_cmd]"; exit 0; fi; tsc_major="${tsc_version#Version }"; tsc_major="${tsc_major%%.*}"; if [ -n "$tsc_major" ] && [ "$tsc_major" -eq "$tsc_major" ] 2>/dev/null && [ "$tsc_major" -ge 7 ]; then echo "tsc:    $tsc_version (native LSP version-compatible; workspace trust checked per query) [$tsc_cmd]"; else echo "tsc:    $tsc_version (incompatible for native LSP; TypeScript 7+ required) [$tsc_cmd]"; fi
	@doctor_os="$(uname -s 2>/dev/null || true)"; case "${OS:-}:$doctor_os" in Windows_NT:*|*:MINGW*|*:MSYS*|*:CYGWIN*) doctor_windows=true ;; *) doctor_windows=false ;; esac; ast_cmd="${HECATE_CODEINTEL_AST_GREP_PATH:-}"; if [ -z "$ast_cmd" ]; then if [ "$doctor_windows" = true ]; then ast_cmd="$(command -v ast-grep.exe 2>/dev/null || command -v ast-grep 2>/dev/null || true)"; else ast_cmd="$(command -v ast-grep 2>/dev/null || true)"; fi; fi; if [ -z "$ast_cmd" ]; then echo "ast:    not installed (optional; enables structural search)"; exit 0; fi; if [ "$doctor_windows" = true ]; then case "$ast_cmd" in /*|[A-Za-z]:[\\/]*) ;; *) echo "ast:    unavailable (provider path must be absolute) [$ast_cmd]"; exit 0 ;; esac; case "$ast_cmd" in *.[Ee][Xx][Ee]) ;; *) echo "ast:    unavailable (Windows providers must be native .exe files) [$ast_cmd]"; exit 0 ;; esac; else case "$ast_cmd" in /*) ;; *) echo "ast:    unavailable (provider path must be absolute) [$ast_cmd]"; exit 0 ;; esac; fi; ast_probe="$ast_cmd"; if [ "$doctor_windows" = true ] && command -v cygpath >/dev/null 2>&1; then converted="$(cygpath -u "$ast_cmd" 2>/dev/null || true)"; if [ -n "$converted" ]; then ast_probe="$converted"; fi; fi; if [ ! -f "$ast_probe" ] || [ ! -x "$ast_probe" ]; then echo "ast:    unavailable (not a regular executable) [$ast_cmd]"; exit 0; fi; if ast_version="$("$ast_probe" --version 2>/dev/null)" && [ -n "$ast_version" ]; then echo "ast:    $ast_version [$ast_cmd] (workspace trust checked per query)"; else echo "ast:    unavailable (version probe failed) [$ast_cmd]"; fi
