# CLI structure for `hecate`

> **Status:** partially implemented.
> **Current source of truth:** [`cmd/hecate/cli.go`](../../../cmd/hecate/cli.go), [`cmd/hecate/main.go`](../../../cmd/hecate/main.go), and [`docs/runtime/mcp.md`](../../runtime/mcp.md).
> **Next action:** make bare `hecate` the terminal operator UI, then add `hecate ui`, `status`, `about`, and `doctor` in focused follow-up PRs.

## Implementation status

The first command-tree slice is implemented with Cobra:

- `hecate serve` starts the runtime / HTTP API / embedded UI server.
- `hecate mcp serve` starts the stdio MCP server.
- `hecate version`, `hecate --version`, and `hecate -v` print the version.
- Bare `hecate` still starts the runtime as a temporary compatibility alias.
- `hecate mcp-server` still works as a hidden compatibility alias.

Not implemented yet: the terminal operator UI for bare `hecate`, `hecate ui`,
`status`, `about`, `doctor`, auth commands, ACP server commands, and
`migrate`.

## Problem

The `hecate` binary today routes one flag (`--version`/`-v`/`version`) and one
ad-hoc subcommand (`mcp-server`), then falls through to "start the runtime."
That made sense when Hecate was mostly a local gateway process. It no longer
matches the product:

1. **Hecate is an operator console, not only a server.** The binary now
   represents multiple surfaces: local runtime, browser UI, MCP server,
   supervised ACP agents, diagnostics, and future migration / auth commands.
2. **Humans need a better default.** Operators should be able to type
   `hecate` and land in an interactive terminal control surface. Long-running
   runtime startup should be explicit as `hecate serve`.
3. **Protocol and maintenance commands need a real command tree.**
   [`migration-cli.md`](migration-cli.md) is blocked on a structured dispatcher,
   and `hecate mcp-server` does not scale as more protocol surfaces appear.
4. **Browser UI launch needs a precise name.** `open` is too vague: it could
   mean open a project, workspace, file, or browser. The command should say
   what it opens.

## Goals

- Make bare `hecate` the terminal operator UI.
- Move the long-running runtime process to explicit `hecate serve`.
- Add a clear browser-UI launcher: `hecate ui`.
- Keep protocol surfaces under noun + `serve` (`hecate mcp serve`).
- Reserve root auth verbs (`login`, `logout`, `whoami`) for Hecate/operator
  identity without designing external-agent auth in this RFC.
- Keep command parsing and presentation separate from runtime behavior so the
  command tree can grow without spreading CLI concerns through `internal/`.
- Cross-distribution feature parity: every command works in Hecate Desktop,
  Hecate standalone tarballs, Hecate Docker, and future Homebrew distribution
  where the command makes sense.

## Non-goals

- **No full terminal clone of the web UI.** The first TUI is a local operator
  control panel: runtime status, quick actions, recent projects/chats/tasks,
  connection readiness, and diagnostics. Full chat, provider editing,
  observability waterfall, and patch review stay in the browser UI for now.
- **No TTY auto-detection.** `hecate` always means TUI. `hecate serve` always
  means runtime. Different behavior based on stdin/stdout shape is too
  surprising for SSH, scripts, and service managers.
- **No wholesale rewrite of config loading.** Runtime config stays env-driven
  for `hecate serve`; CLI flags are added only where they are meaningful for
  command UX (`--format`, `--watch`, `--url`, `--start`).
- **Not a plugin system.** No external subcommand discovery, no `hecate-foo`
  PATH lookup.
- **Not a service manager.** Hecate can start its runtime for local UX, but it
  does not replace `systemd`, `launchd`, Docker, or a future updater.

## Proposal

The command surface becomes:

```text
hecate                         # terminal operator UI
hecate serve                   # start the runtime / HTTP API / embedded UI server
hecate ui [flags]              # open local browser UI
hecate status [flags]          # runtime health snapshot
hecate about [flags]           # version + environment/runtime summary
hecate doctor [flags]          # local setup diagnostics
hecate login                   # FUTURE: authenticate Hecate/operator identity
hecate logout                  # FUTURE
hecate whoami [flags]          # FUTURE
hecate mcp serve               # MCP server over stdio
hecate acp serve               # FUTURE: ACP server over stdio (reserved)
hecate migrate <sub>           # per migration-cli.md RFC
hecate version                 # print version; --version / -v aliases
hecate help [topic]            # usage
```

