# Desktop app

Hecate ships a native desktop app (`tauri/`) alongside the binary tarball and
Docker image. It's a thin Tauri 2.x chrome around the same `gateway` binary
used everywhere else: on launch, the Rust layer spawns the gateway as a
companion process on a free loopback port, polls `/healthz`, then navigates a
webview to `http://127.0.0.1:{port}/` where the gateway serves its embedded UI.

Code: [`tauri/`](../tauri/) · agent guide: [`ai/skills/tauri/SKILL.md`](../ai/skills/tauri/SKILL.md) · CI: [`.github/workflows/release.yml`](../.github/workflows/release.yml), [`.github/workflows/tauri-build.yml`](../.github/workflows/tauri-build.yml).

## Distribution

Released alongside the rest of the alpha. The release pipeline builds three
matrix legs in parallel and attaches bundles to the GitHub Release entry:

| Platform | Bundle |
|---|---|
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg` |
| Linux x86_64 | `hecate-app_X.Y.Z_amd64.deb`, `hecate-app_X.Y.Z_amd64.AppImage` |
| Windows x86_64 | `Hecate_X.Y.Z_x64_en-US.msi` |

PR validation: [`tauri-build.yml`](../.github/workflows/tauri-build.yml) runs
the same matrix on PRs touching the desktop pipeline and persists bundles as
14-day workflow artifacts so reviewers can test-launch before merge.

## Current state — `v0.1.0-alpha.9`

What works:

- Sidecar lifecycle (spawn, healthz wait, kill on exit; `pgrep gateway` is
  empty after `cmd+Q`).
- Same-origin loopback to the embedded gateway UI; the sidecar UI just loads.
- Per-platform writable data dir (`~/Library/Application Support/com.hecate.app/`,
  `%APPDATA%\com.hecate.app\`, `~/.local/share/com.hecate.app/`).
- Sidecar stderr piped to `<data_dir>/gateway.log` (truncated per launch);
  the 30 s healthz timeout error message points at the file.
- Cross-platform CI matrix with PR validation, draft skipping, run
  cancellation on push, and signed nothing.
- macOS bundle launch-validated end-to-end: download → mount → drag to
  `/Applications` → right-click Open (Gatekeeper escape) → splash → UI →
  configure provider → quit cleanly. Linux and Windows bundles build
  green in CI but have not yet been launch-tested on real hardware.

What doesn't yet:

- No code signing — macOS Gatekeeper and Windows SmartScreen warn on
  every install. Real users need the right-click-Open / More-info-Run-anyway
  escape, documented in release notes.
- Real artwork — current icons are a solid `#1a1a2e` block, format-correct
  but visually placeholder.
- No auto-update — plugin is wired but `active: false` until a signing
  keypair and update endpoint are decided.
- No native menubar, no tray, no window-state persistence, no deep links.
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
| **Test the Linux + Windows bundles** | ~30 min per OS | Download from the `v0.1.0-alpha.9` release, install the `.deb` / `.AppImage` / `.msi`, configure a provider, send one chat, quit, relaunch, confirm config persists. macOS is done; these two are the remaining platform unknowns. |
| **Real icons** | ~5 min once art exists | Source a 1024×1024 PNG, run `bunx @tauri-apps/cli icon path/to/source.png`, prune the iOS/Android/Windows-Store outputs. Format-correct placeholders are committed today. |
| **Better startup-error UX** | ~1 h | Today a sidecar failure leaves the splash spinning forever and surfaces the error only in the window title. Add a Tauri event channel + an error view in `splash/index.html` that quotes the `gateway.log` path. |
| **Window state persistence** | ~15 min | Install [`tauri-plugin-window-state`](https://docs.rs/tauri-plugin-window-state/), register it. Save/restore size + position across launches. |

### Tier 2 — operational gates

| Item | Cost / decision | Notes |
|---|---|---|
| **macOS code signing** | Apple Developer Program ($99/yr) | Biggest single user-friction reduction. Cert + password go into `APPLE_CERTIFICATE`/`APPLE_CERTIFICATE_PASSWORD` Actions secrets; `tauri-action` picks them up automatically. Notarization adds ~5 min to the macOS leg. |
| **Auto-updater wiring** | Decide endpoint + generate keypair | `tauri-plugin-updater` is installed but `active: false`. Needs `bunx tauri signer generate -w ~/.tauri/hecate.key`, a static `latest.json` host (GitHub Release asset is fine), and the pubkey committed to `tauri.conf.json`. Probably wait until release cadence is established — auto-update on a weekly-bumping alpha is annoying. |
| **Windows code signing** | EV cert (~$300+/yr) | Lower priority. Reputation builds over hundreds of installs anyway, so signing a low-volume alpha is mostly about removing the SmartScreen warning, not unlocking distribution. |

### Tier 3 — features

| Item | Scope | Notes |
|---|---|---|
| **Native menubar** | ~half a day | File / Edit / View / Window / Help with platform conventions (Cmd+Q on macOS, Alt+F4 on Windows). Tauri 2 menu API. |
| **Tray / menubar mode** | Multi-day | "Always on, click to focus." Adds tray icon + show/hide window logic, dock-icon hiding on macOS. Worth doing if "background gateway" is a use case. |
| **Deep links (`hecate://...`)** | ~1 day | Open specific runs, configure providers from a link. Real value depends on whether such links would appear anywhere — premature today. |

### Skip / reconsider later

- **CSP design.** Real but blocked on the gateway UI not setting a CSP header
  itself. Not a security emergency on a loopback-only app. Revisit when the
  gateway UI grows a CSP.
- **Crash reporting (Sentry / similar).** Premature for an alpha; the
  `gateway.log` capture covers most diagnostics.
- **Mobile (iOS/Android).** Tauri 2 supports it; we deleted the icon CLI's
  mobile output. Off-roadmap.

## Sandbox executor

The desktop app bundles only the `gateway` binary. Agent tool calls
(`shell_exec`, `git_exec`, `file_write`) spawn a per-call `sh`
subprocess directly from the gateway with rlimits, env sanitisation,
and an output cap applied inline (Layer 1). On macOS the call is
additionally wrapped by `sandbox-exec` (Layer 2) for filesystem and
network confinement; the binary ships on every macOS install so this
is automatic.

See [`docs/sandbox.md`](sandbox.md) for the layer model and policy
reference.

## Footguns to know

Captured in detail at [`ai/skills/tauri/SKILL.md`](../ai/skills/tauri/SKILL.md);
the ones likely to bite an operator:

- **Gatekeeper / SmartScreen on first launch.** Until signing lands, macOS
  users right-click → Open the first time; Windows users click "More info"
  on the SmartScreen warning. Document in release notes.
- **Quitting via `cmd+Q` (macOS) — not the red close button.** The window
  close button hides the window on macOS; only quitting kills the gateway
  child. We'll fix this when a tray mode lands; until then, document.
- **Data dir is platform-specific.** Settings saved on macOS don't migrate
  to a Linux build of the same version. Multi-machine users keep separate
  config per platform.

## Why we keep `//go:embed` instead of `frontendDist`

The default Tauri pattern is to point `frontendDist` at the UI build output and
let the webview load files directly. We don't, on purpose: the gateway serves
the UI over the same loopback origin as the API, which keeps same-origin
enforcement intact. Splitting them would be a meaningful refactor for ~13 MB
of disk savings. Full rationale in the agent skill doc.
