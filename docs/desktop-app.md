# Desktop app

Hecate ships a native desktop app (`tauri/`) alongside the binary tarball and
Docker image. It's a thin Tauri 2.x chrome around the same `hecate` binary
used everywhere else: on launch, the Rust layer spawns Hecate as a
companion process on a free loopback port, polls `/healthz`, then navigates a
webview to `http://127.0.0.1:{port}/` where the gateway serves its embedded UI.

The desktop app bundles both `hecate` and `hecate-acp`. ACP clients still
launch `hecate-acp` themselves over stdio, but the gateway writes
`hecate.runtime.json` into the app data directory so the bridge can discover the
current dynamic gateway URL when `HECATE_GATEWAY_URL` is not set.

Code: [`tauri/`](../tauri/) · agent guide: [`docs-ai/skills/tauri/SKILL.md`](../docs-ai/skills/tauri/SKILL.md) · CI: [`.github/workflows/release.yml`](../.github/workflows/release.yml), [`.github/workflows/tauri-build.yml`](../.github/workflows/tauri-build.yml).

## Distribution

Released alongside the rest of the alpha. The release pipeline builds three
matrix legs in parallel and attaches bundles to the GitHub Release entry:

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `Hecate_X.Y.Z_amd64.deb`, `Hecate_X.Y.Z_amd64.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

PR validation: [`tauri-build.yml`](../.github/workflows/tauri-build.yml) runs
the same matrix on PRs touching the desktop pipeline and persists bundles as
14-day workflow artifacts so reviewers can test-launch before merge.

## Current state — `v0.1.0-alpha.29`

What works:

- Sidecar lifecycle (spawn, healthz wait, kill on exit; `pgrep hecate` is
  empty after `cmd+Q`).
- Runtime discovery file for ACP bridges (`hecate.runtime.json`) written by the
  sidecar gateway on successful startup and removed on app exit.
- Same-origin loading of the embedded gateway UI from the sidecar port.
- Native Hecate menu with actions to focus the window, open the gateway log,
  open the data directory, and quit.
- Per-platform writable data dir (`~/Library/Application Support/sh.hecate.app/`,
  `%APPDATA%\sh.hecate.app\`, `~/.local/share/sh.hecate.app/`).
- Sidecar stderr piped to `<data_dir>/gateway.log` (truncated per launch);
  the startup splash shows failures with the log and data-directory paths.
- Startup splash fonts are vendored for offline startup; their OFL license
  texts live next to the font files under `tauri/splash/fonts/`.
- Window size and position persistence across launches.
- Cross-platform CI matrix with PR validation, draft skipping, and run
  cancellation on push.
- macOS bundles signed with a Developer ID Application certificate and
  notarized by Apple on release-workflow runs (any invocation of
  `release.yml` — tag push or manual `workflow_dispatch` — sets
  `inputs.tagName`, satisfying the env gate; the `APPLE_*` /
  `KEYCHAIN_PASSWORD` repo secrets must also be configured); see
  [`macos-signing.md`](macos-signing.md) for the maintainer-side setup
  and rotation playbook. PR validation builds and Windows/Linux bundles
  remain unsigned by design.
- macOS bundle launch-validated end-to-end: build `.app` + `.dmg`, launch
  the app, confirm the hecate sidecar listens on loopback and `/healthz`
  returns `ok`, then quit and confirm both app and hecate processes exit.
- Linux and Windows bundles build green in CI, including Tauri Rust tests and
  sidecar staging. They still need manual launch smoke on real hardware before
  we describe them as fully platform-tested.
- Auto-update is active. Each release emits a signed `latest.json`
  manifest as a GitHub Release asset and publishes the same manifest
  to `https://hecate.sh/releases/alpha/latest.json`, which alpha.28+
  desktop bundles read on launch. When a newer version is published,
  Hecate surfaces "Hecate X.Y.Z is available — Install and Restart"
  with live download progress. Maintainer-side keypair custody and rotation playbook:
  [`desktop-updater-signing.md`](desktop-updater-signing.md).

What doesn't yet:

- No Windows code signing — SmartScreen warns on every install. Real users
  click "More info → Run anyway"; documented in release notes. Authenticode
  + EV cert is roadmap.
- No Homebrew formula or cask yet. A formula would help CLI installation, and
  a cask would help app distribution. macOS now signs+notarizes via
  `APPLE_*` repo secrets; a cask would still be additional polish.
- No tray and no deep links.
- Linux and Windows: build-only. Need an actual launch on each platform
  before claiming they work.

## Roadmap

Three tiers by dependency, not by enthusiasm. Anything in Tier 1 is
"contained code change, do whenever"; Tier 2 needs a decision or a secret
before it ships; Tier 3 is real feature scope that's only worth doing once
the bundle is polished enough to recommend.

### Tier 1 — polish before the next alpha

| Item | Scope | Notes |
|---|---|---|
| **Test the Linux + Windows bundles** | ~30 min per OS | Download from the current alpha release, install the `.deb` / `.AppImage` / `.msi`, configure a provider, send one chat, quit, relaunch, confirm config persists. macOS is done; these two are the remaining platform unknowns. |
| **Homebrew distribution** | Formula/cask decision | Useful for install ergonomics, especially the CLI. Does not remove the need for macOS signing/notarization for the desktop app; treat it as distribution, not trust. |

### Tier 2 — operational gates

| Item | Cost / decision | Notes |
|---|---|---|
| **Windows code signing** | EV cert (~$300+/yr) | Lower priority. Reputation builds over hundreds of installs anyway, so signing a low-volume alpha is mostly about removing the SmartScreen warning, not unlocking distribution. |

### Tier 3 — features

| Item | Scope | Notes |
|---|---|---|
| **Tray / menubar mode** | Multi-day | "Always on, click to focus." Adds tray icon + show/hide window logic, dock-icon hiding on macOS. Worth doing if "background gateway" is a use case. |
| **Deep links (`hecate://...`)** | ~1 day | Open specific runs, configure providers from a link. Real value depends on whether such links would appear anywhere — premature today. |

### Skip / reconsider later

- **CSP design.** Real but blocked on the gateway UI not setting a CSP header
  itself. Revisit when the gateway UI grows a CSP.
- **Crash reporting (Sentry / similar).** Premature for an alpha; the
  `gateway.log` capture covers most diagnostics.
- **Mobile (iOS/Android).** Tauri 2 supports it; we deleted the icon CLI's
  mobile output. Off-roadmap.

## Sandbox executor

The desktop app bundles the `hecate` and `hecate-acp` binaries. Agent tool calls
(`shell_exec`, `git_exec`, `file_write`) spawn a per-call `sh`
subprocess directly from the gateway with env sanitisation, output cap,
and wall-clock timeout applied inline (Layer 1). On macOS the call is
additionally wrapped by `sandbox-exec` (Layer 2) for filesystem and
network confinement; the binary ships on every macOS install so this
is automatic.

See [`docs/sandbox.md`](sandbox.md) for the layer model and policy
reference.

## Native smoke test

```bash
just test-tauri-smoke
just test-tauri-acp-smoke
```

The target builds only the native `.app` bundle, launches the packaged macOS
app, waits for the hecate sidecar to answer `/healthz`, quits Hecate, and
verifies the sidecar process exits. It intentionally skips `.dmg` packaging so
local smoke runs are faster and less vulnerable to temporary disk-image mount
flakes. It is not part of `just verify` because it opens a real GUI app
and is macOS-specific today.

`just test-tauri-acp-smoke` uses the same packaged app launch but also starts
the bundled `hecate-acp` sidecar without `HECATE_GATEWAY_URL`, expecting it to
discover the dynamic gateway URL from `hecate.runtime.json` and complete an ACP
`initialize` handshake. It intentionally stops at discovery + initialize; full
approval/task behavior stays in `just test-acp-smoke`.

## Footguns to know

Captured in detail at [`docs-ai/skills/tauri/SKILL.md`](../docs-ai/skills/tauri/SKILL.md);
the ones likely to bite an operator:

- **Gatekeeper / SmartScreen on first launch.** macOS bundles produced
  by a release-workflow run with the `APPLE_*` repo secrets configured
  are signed+notarized — no first-launch warning. Pre-signing alpha
  bundles, fork builds, or releases cut before the secrets landed need
  right-click → Open the first time. Windows is unsigned regardless;
  click "More info" on the SmartScreen warning. Document in release
  notes when shipping an unsigned build.
- **Quitting via `cmd+Q` (macOS) — not the red close button.** The window
  close button hides the window on macOS; only quitting kills the gateway
  child. We'll fix this when a tray mode lands; until then, document.
- **Data dir is platform-specific.** Settings saved on macOS don't migrate
  to a Linux build of the same version. Multi-machine users keep separate
  config per platform.

## Why we keep `//go:embed` instead of `frontendDist`

The default Tauri pattern is to point `frontendDist` at the UI build output and
let the webview load files directly. We don't, on purpose: the gateway serves
the UI from the same origin as the API, which keeps browser same-origin
enforcement intact. Splitting them would be a meaningful refactor for ~13 MB of
disk savings. Full rationale in the agent skill doc.