Conceptual split:

```text
hecate       = terminal operator console
hecate ui    = local browser operator console
hecate serve = runtime process
```

### Bare `hecate` — terminal operator UI

Launches the terminal UI. If a runtime is already reachable, the TUI connects
to it. If not, it offers to start a local runtime process and then connects.

Initial TUI scope:

- runtime state: running/stopped, URL, version, storage, data dir
- quick actions: start runtime, open browser UI, copy diagnostics, run doctor
- project / chat / task summary: selected project, recent chats, running tasks,
  awaiting approvals
- connections summary: provider readiness and external-agent readiness/auth
- recent errors/log tail for startup and supervised-agent failures

The TUI may start `hecate serve` as a child for local convenience. When it does,
the TUI owns that child lifecycle and shuts it down on exit unless the operator
chooses to leave it running.

### `hecate serve`

Starts the runtime. This is the old bare `hecate` behavior under an explicit
verb: env-driven config, HTTP API, embedded React UI, OpenAI/Anthropic-compatible
endpoints, task runtime, ACP supervision, telemetry, and graceful shutdown.

`hecate serve` is the command for non-interactive runtime launch sites:

- Docker `CMD`
- systemd / launchd
- Tauri sidecar
- e2e test helpers
- operator scripts that want the foreground runtime process

### `hecate ui [--url URL] [--start]`

Opens the local browser UI in the operator's default browser.

- **`hecate ui`** resolves the runtime URL (default
  `http://127.0.0.1:8765`, override `--url` or `HECATE_BASE_URL`) and launches
  the system browser via `open` (macOS), `xdg-open` (Linux), or `start`
  (Windows). It returns immediately.
- **`hecate ui --start`** starts a local runtime if `/healthz` does not answer,
  waits until healthy, opens the browser UI, then keeps the runtime in the
  foreground. Ctrl+C tears it down.

If `hecate ui` is run with no reachable runtime and no `--start`, it prints:

```text
no Hecate runtime reachable at http://127.0.0.1:8765
  start one in another shell: hecate serve
  or start and open the browser UI: hecate ui --start
```

Exit code 1.

### `hecate status [--format text|json] [--watch] [--url URL]`

A thin HTTP client. Reads `/healthz` and `/hecate/v1/system/stats` from the
configured runtime URL (resolution: `--url` → `HECATE_BASE_URL` →
`http://127.0.0.1:8765`).

Default output:

```text
$ hecate status
Hecate at http://127.0.0.1:8765 — healthy
  Queue:    2 pending, 4 task runners, depth 6 / 1000
  Runs:     3 running, 1 awaiting approval, 12 queued
  Backend:  sqlite (.data/hecate.db)
  Version:  0.1.0-alpha.41
```

`--format json` emits the merged payload for scripting. `--watch` repaints
every N seconds (default 2). `--watch --format json` emits newline-delimited
JSON.

Exit codes: 0 = healthy, 1 = unhealthy / unreachable, 2 = usage error.

### `hecate about [--format text|json]`

Prints version and environment information that is useful in support requests:

- Hecate version and git revision when available
- OS / architecture
- runtime URL resolution
- data dir
- storage backend
- whether a runtime is reachable
- Tauri sidecar marker when running inside the desktop app, if available

`hecate version` stays intentionally small; `about` is the richer diagnostic
summary.

### `hecate doctor`

Runs local diagnostics without mutating configuration:

- checks runtime reachability
- checks data dir writability
- checks configured providers/adapters enough to explain obvious setup issues
- checks common local commands (`ollama`, `lms`, external-agent CLIs) when
  available
- prints actionable repair hints

`doctor` may grow command groups later (`hecate doctor agents`,
`hecate doctor providers`), but the first version can be a single command.

### `hecate login` / `logout` / `whoami`

Reserved for future Hecate/operator identity. They should not be overloaded to
mean "log in to Claude Code", "log in to Grok Build", or other external-agent
auth flows.

