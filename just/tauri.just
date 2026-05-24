# Tauri native desktop app recipes: deps, version stamping, sidecar build,
# `tauri dev` / `tauri build`, Rust unit tests, packaged-app smoke.
#
# The Tauri app bundles hecate as its sidecar binary. The flow:
#   1. Build the Go binary for the current platform (`just build`).
#   2. Copy it into tauri/src-tauri/binaries/ with the platform-triple suffix
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

# Build hecate with the same version Tauri will package. This
# keeps the native app's embedded gateway from reporting "dev" in /healthz and
# the shell status bar after a release build.
# Build versioned gateway sidecar for Tauri.
tauri-build-sidecars: ui-build _go-cache
	version=$(bun scripts/resolve-tauri-version.ts); \
	goexe=$(go env GOEXE); \
	if [ -z "$version" ]; then \
	  echo "could not resolve Tauri sidecar version" && exit 1; \
	fi; \
	ldflags="-s -w -X github.com/hecatehq/hecate/internal/version.Version=$version"; \
	echo "building Tauri sidecars at version $version"; \
	{{go_env}} go build -trimpath -ldflags "$ldflags" -o "hecate$goexe" ./cmd/hecate

# Build hecate and stage it as a Tauri sidecar. Pass an explicit
# target triple in CI matrix builds; local builds auto-detect the host triple.
# Stage gateway sidecar for Tauri.
tauri-sidecar target="": tauri-build-sidecars
	rust_target="{{target}}"; \
	if [ -z "$rust_target" ]; then \
	  rust_target=$(rustc -vV 2>/dev/null | awk '/^host:/{print $2}'); \
	fi; \
	if [ -z "$rust_target" ]; then \
	  echo "rustc not found — cannot determine host triple" && exit 1; \
	fi; \
	goexe=$(go env GOEXE); \
	src="hecate$goexe"; \
	dest="tauri/src-tauri/binaries/hecate-$rust_target$goexe"; \
	echo "staging sidecar: $dest"; \
	cp "$src" "$dest"

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

# Run the Tauri crate's Rust unit tests. Depends on tauri-sidecar because
# build.rs validates the externalBin path at compile time — without a staged
# sidecar, even `cargo test` fails before any test runs.
# Run Tauri Rust unit tests.
tauri-rust-test: tauri-sidecar
	cd tauri/src-tauri && cargo test --lib

# Build the native app bundle, launch it, wait for the hecate sidecar to answer
# /healthz, quit the app, and verify the sidecar exits. It opens a real desktop
# window, so keep it opt-in rather than part of verify.
# Smoke-test the packaged native app.
test-tauri-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts
