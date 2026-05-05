# Terminal Distribution and ACP Companion — Candidate RFC

> **Status:** candidate for stable-core distribution work. This document
> proposes a first-class terminal distribution for Hecate: a CLI/TUI operator
> experience, the gateway runtime, and the `hecate-acp` editor bridge shipped
> together.
> **Related:** [Deployment](../deployment.md), [ACP bridge](../acp.md),
> [Desktop app](../desktop-app.md), [External agent adapters](../external-agent-adapters.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

Hecate currently has three install shapes:

- Native desktop app: no terminal, app-owned sidecar lifecycle.
- Docker image: container-owned gateway lifecycle.
- Release tarball: `hecate` plus `hecate-acp`, but no first-class terminal
  operator experience beyond starting the embedded web UI.

The terminal distribution should become its own product surface, not a fallback
for people who do not use the desktop app. It should be pleasant on a laptop,
scriptable on a server, and predictable for ACP-aware editors.

## Goals

- Ship a first-class CLI/TUI distribution for terminal users.
- Keep `hecate` as the primary terminal binary.
- Ship `hecate-acp` beside `hecate` in every terminal package.
- Let terminal users operate Hecate without opening the browser UI.
- Keep the embedded browser UI available for advanced views and parity.
- Make editor setup deterministic: ACP hosts launch `hecate-acp`, and
  `hecate-acp` discovers or is pointed at the running gateway.
- Keep Docker and native app packaging aligned with the same binary pair.
- Avoid requiring Homebrew for the first implementation; tarballs remain the
  initial terminal artifact, Homebrew comes later.

## Non-goals

- Do not replace the native desktop app.
- Do not remove the embedded React UI from the `hecate` binary.
- Do not make `hecate-acp` an optional extra package.
- Do not make editors launch `hecate` directly as the stable ACP path.
- Do not implement a remote multi-user server model.
- Do not add auth as part of terminal distribution. The current local-first,
  loopback-default threat model remains.
- Do not build an entire terminal observability dashboard in v0. The TUI should
  cover the high-value operator workflows first.

## Product Shape

The terminal distribution has two executables:

| Binary | Purpose |
|---|---|
| `hecate` | Gateway runtime plus CLI/TUI operator surface. |
| `hecate-acp` | Stdio ACP bridge launched by Zed, JetBrains, VS Code/Cursor extensions, and other ACP hosts. |

The key design choice: `hecate-acp` stays a separate binary even if `hecate`
also grows ACP helper subcommands. ACP hosts naturally launch a stdio process,
and a standalone bridge is easier to configure, package, debug, and version.

## Proposed Commands

| Command | Behavior |
|---|---|
| `hecate` | Start the terminal operator experience. Default to TUI-owned gateway mode. |
| `hecate tui` | Explicit TUI entrypoint. Same as `hecate` once stable. |
| `hecate serve` | Start the gateway without the TUI. Keep the embedded web UI available on the configured HTTP address. |
| `hecate doctor` | Check data directory, port binding, SQLite, provider readiness, external-agent readiness, sidecar paths, and OTel export config. |
| `hecate version` | Print the Hecate version, build metadata, and bundled/expected companion versions. |
| `hecate acp doctor` | Check that `hecate-acp` can reach the active gateway and complete `initialize`. |
| `hecate acp config zed` | Print a ready-to-paste local Zed ACP server config. |
| `hecate acp config jetbrains` | Print a ready-to-paste local JetBrains ACP config. |
| `hecate acp config vscode` | Print a ready-to-paste local VS Code/Cursor ACP-host config once the supported host shape is known. |
| `hecate-acp` | Run the ACP stdio bridge. This remains the editor-facing executable. |
| `hecate-acp --version` | Print bridge version and compatible gateway version. |
| `hecate-acp doctor` | Same diagnostic as `hecate acp doctor`, useful when users run the bridge path directly. |

### Default Mode

`hecate` should default to the TUI once the TUI is useful enough. Before then,
we can keep today's behavior and introduce `hecate tui` first. The stable goal
is:

```sh
hecate
```

starts the local operator console in the terminal.

## Gateway Lifecycle

The TUI needs a concrete lifecycle story. Two modes cover the common cases:

| Mode | Command shape | Lifecycle |
|---|---|---|
| TUI-owned gateway | `hecate` / `hecate tui` | Pick a loopback port, start the gateway in-process or as an owned child, write runtime discovery state, stop it on TUI exit unless configured to keep running. |
| Attach mode | `hecate tui --connect http://127.0.0.1:8765` | Connect to an already-running gateway, do not own its lifecycle. |
| Headless gateway | `hecate serve` | Start the gateway only; used by scripts, services, Docker, and operators who prefer the browser UI. |

Preferred implementation for v0:

- `hecate serve` keeps the current gateway behavior.
- `hecate tui` starts an owned gateway on a free loopback port by default.
- `hecate tui --connect URL` attaches to an existing gateway.
- `hecate` aliases to `hecate serve` until the TUI is ready, then flips to
  `hecate tui` in a clearly documented alpha breaking release.

Open implementation choice:

- **In-process gateway:** one process, shared memory, simpler lifecycle, harder
  to separate logs and crash domains.
- **Child-process gateway:** mirrors the native app sidecar model, clearer
  logs/process ownership, but requires spawning the same binary with an internal
  serve mode and carefully avoiding recursive launch.

The RFC leans toward **child-process gateway** for parity with the native app
and cleaner failure boundaries, but either is acceptable if tests cover startup,
shutdown, and port collision behavior.

## TUI Scope

The TUI should not try to clone the whole React app immediately. It should
cover the workflows that make a terminal install feel self-contained:

| Area | v0 TUI capability |
|---|---|
| Startup | Show gateway status, active URL, data dir, storage backend, and trace/export status. |
| Providers | List configured providers, detected local providers, routable model count, last error, and readiness. Add detected local providers. |
| Chats | Start model chat and external-agent chat; stream output; show route/cost/trace metadata; show reported external-agent usage. |
| Agent approvals | Render pending external-agent approvals; allow once/session/workspace/tool; deny; cancel; show grants. |
| Tasks | List tasks/runs and show status. Creating and deep task inspection can remain browser-first initially. |
| Observability | Show recent requests and trace IDs. Deep route reports can remain browser-first initially. |
| Settings | Show key runtime settings and diagnostics. Full pricebook/retention editing can remain browser-first initially. |

The TUI should use the same HTTP API as the React app. It should not get a
special in-process backdoor. That keeps terminal and browser surfaces honest:
if the TUI needs an operation, the product API needs it too.

## Shipping `hecate-acp`

`hecate-acp` is part of the terminal distribution contract:

- Every release tarball includes `hecate` and `hecate-acp`.
- The Docker image includes both under `/usr/local/bin`.
- Homebrew formula installs both into the same prefix.
- Native app bundles both as sidecars.
- Both binaries are stamped with the same release version.
- Checksums cover the full archive.

`hecate-acp` should discover the gateway in this order:

1. `HECATE_GATEWAY_URL`
2. `hecate.runtime.json` in `GATEWAY_DATA_DIR`
3. Native app data-dir runtime state
4. `http://127.0.0.1:8765`

The terminal distribution should make the stable path easy:

```sh
hecate tui
hecate acp config zed
```

The generated editor config should point at the installed `hecate-acp` path and
only include `HECATE_GATEWAY_URL` when the gateway URL is not discoverable.

## Install Shapes

### Release Tarballs

Tarballs remain the first terminal artifact:

```text
hecate
hecate-acp
LICENSE
README.md
```

Nice-to-have later:

```text
completions/
  hecate.zsh
  hecate.bash
  hecate.fish
```

### Homebrew

Homebrew should install both binaries:

```ruby
bin.install "hecate"
bin.install "hecate-acp"
```

The formula should also install shell completions once generated by the CLI.
Homebrew improves terminal install and upgrades, but it does not replace
desktop-app signing/notarization.

### Docker

Docker already includes both binaries. The entrypoint should remain `hecate`
or `hecate serve`; users generally should not run the TUI inside Docker unless
they explicitly want an attached terminal session.

### Native App

The native app keeps its own lifecycle. It bundles `hecate` and `hecate-acp`,
but this RFC does not change the app UX.

## Runtime Discovery File

The gateway writes runtime discovery state so independently launched helper
processes can find it:

```json
{
  "base_url": "http://127.0.0.1:52341",
  "listen_addr": "127.0.0.1:52341",
  "pid": 12345,
  "updated_unix": 1770000000
}
```

Terminal mode should write this file in the same shape as other launch modes.
If the TUI owns a dynamic gateway port, `hecate-acp` depends on this file unless
the generated editor config pins `HECATE_GATEWAY_URL`.

## Version Compatibility

`hecate` and `hecate-acp` should be built and distributed from the same release,
but mismatches happen when users move binaries by hand. The bridge should fail
early with a helpful message when it detects an incompatible gateway.

Minimum contract:

- `hecate --version` prints version and build commit.
- `hecate-acp --version` prints version and build commit.
- `hecate-acp initialize` path checks gateway compatibility during startup.
- Error copy says which binary is old and how to fix it.

Future:

- `GET /hecate/v1/whoami` or equivalent exposes a small
  `acp_bridge_compatibility` object.
- `hecate doctor` warns if `hecate-acp` on `PATH` does not match `hecate`.

## Configuration

Terminal distribution should keep the same env-driven configuration as other
launch modes:

| Variable | Meaning |
|---|---|
| `GATEWAY_ADDRESS` | Gateway bind address for `serve` mode. Defaults to `127.0.0.1:8765`. |
| `GATEWAY_DATA_DIR` | Runtime state, SQLite database, logs, and `hecate.runtime.json`. |
| `HECATE_GATEWAY_URL` | Explicit gateway URL for helper processes such as `hecate-acp`. |
| `HECATE_AGENT_ADAPTERS_DIR` | Managed external-agent ACP launcher cache. |

Open question: should the TUI have its own config file for UI preferences
such as keybindings and theme? The first implementation can store this under
`GATEWAY_DATA_DIR` or a platform config dir, but gateway runtime settings should
stay env/API-driven.

## Observability and Logs

Terminal users need log access without spelunking:

- TUI footer/status should show current trace export health.
- `hecate doctor` should include OTel endpoint/config state.
- TUI-owned gateway mode should write a gateway log file and expose its path.
- Attach mode should tell the user it does not own the gateway logs.

## Testing

Required tests before calling the terminal distribution stable:

- `hecate serve` starts, writes runtime discovery state, serves `/healthz`, and
  exits cleanly.
- `hecate tui --connect URL` attaches to a fake or real gateway without
  starting another gateway.
- TUI-owned gateway starts on a free loopback port and shuts down on TUI exit.
- Port collision fallback works for TUI-owned dynamic ports.
- `hecate-acp` can discover a TUI-owned gateway through runtime state.
- `hecate acp config zed` and `hecate acp config jetbrains` print valid JSON
  with the resolved bridge path.
- Archive smoke verifies `hecate` and `hecate-acp` are both present and stamped
  with the same version.

## Documentation Updates

When this lands, update:

- `README.md` Quick Start: add Terminal/TUI beside Desktop and Docker.
- `docs/deployment.md`: split "Binary install" into "Terminal install".
- `docs/acp.md`: describe terminal install as the preferred local
  `hecate-acp` source.
- `docs/development.md`: add local TUI commands.
- `docs/release.md`: add terminal archive smoke and ACP companion checks.

## Open Questions

### Should `hecate` default to TUI or serve?

Stable target: default to TUI. Migration path: introduce `hecate tui` first,
then flip the default in an alpha release once the TUI covers enough operator
workflows.

### Should the TUI own the gateway in-process or as a child process?

The native app already uses a sidecar lifecycle, and matching that model makes
logs, crashes, and shutdown easier to reason about. Child process is the
preferred direction unless implementation complexity is much higher than
expected.

### Should `hecate-acp` be replaced by `hecate acp run`?

No for the stable editor-facing path. A wrapper command is fine as a convenience
or diagnostic path, but editors should keep launching `hecate-acp` because it is
simple, explicit, and already matches ACP host expectations.

### Do we need a terminal-only build without embedded React UI?

Not initially. Keeping one `hecate` binary avoids packaging matrix sprawl. A
smaller no-web build can be revisited if binary size becomes a real problem.

## Implementation Plan

1. Add command structure without changing current default:
   `hecate serve`, `hecate tui`, `hecate doctor`, `hecate version`,
   `hecate acp doctor`, and `hecate acp config ...`.
2. Build a minimal TUI shell that can attach to an existing gateway and show
   status/providers.
3. Add TUI-owned gateway mode and runtime-state discovery tests.
4. Add Chats and approval flows to the TUI.
5. Add ACP config generators and bridge doctor command.
6. Update release smoke tests to assert both terminal binaries are present and
   version-aligned.
7. Update docs and README Quick Start.
8. Flip `hecate` default from `serve` to `tui` only when the TUI is good enough
   for normal operator use.
