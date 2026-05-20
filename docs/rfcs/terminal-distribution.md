# Terminal / CLI Distribution

> **Status:** proposed; not implemented.
> **Current source of truth:** [Deployment](../deployment.md),
> [ACP bridge](../acp.md), and [Desktop app](../desktop-app.md) for today's
> shipped binaries and launch modes.
> **Next action:** decide the command surface and build the first attach-only TUI
> slice.

Hecate has several operator surfaces today:

- the desktop app, which owns the gateway sidecar lifecycle;
- Docker, which runs the gateway in a container;
- release archives, which include `hecate` and `hecate-acp`;
- the embedded React UI served by the gateway;
- `hecate-acp`, the stdio bridge launched by ACP-capable editors.

The release archive is already a terminal install shape, but it is not yet a
terminal product. Operators can start `hecate` and open the web UI, but there is
no first-class TUI, no guided terminal setup, and no single terminal workflow
that feels as complete as the desktop app.

This RFC defines the target shape for a terminal-only distribution: one archive
or package that installs `hecate`, installs `hecate-acp`, can run the gateway
headlessly, and eventually provides a TUI for common operator workflows.

## Goals

- Make terminal install a first-class Hecate distribution, not only a fallback
  for users who do not install the desktop app.
- Keep `hecate` as the main runtime and CLI executable.
- Ship `hecate-acp` beside `hecate` in every terminal package.
- Add a TUI that can operate Hecate without opening the browser UI.
- Keep the embedded browser UI available for deep views and parity.
- Make editor setup deterministic: editor ACP hosts launch `hecate-acp`, and
  `hecate-acp` discovers or is pointed at the running gateway.
- Keep Docker, desktop, and archive packaging aligned around the same binary
  pair and version stamp.
- Keep all terminal surfaces on the same HTTP/SSE API as the React UI.

## Non-goals

- Do not replace the desktop app.
- Do not remove the embedded React UI from the `hecate` binary.
- Do not make `hecate-acp` an optional extra package.
- Do not make editors launch `hecate` directly as the stable ACP path.
- Do not add hosted or multi-user deployment support.
- Do not add authentication to the local gateway as part of this work. The
  loopback-first, local-operator threat model remains.
- Do not implement ACP editor-owned workspace reverse-RPC here. That is covered
  by [ACP editor-owned workspace transport](acp-editor-owned-workspace.md).
- Do not build a full terminal observability dashboard before the core TUI
  workflows are useful.

## Product Shape

The terminal distribution has two executables:

| Binary       | Purpose                                                                                              |
| ------------ | ---------------------------------------------------------------------------------------------------- |
| `hecate`     | Main runtime and CLI executable: starts the gateway service today and hosts future CLI/TUI commands. |
| `hecate-acp` | Stdio ACP bridge launched by Zed, JetBrains, VS Code/Cursor extensions, and other ACP hosts.         |

`hecate-acp` stays a separate binary even if `hecate` grows helper commands
such as `hecate acp doctor` or `hecate acp config zed`. ACP hosts naturally
launch a stdio process, and a standalone bridge is easier to configure,
package, debug, and version.

## Current State

| Surface                 | State                                                                 |
| ----------------------- | --------------------------------------------------------------------- |
| Release archive         | Ships `hecate`, `hecate-acp`, `LICENSE`, and `README.md`.             |
| Docker image            | Ships both binaries; entrypoint runs the gateway.                     |
| Desktop app             | Bundles both binaries as sidecars.                                    |
| `hecate-acp`            | Implemented but experimental editor bridge.                           |
| Terminal TUI            | Not implemented.                                                      |
| Terminal setup commands | Not implemented beyond existing gateway startup and release binaries. |
| Homebrew                | Not published yet.                                                    |

## Proposed Commands

The command surface should be explicit while the TUI is young:

| Command                       | Behavior                                                                                                                               |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `hecate serve`                | Start the gateway without the TUI. Keep the embedded web UI available on the configured HTTP address.                                  |
| `hecate tui`                  | Start or attach to a gateway and open the terminal operator UI.                                                                        |
| `hecate doctor`               | Check data directory, port binding, SQLite, provider readiness, external-agent readiness, `hecate-acp`, and OTel export configuration. |
| `hecate version`              | Print Hecate version, build metadata, and companion compatibility information.                                                         |
| `hecate acp doctor`           | Check that `hecate-acp` can reach the active gateway and complete the bridge startup path.                                             |
| `hecate acp config zed`       | Print a ready-to-paste local Zed ACP server config.                                                                                    |
| `hecate acp config jetbrains` | Print a ready-to-paste local JetBrains ACP config.                                                                                     |
| `hecate acp config vscode`    | Print a ready-to-paste VS Code/Cursor ACP-host config once the supported host shape is known.                                          |
| `hecate-acp`                  | Run the ACP stdio bridge. This remains the editor-facing executable.                                                                   |
| `hecate-acp --version`        | Print bridge version and compatible gateway version.                                                                                   |

The default `hecate` behavior should not flip to TUI until the TUI is useful
for normal alpha operation. A safe migration path is:

1. Keep today's bare `hecate` behavior: start the gateway service.
2. Add `hecate tui`.
3. Make terminal docs recommend `hecate tui` once it is useful.
4. Flip bare `hecate` to the TUI only in a clearly documented alpha release.

## Gateway Lifecycle

The TUI needs a concrete gateway lifecycle story.

| Mode              | Command shape                                | Lifecycle                                                                                                                                                                          |
| ----------------- | -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| TUI-owned gateway | `hecate tui`                                 | Pick a loopback port, start the gateway as an owned child process or in-process runtime, write runtime discovery state, and stop it on TUI exit unless configured to keep running. |
| Attach mode       | `hecate tui --connect http://127.0.0.1:8765` | Connect to an already-running gateway and do not own its lifecycle.                                                                                                                |
| Headless gateway  | `hecate serve`                               | Start only the gateway; used by scripts, services, Docker, and operators who prefer the browser UI.                                                                                |

The preferred implementation is child-process ownership, matching the desktop
sidecar model. That gives clearer logs, crash boundaries, and shutdown
behavior. In-process ownership is acceptable if tests cover startup, shutdown,
port collision, and runtime-state cleanup.

## TUI Scope

The TUI should not clone the full React app at first. It should cover the
workflows that make a terminal install self-contained:

| Area          | First useful TUI capability                                                                                                                                    |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Startup       | Show gateway URL, data directory, storage backend, version, and trace/export status.                                                                           |
| Connections   | Show configured providers, detected local providers, model counts, credential state, route blockers, and external-agent readiness.                             |
| Chats         | Start Hecate Chat and External Agent sessions, stream transcript output, show route/run/trace metadata, queue prompts, and show reported external-agent usage. |
| Approvals     | Render pending task and external-agent approvals; approve, deny, or cancel without opening the browser UI.                                                     |
| Tasks         | List tasks/runs, show status, and open a run timeline. Deep patch review can stay browser-first initially.                                                     |
| Observability | Show recent requests, trace IDs, provider/model selection, and final outcome. Deep route reports can stay browser-first initially.                             |
| Settings      | Show key runtime settings and diagnostics. Full editing can stay browser-first until the TUI patterns settle.                                                  |

The TUI must use the same HTTP/SSE API as the React UI. It should not get a
special in-process backdoor. If the TUI needs an operation, the product API
needs it too.

## Shipping `hecate-acp`

`hecate-acp` is part of the terminal distribution contract:

- Every release archive includes `hecate` and `hecate-acp`.
- Docker includes both binaries under `/usr/local/bin`.
- The desktop app bundles both sidecars.
- A future Homebrew formula installs both into the same prefix.
- Both binaries are stamped with the same release version.
- Release smoke tests verify both binaries exist and print matching versions.

`hecate-acp` should discover the gateway in this order:

1. `HECATE_GATEWAY_URL`
2. runtime discovery state in `HECATE_DATA_DIR`
3. native app runtime discovery state
4. `http://127.0.0.1:8765`

Terminal setup should make the stable path easy:

```sh
hecate tui
hecate acp config zed
```

The generated editor config should point at the installed `hecate-acp` path and
only include `HECATE_GATEWAY_URL` when discovery would otherwise be ambiguous.

## Install Shapes

### Release Archives

Release archives remain the first terminal artifact:

```text
hecate
hecate-acp
LICENSE
README.md
```

Optional future archive content:

```text
completions/
  hecate.zsh
  hecate.bash
  hecate.fish
```

### Homebrew

A future Homebrew formula should install both binaries:

```ruby
bin.install "hecate"
bin.install "hecate-acp"
```

The formula should also install shell completions once the CLI can generate
them. Homebrew improves terminal install and upgrade ergonomics; it does not
replace desktop signing/notarization.

### Docker

Docker already includes both binaries. The entrypoint should remain headless
gateway mode. Running the TUI inside Docker is a niche attached-terminal use
case, not the primary Docker workflow.

### Native App

The native app keeps its own sidecar lifecycle. It bundles `hecate` and
`hecate-acp`, but this RFC does not change the app UX.

## Runtime Discovery

The gateway writes runtime discovery state so independently launched helpers can
find it:

```json
{
  "base_url": "http://127.0.0.1:52341",
  "listen_addr": "127.0.0.1:52341",
  "pid": 12345,
  "updated_unix": 1770000000
}
```

Terminal mode should write the same shape as desktop and headless modes. If
the TUI owns a dynamic gateway port, `hecate-acp` depends on this file unless
the generated editor config pins `HECATE_GATEWAY_URL`.

## Version Compatibility

`hecate` and `hecate-acp` should normally come from the same release, but users
will move binaries by hand. The bridge should fail early with useful copy when
it detects an incompatible gateway.

Minimum contract:

- `hecate version` prints version and build commit.
- `hecate-acp --version` prints version and build commit.
- `hecate-acp` checks gateway compatibility during startup.
- Compatibility errors say which binary is old and how to fix it.

Future:

- expose a small compatibility object from a Hecate-native status endpoint;
- make `hecate doctor` warn when `hecate-acp` on `PATH` does not match
  `hecate`;
- make release smoke tests assert archive and Docker version alignment.

## Configuration

Terminal distribution should keep the same env-driven runtime configuration as
other launch modes:

| Variable                    | Meaning                                                               |
| --------------------------- | --------------------------------------------------------------------- |
| `HECATE_ADDRESS`            | Gateway bind address for headless mode. Defaults to `127.0.0.1:8765`. |
| `HECATE_DATA_DIR`           | Runtime state, SQLite database, logs, and runtime discovery state.    |
| `HECATE_GATEWAY_URL`        | Explicit gateway URL for helper processes such as `hecate-acp`.       |
| `HECATE_AGENT_ADAPTERS_DIR` | Managed external-agent ACP launcher cache.                            |

Open question: should the TUI have its own config file for preferences such as
keybindings, compact mode, and theme? The first implementation can keep
terminal UI preferences under the gateway data dir or a platform config dir.
Gateway runtime settings should stay env/API-driven.

## Security

The terminal distribution keeps Hecate's existing local-first security model:

- bind to loopback by default;
- process requests as the local operator;
- keep secrets in the same control-plane secret store as other launch modes;
- keep Hecate Chat tool calls behind the existing task runtime, approvals, and
  per-call sandbox;
- treat external-agent CLIs as trusted subprocesses running in the selected
  workspace;
- make approvals and workspace state visible in the TUI before executing
  high-risk actions.

The TUI must not introduce a privileged bypass around policy, approvals,
workspace validation, or sandbox rules.

## Observability And Logs

Terminal users need enough visibility without opening the browser:

- TUI status should show the current trace/export state and last error.
- `hecate doctor` should include OTel endpoint/config state.
- TUI-owned gateway mode should write a gateway log file and expose its path.
- Attach mode should tell the user it does not own gateway logs.
- Trace IDs shown in the TUI should match the React UI and API response
  headers.

## Testing

Required before calling the terminal distribution stable:

- `hecate serve` starts, writes runtime discovery state, serves `/healthz`, and
  exits cleanly.
- `hecate tui --connect URL` attaches to a fake or real gateway without
  starting another gateway.
- TUI-owned gateway starts on a free loopback port and shuts down on TUI exit.
- Port collision fallback works for TUI-owned dynamic ports.
- `hecate-acp` discovers a TUI-owned gateway through runtime state.
- `hecate acp config zed` and `hecate acp config jetbrains` print valid config
  with the resolved bridge path.
- Archive smoke verifies `hecate` and `hecate-acp` are both present and
  version-aligned.
- Docker smoke verifies both binaries exist in the image.

## Documentation Updates When Implemented

- `README.md`: add Terminal/TUI beside Desktop and Docker.
- `docs/deployment.md`: split "Binary install" into "Terminal install".
- `docs/acp.md`: describe terminal install as the preferred local
  `hecate-acp` source.
- `docs/development.md`: add local TUI commands and test recipes.
- `docs/release.md`: add terminal archive smoke and ACP companion checks.
- `docs/known-limitations.md`: remove or rewrite any Homebrew/TUI gaps that
  are closed by the implementation.

## Open Questions

### Should `hecate` default to TUI or headless gateway mode?

Stable target: default to TUI. Migration path: introduce `hecate tui` first,
then flip the bare `hecate` default only after the TUI covers enough operator
workflows.

### Should the TUI own the gateway in-process or as a child process?

Child process is preferred for parity with the desktop app and cleaner logs.
In-process ownership remains viable if it makes the first TUI substantially
simpler.

### Should `hecate-acp` be replaced by `hecate acp run`?

No. A wrapper command is fine for convenience, but the editor-facing stable path
should remain `hecate-acp`.

### Do we need a terminal-only build without the embedded React UI?

Not initially. One `hecate` binary keeps the release matrix small. A smaller
no-web build can be revisited if binary size becomes a real problem.

### Which TUI stack should Hecate use?

Open. A Go-native TUI keeps the terminal distribution inside the existing
binary/toolchain. The choice should be driven by testability, accessibility,
SSE handling, and how well it renders long transcripts and approval forms.

## Implementation Plan

1. Add command structure without changing the current default:
   `hecate serve`, `hecate tui`, `hecate doctor`, `hecate version`,
   `hecate acp doctor`, and `hecate acp config ...`.
2. Build a minimal TUI that can attach to an existing gateway and show
   status/connections.
3. Add TUI-owned gateway mode and runtime-discovery tests.
4. Add Hecate Chat, External Agent chat, and approval flows to the TUI.
5. Add ACP config generators and bridge doctor command.
6. Update release smoke tests to assert both terminal binaries are present and
   version-aligned.
7. Update docs and README Quick Start.
8. Flip bare `hecate` from gateway mode to TUI only when the TUI is ready for
   normal operator use.