External-agent auth belongs in a future namespaced surface, for example:

```text
hecate agent status
hecate agent login claude-code
hecate agent logout grok-build
```

That future surface is out of scope for this RFC.

### Future additive surfaces

The command tree should leave room for read-only operator shortcuts without
making them part of the first implementation:

```text
hecate models [--format text|json]      # routable model inventory
hecate agents [--format text|json]      # supervised-agent readiness
hecate projects [list|show|...]         # project inventory
hecate chat [list|show|...]             # transcript inspection / scripting
```

These are deliberately not in the initial command contract. The browser UI and
TUI remain the primary operator surfaces; root commands should be added only
when they are useful for scripting, diagnostics, or quick terminal inspection.

### `hecate mcp serve`

Renames today's `hecate mcp-server`. Same behavior: stdio JSON-RPC server
bridging MCP clients (Claude Desktop, Cursor, Zed) to a running Hecate runtime
over HTTP. Same env interface (`HECATE_BASE_URL`), same wire protocol, logs and
errors to stderr.

```diff
- args: ["mcp-server"]
+ args: ["mcp", "serve"]
```

Hecate is pre-1.0, so this RFC does not require a compatibility shim for
`mcp-server`.

### `hecate acp serve` — reserved, not implemented

Namespace placeholder for a future ACP server (Hecate as ACP-protocol agent
backend for external clients like Zed). Today Hecate is an ACP **client**
([`internal/agentadapters`](../../../internal/agentadapters/)); the inverse
direction does not yet exist.

Invoking `hecate acp serve` until that RFC lands prints:

```text
hecate acp serve: not implemented yet.
See docs/design/ for proposals.
```

Exit code 2.

### `hecate migrate <sub>`

Per the existing [`migration-cli.md`](migration-cli.md) RFC. This RFC's only
contribution is establishing the command tree and naming convention.

### `hecate version` / `--version` / `-v`

All three print the version string and exit.

### `hecate help [topic]`

Prints top-level usage without arguments and subcommand-specific usage with a
topic (`hecate help mcp serve`, `hecate help status`). Same content is reachable
through `hecate <subcommand> --help`.

## Naming conventions

- **Bare `hecate` is the terminal UI.** Humans get the interactive operator
  entrypoint by default.
- **Runtime launch is explicit.** Use `hecate serve`.
- **Browser UI launch is explicit.** Use `hecate ui`, not `web` (sounds
  internet-hosted) and not `open` (too ambiguous).
- **Protocol surfaces are nouns followed by `serve`.** `mcp serve`, `acp serve`.
- **Verbs are lowercase, single words.** `serve`, `status`, `about`, `doctor`,
  `login`, `logout`, `whoami`, `version`.
- **`--format text|json` is the canonical machine-output switch.** Use it for
  commands with structured output (`status`, `about`, `whoami`). Prefer it over
  a one-off `--json` flag.
- **Exit codes:** 0 = success, 1 = expected failure (unhealthy, unreachable,
  validation failure), 2 = usage error / invalid invocation.

## What changes per distribution channel

| Channel                  | Today                   | After                                                                  |
| ------------------------ | ----------------------- | ---------------------------------------------------------------------- |
| Hecate Desktop (Tauri)   | Sidecar spawns `hecate` | Sidecar spawns `hecate serve`.                                         |
| Hecate standalone        | Operator runs `hecate`  | `hecate` opens the TUI; `hecate ui --start` gives browser-first setup. |
| Hecate Docker            | `CMD ["hecate"]`        | `CMD ["hecate", "serve"]`; `docker exec hecate hecate status` works.   |
| Hecate Homebrew (future) | One binary              | One binary; `hecate` is the local terminal UI.                         |
| MCP clients              | `hecate mcp-server`     | `hecate mcp serve`.                                                    |

This is intentionally breaking while Hecate is alpha. The migration is clear:
non-interactive process launchers use `hecate serve`; humans use `hecate`.

## Implementation sketch

The command tree should be structured as commands plus behavior packages, not
one growing `main.go` switch. Suggested layout:

