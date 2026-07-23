# Tauri native app recipes: deps, desktop sidecar/build lifecycle, mobile
# companion builds, Rust unit tests, and packaged-app smoke.
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
	cd tauri && bun install --frozen-lockfile

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

# Stage a minimal executable sidecar placeholder for Cargo-only Tauri tests.
# Tauri's build script validates that the externalBin file exists at compile
# time, but Rust unit tests do not execute the gateway. Keeping this separate
# avoids making `tauri-rust-test` rebuild the embedded UI and Go sidecar.
# Stage placeholder sidecar for Tauri Rust tests.
tauri-test-sidecar target="":
	rust_target="{{target}}"; \
	if [ -z "$rust_target" ]; then \
	  rust_target=$(rustc -vV 2>/dev/null | awk '/^host:/{print $2}'); \
	fi; \
	if [ -z "$rust_target" ]; then \
	  echo "rustc not found — cannot determine host triple" && exit 1; \
	fi; \
	case "$rust_target" in \
	  *windows*) suffix=".exe" ;; \
	  *) suffix="" ;; \
	esac; \
	dest="tauri/src-tauri/binaries/hecate-$rust_target$suffix"; \
	echo "staging test sidecar placeholder: $dest"; \
	mkdir -p "$(dirname "$dest")"; \
	printf '#!/bin/sh\nexit 0\n' > "$dest"; \
	chmod +x "$dest"

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

# Run the Tauri crate's Rust tests. Depends on a lightweight placeholder
# sidecar because build.rs validates the externalBin path at compile time —
# without a staged sidecar, even `cargo test` fails before any test runs.
# Run Tauri Rust unit tests.
tauri-rust-test: tauri-test-sidecar
	cd tauri && bun run test:mobile-shell
	cd tauri/src-tauri && cargo test

# Build the Cloud-only companion for an Apple Silicon iOS Simulator. The
# generated Xcode project is checked in; this does not require a signing team.
# Build the unsigned iOS Simulator companion.
tauri-ios-build-debug: tauri-install
	rm -rf tauri/src-tauri/gen/apple/build/hecate-app_iOS.xcarchive
	rm -rf tauri/src-tauri/gen/apple/build/arm64-sim
	cd tauri && bun run tauri ios build --debug --target aarch64-sim --no-sign --ci

# Build a development-signed iOS IPA that can be installed on a registered
# physical device without using the App Store export flow.
tauri-ios-build-device-debug: tauri-install
	rm -rf tauri/src-tauri/gen/apple/build/hecate-app_iOS.xcarchive
	rm -rf tauri/src-tauri/gen/apple/build/arm64
	cd tauri && bun run tauri ios build --debug --target aarch64 --export-method debugging --ci

# Build a signed iOS IPA for TestFlight/App Store Connect. Xcode uses the
# account configured on this Mac; APPLE_DEVELOPMENT_TEAM can override the
# checked-in project team when a maintainer signs with another account.
# Build the signed iOS companion IPA.
tauri-ios-build-release: tauri-install tauri-version
	: "${APPLE_DEVELOPMENT_TEAM:?set APPLE_DEVELOPMENT_TEAM to the Apple Developer team ID}"
	rm -rf tauri/src-tauri/gen/apple/build/hecate-app_iOS.xcarchive
	rm -rf tauri/src-tauri/gen/apple/build/arm64
	cd tauri && bun run tauri ios build --target aarch64 --export-method app-store-connect --ci

# Build a debug Android APK for one Rust target. JAVA_HOME must point to a JDK
# supported by Gradle (Java 21 is the maintained local/CI choice), while
# ANDROID_HOME and NDK_HOME must point to the installed SDK and NDK.
# Build the Android companion debug APK.
tauri-android-build-debug target="aarch64": tauri-install
	: "${JAVA_HOME:?set JAVA_HOME to a supported JDK such as Java 21}"; \
	: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK}"; \
	: "${NDK_HOME:?set NDK_HOME to the Android NDK}"; \
	cd tauri && bun run tauri android build --debug --target "{{target}}" --apk --ci

# Build a signed universal Android App Bundle for Google Play. The upload key
# lives in the ignored gen/android/keystore.properties file; see the checked-in
# example and docs/operator/mobile-app.md.
# Build the signed Android companion AAB.
tauri-android-build-release: tauri-install tauri-version
	: "${JAVA_HOME:?set JAVA_HOME to a supported JDK such as Java 21}"; \
	: "${ANDROID_HOME:?set ANDROID_HOME to the Android SDK}"; \
	: "${NDK_HOME:?set NDK_HOME to the Android NDK}"; \
	test -f tauri/src-tauri/gen/android/keystore.properties || { \
	  echo "copy keystore.properties.example to keystore.properties and configure the upload key" >&2; \
	  exit 1; \
	}; \
	cd tauri && bun run tauri android build --aab --ci

# Build the native app bundle, launch it, wait for the hecate sidecar to answer
# /healthz, quit the app, and verify the sidecar exits. It opens a real desktop
# window, so keep it opt-in rather than part of verify.
# Smoke-test the packaged native app.
test-tauri-smoke: tauri-build-app
	bun scripts/tauri-smoke.ts
