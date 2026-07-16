# Desktop app

Hecate ships a native desktop app (`tauri/`) alongside the binary tarball and
Docker image. It's a thin Tauri 2.x chrome around the same `hecate` runtime
binary used everywhere else: on launch, the Rust layer spawns it in gateway
mode on a free loopback port, polls `/healthz`, then navigates a webview to
`http://127.0.0.1:{port}/` where the gateway serves its embedded UI.

Code: [`tauri/`](../../tauri/) · agent guide: [`docs-ai/skills/tauri/SKILL.md`](../../docs-ai/skills/tauri/SKILL.md) · CI: [`.github/workflows/test.yml`](../../.github/workflows/test.yml), [`.github/workflows/release.yml`](../../.github/workflows/release.yml), [`.github/workflows/tauri-build.yml`](../../.github/workflows/tauri-build.yml).

## Distribution

Released alongside the rest of the alpha. The release pipeline builds three
matrix legs in parallel and attaches bundles to the GitHub Release entry:

| Platform              | Bundle                                                  |
| --------------------- | ------------------------------------------------------- |
| macOS (Apple Silicon) | `Hecate_X.Y.Z_aarch64.dmg`                              |
| Linux x86_64          | `Hecate_X.Y.Z_amd64.deb`, `Hecate_X.Y.Z_amd64.AppImage` |
| Windows x86_64        | `Hecate_X.Y.Z_x64_en-US.msi`                            |

PR validation: [`test.yml`](../../.github/workflows/test.yml) runs the desktop
bundle matrix only after the cheaper Go, TypeScript, e2e, Docker smoke, and
Tauri Rust checks pass or skip by path filter. PR validation proves that the
macOS, Linux, and Windows bundles build, but does not upload unsigned bundles
as workflow artifacts. [`tauri-build.yml`](../../.github/workflows/tauri-build.yml)
is manual-only for explicit desktop rebuild/debug runs from the Actions tab. To
test-launch a bundle before merge, dispatch `tauri-build.yml` manually from the
PR branch.

Build success is not the same as platform confidence:

| Platform              | Current confidence                                                                                                                                          |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| macOS (Apple Silicon) | Maintainer-tested alpha path. Release bundles are signed, notarized, launch-smoked, and covered by the local `just test-tauri-smoke` macOS app smoke.       |
| Linux x86_64          | CI-built only. The `.deb` and `.AppImage` have not yet been manually launched or exercised on a Linux desktop, so expect packaging and runtime bugs.        |
| Windows x86_64        | CI-built only. The `.msi` has not yet been manually installed or exercised on Windows, and it is not code-signed yet, so SmartScreen warnings are expected. |

For Linux or Windows operators who need the safest path today, use Docker or
the standalone binary tarballs until the desktop bundles get real-machine
smoke coverage.

## Current state — `v0.3.0-alpha.1`

What works:

- Sidecar lifecycle: spawn, `/healthz` wait on startup; on quit (red-X,
  `cmd+Q`, or menu Quit) the app asks the gateway to drain via
  `POST /hecate/v1/system/shutdown`, polls `/healthz` until it stops
  responding (12 s deadline), then exits — same code path as
  `SIGINT`/`SIGTERM` from a terminal. `pgrep hecate` is empty afterward
  in both paths. When agent runs are in-flight, a native confirmation
  dialog appears first.
- Runtime discovery file (`hecate.runtime.json`) written by the sidecar gateway
  on successful startup and removed on app exit for native diagnostics.
- Same-origin loading of the embedded gateway UI from the sidecar port.
- Native Hecate menu with actions to focus the window, open the gateway log,
  open the data directory, and quit.
- Per-platform writable data dir (`~/Library/Application Support/sh.hecate.app/`,
  `%APPDATA%\sh.hecate.app\`, `~/.local/share/sh.hecate.app/`).
- Durable sqlite storage in that data dir (`hecate.db`) for settings,
  provider connections, projects, chats, tasks, usage, and approval state
  across app launches.
- Sidecar stderr piped to `<data_dir>/gateway.log` (truncated per launch);
  the startup splash shows failures with the log and data-directory paths, and
  adds a bootstrap-key recovery hint when startup fails before the gateway
  serves the web UI.
- Native app lifecycle breadcrumbs are written to the Tauri `app.log`: app
  startup, gateway sidecar spawn/readiness/shutdown, update-check dispatch,
  badge failures, and fallback sidecar termination. Use `gateway.log` for the
  gateway process stderr and `app.log` for the desktop wrapper lifecycle.
- Chats with a workspace show an **Open workspace** menu in the header. In the
  desktop app it launches common local editors, Terminal/iTerm2, or
  Finder/folder via Tauri commands; in the browser UI the local gateway handles
  the same action for loopback clients.
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
  sidecar staging. They have not yet been manually launched on real hardware,
  so treat them as experimental and expect bugs until smoke coverage exists.
- Auto-update is active. Each release emits a signed `latest.json`
  manifest as a GitHub Release asset and publishes the same manifest
  to `https://hecate.sh/releases/alpha/latest.json`, which alpha.28+
  desktop bundles read on launch. When a newer version is published,
  Hecate surfaces "Hecate X.Y.Z is available — Install and Restart"
  with live download progress. This flow is exercised on macOS; Linux and
  Windows updater behavior still needs real-machine testing. Maintainer-side
  keypair custody and rotation playbook:
  [`desktop-updater-signing.md`](desktop-updater-signing.md).

What doesn't yet:

- No Windows code signing — SmartScreen warns on every install. Real users
  click "More info → Run anyway"; documented in release notes. Authenticode
  - EV cert is roadmap.
- No Homebrew formula or cask yet. A formula would help CLI installation, and
  a cask would help app distribution. macOS now signs+notarizes via
  `APPLE_*` repo secrets; a cask would still be additional polish.
- No tray and no deep links.
- Linux and Windows: build-only and currently untested by maintainers. Need an
  actual launch on each platform before claiming they work.

## Roadmap

Three tiers by dependency, not by enthusiasm. Anything in Tier 1 is
"contained code change, do whenever"; Tier 2 needs a decision or a secret
before it ships; Tier 3 is real feature scope that's only worth doing once
the bundle is polished enough to recommend.

### Tier 1 — polish before the next alpha

| Item                                 | Scope                 | Notes                                                                                                                                                                                                                                                                                                  |
| ------------------------------------ | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Test the Linux + Windows bundles** | ~30 min per OS        | Download from the current alpha release, install the `.deb` / `.AppImage` / `.msi`, configure a provider, send one chat, quit (confirm the running-tasks dialog appears when an agent run is active), relaunch, confirm config persists. macOS is done; these two are the remaining platform unknowns. |
| **Homebrew distribution**            | Formula/cask decision | Useful for install ergonomics, especially the CLI. Does not remove the need for macOS signing/notarization for the desktop app; treat it as distribution, not trust.                                                                                                                                   |

### Tier 2 — operational gates

| Item                     | Cost / decision     | Notes                                                                                                                                                                           |
| ------------------------ | ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Windows code signing** | EV cert (~$300+/yr) | Lower priority. Reputation builds over hundreds of installs anyway, so signing a low-volume alpha is mostly about removing the SmartScreen warning, not unlocking distribution. |

### Tier 3 — features

| Item                            | Scope     | Notes                                                                                                                                               |
| ------------------------------- | --------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Tray / menubar mode**         | Multi-day | "Always on, click to focus." Adds tray icon + show/hide window logic, dock-icon hiding on macOS. Worth doing if "background gateway" is a use case. |
| **Deep links (`hecate://...`)** | ~1 day    | Open specific runs, configure providers from a link. Real value depends on whether such links would appear anywhere — premature today.              |

### Skip / reconsider later

- **CSP design.** Real but blocked on the gateway UI not setting a CSP header
  itself. Revisit when the gateway UI grows a CSP.
- **Crash reporting (Sentry / similar).** Premature for an alpha; the
  `gateway.log` capture covers most diagnostics.
- **Mobile (iOS/Android).** Tauri 2 supports it; we deleted the icon CLI's
  mobile output. Off-roadmap.

## Sandbox executor

The desktop app bundles the `hecate` binary. Agent tool calls (`shell_exec`,
`git_exec`, `file_write`) spawn a per-call `sh` subprocess directly from the
gateway with env sanitisation, output cap, and wall-clock timeout applied inline
(Layer 1). On macOS the call is additionally wrapped by `sandbox-exec` (Layer 2) for filesystem and network confinement; `sandbox-exec` ships on every macOS
install so this is automatic.

See [`docs/runtime/sandbox.md`](../runtime/sandbox.md) for the layer model and policy
reference.

## Native smoke test

```bash
just test-tauri-smoke
```

The target builds only the native `.app` bundle, launches the packaged macOS
app, waits for the hecate sidecar to answer `/healthz`, quits Hecate, and
verifies the sidecar process exits. It intentionally skips `.dmg` packaging so
local smoke runs are faster and less vulnerable to temporary disk-image mount
flakes. It is not part of `just verify` because it opens a real GUI app
and is macOS-specific today.

## Footguns to know

Captured in detail at [`docs-ai/skills/tauri/SKILL.md`](../../docs-ai/skills/tauri/SKILL.md);
the ones likely to bite an operator:

- **Gatekeeper / SmartScreen on first launch.** macOS bundles produced
  by a release-workflow run with the `APPLE_*` repo secrets configured
  are signed+notarized — no first-launch warning. Pre-signing alpha
  bundles, fork builds, or releases cut before the secrets landed need
  right-click → Open the first time. Windows is unsigned regardless;
  click "More info" on the SmartScreen warning. Document in release
  notes when shipping an unsigned build.
- **Window close and `cmd+Q` both quit cleanly.** The red-X, `cmd+Q`, and the menu "Quit Hecate" item all funnel through the same path. If any agent runs are in-flight, a native confirmation dialog appears ("X tasks still running. Quitting Hecate will stop them.") with Quit anyway / Keep running. On confirm — or when there are no running tasks — the gateway is asked to drain via `POST /hecate/v1/system/shutdown` (same code path as `SIGINT`/`SIGTERM`) before the app exits, so MCP subprocesses are torn down cleanly and no run is left stuck in `running`.
- **Reset local data is unavailable while the app is running.** Settings →
  Maintenance → Danger zone shows the action disabled. Quit Hecate completely
  before removing or replacing its platform data directory; the reserved
  `POST /hecate/v1/system/reset-data` route returns `409 conflict` without
  deleting anything until runtime-wide writer quiescence exists. Back up the
  directory first if you may need provider setup, chats, tasks, or project
  coordination state later.
- **Data dir is platform-specific.** Settings saved on macOS don't migrate
  to a Linux build of the same version. Multi-machine users keep separate
  config per platform.

## Why we keep `//go:embed` instead of `frontendDist`

The default Tauri pattern is to point `frontendDist` at the UI build output and
let the webview load files directly. We don't, on purpose: the gateway serves
the UI from the same origin as the API, which keeps browser same-origin
enforcement intact. Splitting them would be a meaningful refactor for ~13 MB of
disk savings. Full rationale in the agent skill doc.