```text
cmd/hecate/
├── main.go              # calls cli.Execute()
└── cli/
    ├── root.go          # command tree, global help, shared URL resolution
    ├── serve.go         # runtime command
    ├── ui.go            # browser UI command
    ├── status.go
    ├── about.go
    ├── doctor.go
    ├── auth.go          # login/logout/whoami placeholders or future impl
    ├── mcp.go
    ├── migrate.go
    └── version.go

internal/runtimeapp/     # current runtime startup extracted from main
internal/tui/            # terminal operator UI
internal/browseropen/    # cross-platform local browser opener if worth splitting
```

`cmd/hecate/cli` owns command parsing and presentation. Runtime behavior lives
under `internal/` so tests can exercise it without shelling out to the binary.

## Breaking changes and migration

The first Cobra slice is intentionally compatibility-preserving: bare `hecate`
and `hecate mcp-server` still work while internal launch sites and docs move to
the new names. The breaking changes below are the target state for the TUI PR.

Breaking changes:

- Bare `hecate` opens the terminal UI instead of starting the runtime.
- Runtime startup moves to `hecate serve`.
- `hecate mcp-server` is replaced by `hecate mcp serve`.

Required updates:

- Dockerfile / compose / release image: `CMD ["hecate", "serve"]`
- Tauri sidecar spawn: `hecate serve`
- systemd / launchd examples: `ExecStart=/path/to/hecate serve`
- MCP client configs: `args: ["mcp", "serve"]`
- e2e helpers and local scripts that expect a runtime: call `hecate serve`

Release note:

> **Breaking:** `hecate` now opens the terminal operator UI. Use
> `hecate serve` to start the runtime process. MCP server configs should use
> `hecate mcp serve` instead of `hecate mcp-server`.

## Implications for the migration-cli RFC

[`migration-cli.md`](migration-cli.md) is unblocked but unchanged in its core
design. Add a cross-reference near the top:

> Migration commands live under the command tree defined by
> [cli-structure.md](cli-structure.md).

## Open questions

1. **How much should TUI v1 include?** Recommendation: status, quick actions,
   project/chat/task summaries, connection readiness, and diagnostics. Full
   chat and editing stay in the browser UI.
2. **Should `hecate ui --start` keep the runtime foreground?** Recommendation:
   yes. Foreground keeps lifecycle obvious; service managers own daemonization.
3. **Should `hecate serve --ui` also exist?** Recommendation: maybe later as
   convenience, but keep `hecate ui --start` as the primary human path.
4. **Should root auth commands land as stubs or wait?** Recommendation: reserve
   the names in the RFC, but implement only when Hecate/operator auth exists.

## Risks

- **Breaking runtime invocation muscle memory.** Mitigation: release notes,
  docs, and obvious `hecate` TUI copy that says "Use `hecate serve` for the
  runtime process."
- **TUI surface creep.** Mitigation: TUI v1 is a control panel, not a second
  full frontend.
- **Command tree creep.** Mitigation: the RFC's verb inventory is the initial
  contract; new root verbs need their own rationale.
- **Browser opener variance.** `xdg-open` differs across Linux distros. If
  launch fails, print the URL and exit with a clear message.

## Alternatives considered

### Keep bare `hecate` as runtime

Rejected. It preserves scripts but leaves humans with a server process as the
default experience. Hecate is now more than a gateway process; the command name
should open the operator surface.

### `hecate web`

Rejected. The browser UI is local and does not require internet access. `web`
can read like "open the website." `ui` is clearer and more neutral.

### `hecate open`

Rejected. Too vague: future commands may open projects, workspaces, files, or
the browser UI. `hecate ui` says what it opens.

### `hecate serve --open` as the primary browser path

Useful as a possible future convenience, but not the main UX. If the user's
intent is "show me the UI," the command should start with `ui`; starting the
runtime is an implementation detail handled by `--start`.

### TTY auto-detection

Rejected. `hecate` with a TTY versus without a TTY would behave differently,
which is surprising for SSH, shell scripts, background jobs, and service
managers.

### Separate `hecate` and `hecate-cli` binaries

Rejected. Doubles distribution complexity and forces every packaging surface to
coordinate two binaries. One binary with explicit subcommands is enough.
