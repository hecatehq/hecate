# CLI structure for `hecate`

> **Status:** proposed; not implemented.
> **Current source of truth:** [`cmd/hecate/main.go`](../../cmd/hecate/main.go) (current manual flag parse) and [`docs/mcp.md`](../mcp.md) (operator-facing `hecate mcp-server` documentation).
> **Next action:** land a thin subcommand dispatcher and ship `hecate status` + the `mcp serve` rename together; sequence everything else behind it.

## Problem

The `hecate` binary today routes one flag (`--version`/`-v`/`version`) and one ad-hoc subcommand (`mcp-server`), then falls through to "start the runtime." Three pressures push toward a real subcommand structure:

1. **The runtime is one of several surfaces the binary already exposes.** Today: HTTP runtime + MCP server. Future: ACP server, migration commands, status/diagnostic commands. The current "start runtime by default, with one hardcoded `switch`" scales to one subcommand, not five.
2. **[`migration-cli.md`](migration-cli.md) is blocked on this.** Adding `hecate migrate status/apply/snapshot/restore/verify` requires a real subcommand router — without one, we'd grow ad-hoc string switches in `main.go` forever.
3. **Operators have no in-band ergonomics today.** Health and operational status come from `curl /hecate/v1/system/stats | jq`. Opening the web UI means typing `http://127.0.0.1:8765` into a browser by hand. A handful of small subcommands closes that gap.

The current binary's UX is also internally inconsistent: `hecate mcp-server` is kebab-case, lives in a hand-written `switch` in `main.go`, and shares no plumbing with the hypothetical other subcommands.

## Goals

- A small, hand-rolled subcommand dispatcher that scales to ~10 verbs cleanly.
- Consistent naming convention for protocol surfaces (`<surface> serve`).
- Cross-distribution feature parity: every subcommand works in Hecate Desktop, Hecate standalone (tarball), and Hecate Docker.
- Backward-compatible bare invocation: `hecate` with no arguments still starts the runtime, so Docker images, systemd units, the Tauri sidecar, and operator shell habits keep working unchanged.

## Non-goals

- **No TUI.** A terminal UI for Hecate was considered and explicitly dropped. The web UI is the canonical operator surface; SSH-only operators port-forward via `ssh -L` or rely on `hecate status` / `hecate open`. Revisit only if real demand appears.
- **No `cobra`/`urfave/cli`.** Stdlib `flag` + manual subcommand `switch` is enough for the verbs we have. We're not building auto-completion, structured help generation, or subcommand trees deeper than two levels.
- **No wholesale rewrite of config loading.** Config stays env-driven for `hecate` (the runtime); CLI flags are added per-subcommand only where they're operator-meaningful (`--json`, `--watch`, `--url`, `--start`).
- **Not a plugin system.** No external subcommand discovery, no `hecate-foo` PATH lookup.
- **Not a service manager.** We don't compete with `systemd`/`launchd`/`docker run`; the runtime is the foreground process those tools wrap.

## Proposal

A new subcommand surface, dispatched from `cmd/hecate/main.go`:

```text
hecate                    # start the runtime (today's behavior — unchanged)
hecate open [flags]       # open web UI in default browser
hecate mcp serve          # MCP server over stdio (renames today's `hecate mcp-server`)
hecate acp serve          # FUTURE: ACP server over stdio (reserved namespace; not implemented here)
hecate migrate <sub>      # per migration-cli.md RFC
hecate status [flags]     # runtime health snapshot via HTTP
hecate version            # print version; --version / -v aliases
hecate help [topic]       # usage
```

### Bare `hecate` (no args) — unchanged

Starts the runtime. Same env-driven config, same lifecycle (the SIGINT/SIGTERM + `/hecate/v1/system/shutdown` graceful path lands as-is). No flags initially; everything stays env-driven.

This is the most-invoked form, and keeping it stable preserves backward compat for every existing operator script, Docker `CMD`, systemd `ExecStart`, the Tauri sidecar resolver, the e2e Go test helpers, and operator muscle memory.

### `hecate open [--url URL] [--start]`

A small client that opens the web UI in the operator's default browser.

- **`hecate open`** — resolves the runtime URL (default `http://127.0.0.1:8765`, override `--url` or `HECATE_BASE_URL`) and launches the system browser via `open` (macOS), `xdg-open` (Linux), or `start` (Windows). Returns immediately. Useful when a runtime is already running locally, in Docker, or remotely. `HECATE_BASE_URL` is reused (not a new env var) because `hecate mcp serve` already uses it for the same concept.
- **`hecate open --start`** — same, but also starts a local runtime in the foreground if `/healthz` doesn't answer. Polls until healthy, then opens the browser, then keeps the runtime in the foreground. Ctrl+C tears the runtime down. This is the one-shot "I just downloaded the tarball, give me the experience" command for new operators.

If `hecate open` is run with no runtime reachable and no `--start`, it prints:

```text
no Hecate runtime reachable at http://127.0.0.1:8765
  start one in another shell: hecate
  or open this command in start mode: hecate open --start
```

Exit code 1.

### `hecate mcp serve`

Renames today's `hecate mcp-server`. Same behavior — stdio JSON-RPC server bridging MCP clients (Claude Desktop, Cursor, Zed) to a running Hecate runtime over HTTP. Same env interface (`HECATE_BASE_URL`), same wire protocol, same logs. The only change is the invocation:

```diff
- args: ["mcp-server"]
+ args: ["mcp", "serve"]
```

For one release, `hecate mcp-server` aliases to `hecate mcp serve` with a stderr deprecation warning to give in-the-wild Claude Desktop / Cursor / Zed configurations a soft landing.

### `hecate acp serve` — reserved, not implemented

Namespace placeholder for a future ACP server (Hecate as ACP-protocol agent backend for external clients like Zed). Today Hecate is an ACP **client** ([`internal/agentadapters`](../../internal/agentadapters/)); the inverse direction does not yet exist.

This RFC reserves the `hecate acp` namespace and the `serve` verb under it. The implementation design (HTTP vs stdio transport, auth, session scoping, what subset of runtime functionality is reachable) is out of scope; it gets its own RFC when prioritized.

Invoking `hecate acp serve` until that RFC lands prints:

```text
hecate acp serve: not implemented yet.
See https://github.com/hecatehq/hecate/blob/master/docs/rfcs/ for proposals.
```

Exit code 2.

### `hecate migrate <sub>`

Per the existing [`migration-cli.md`](migration-cli.md) RFC. This RFC's only contribution to migrate is establishing the dispatcher and verb conventions; the migrate-specific design is unchanged.

### `hecate status [--json] [--watch] [--url URL]`

A thin HTTP client. Reads `/healthz` + `/hecate/v1/system/stats` from the configured runtime URL (default resolution: `--url` flag → `HECATE_BASE_URL` env → `http://127.0.0.1:8765`). Renders a human-readable digest by default; `--json` emits the raw payload for scripting.

Default output:

```text
$ hecate status
Hecate at http://127.0.0.1:8765 — healthy
  Queue:    2 pending, 4 workers, depth 6 / 1000
  Runs:     3 running, 1 awaiting approval, 12 queued
  Backend:  sqlite (.data/hecate.db)
  Version:  0.1.0-alpha.37
```

With `--watch`, repaints every N seconds (default 2). With `--json`, prints the merged `/healthz` + `/hecate/v1/system/stats` payload and exits. With `--watch --json`, emits one JSON object per interval to stdout (newline-delimited) — composable with `jq`/`grep`/`tee`.

Exit codes: 0 = healthy, 1 = unhealthy (couldn't reach runtime), 2 = usage error.

This single subcommand subsumes the most common operator one-liners today (`curl /healthz | jq`, `curl /hecate/v1/system/stats | jq`).

### `hecate version` / `--version` / `-v`

All three print the version string and exit. Preserves today's behavior so scripts that grep `hecate --version` keep working.

### `hecate help [topic]`

Prints top-level usage when invoked without arguments; subcommand-specific help when given a topic (`hecate help mcp serve`, `hecate help status`). Same content reachable via `hecate <subcommand> --help`.

## Naming conventions

Codified here so we don't bikeshed each new subcommand:

- **Verbs are lowercase, single words.** `serve`, `status`, `migrate`, `version`, `open`. No `start-runtime`, no `mcp_server`.
- **Protocol surfaces are nouns followed by `serve`.** `mcp serve`, `acp serve`. Reserves room for variants under each namespace later (`mcp probe`, `acp serve --http`, etc.).
- **Bare `hecate` runs the runtime.** Not a subcommand. The runtime hosts multiple protocol families (OpenAI-compat, Anthropic-compat, Hecate operator API) in one process; it's the default "what does this binary do" answer.
- **`--flag-with-dashes`, kebab-case for multi-word flags.** Matches Go convention via stdlib `flag`.
- **`--json` is the canonical machine-output flag.** Wherever a subcommand has structured data to emit, `--json` switches from human to machine output.
- **Exit codes:** 0 = success, 1 = expected failure (unhealthy, validation failure, etc.), 2 = usage error / invalid invocation.

## What changes per distribution channel

| Channel                  | Today                      | After                                                                            |
| ------------------------ | -------------------------- | -------------------------------------------------------------------------------- |
| Hecate Desktop (Tauri)   | Sidecar spawns `hecate`    | **Unchanged.** Bare invocation still starts the runtime.                         |
| Hecate standalone        | Operator runs `./hecate`   | **Unchanged.** Plus new convenience: `hecate open --start` for one-shot setup.   |
| Hecate Docker            | `CMD ["hecate"]`           | **Unchanged.** Plus `docker exec hecate hecate status` becomes useful.           |
| Hecate Homebrew (future) | One binary                 | One binary, more verbs.                                                          |
| MCP clients (Zed etc.)   | Launch `hecate mcp-server` | Launch `hecate mcp serve` (one-release alias preserves the old name with a warn) |

No second-binary problem; no spawn-site coordination across multiple repositories; no Tauri `externalBin` adjustment.

## Implementation sketch

`cmd/hecate/main.go` grows a thin dispatcher. Each subcommand lives in its own file:

```text
cmd/hecate/
├── main.go              # dispatcher (~50 LOC)
├── cmd_runtime.go       # current main() body, extracted (bare-invocation entrypoint)
├── cmd_open.go          # new: URL resolution + cross-platform browser launch
├── cmd_mcp.go           # extends current mcp.go: dispatch `mcp <verb>` → mcpServe()
├── cmd_acp.go           # stub: prints "not implemented" for `acp serve`
├── cmd_migrate.go       # per migration-cli.md
├── cmd_status.go        # new: HTTP client to /healthz + /hecate/v1/system/stats
├── cmd_version.go       # extracted from current --version handling
├── cmd_help.go          # new: usage strings, per-subcommand help
└── runtime_state.go     # existing
```

Dispatcher sketch (~50 LOC):

```go
func main() {
    if len(os.Args) < 2 {
        runRuntime(nil) // bare hecate — start the runtime
        return
    }
    switch os.Args[1] {
    case "open":
        runOpen(os.Args[2:])
    case "mcp":
        runMCP(os.Args[2:])
    case "acp":
        runACP(os.Args[2:])
    case "migrate":
        runMigrate(os.Args[2:])
    case "status":
        runStatus(os.Args[2:])
    case "version", "--version", "-v":
        printVersion()
    case "help", "--help", "-h":
        printHelp(os.Args[2:])
    case "mcp-server": // backward-compat alias for one release; logs deprecation
        fmt.Fprintln(os.Stderr, "deprecated: use `hecate mcp serve`")
        runMCP([]string{"serve"})
    default:
        // Treat unknown first-arg as runtime args until proven otherwise.
        // The runtime ignores all CLI args today; preserving this lets
        // operators with malformed scripts hit the runtime path instead
        // of a usage error.
        runRuntime(os.Args[1:])
    }
}
```

Each `cmd_*.go` owns its own flag parsing via stdlib `flag.NewFlagSet`. No shared global flag state.

Shared helpers (config load, runtime-state file resolution) live in `cmd/hecate/` (or `cmd/hecate/internal/` if it grows).

## Breaking changes and migration

One small breaking change:

- **`hecate mcp-server` is deprecated.** Continues to work for one release with a stderr deprecation warning; removed in the release after. Configs in Claude Desktop / Cursor / Zed update from `args: ["mcp-server"]` to `args: ["mcp", "serve"]`.

That's it. Bare `hecate` keeps working. Tauri sidecar, Docker, systemd, e2e tests — none need changes.

Pre-1.0 alpha, so one release note callout is enough:

> **Deprecated:** `hecate mcp-server` is renamed to `hecate mcp serve`. The old name continues to work with a stderr warning in this release; remove it in the next. Update Claude Desktop / Cursor / Zed `mcpServers` configurations.

## Implications for the migration-cli RFC

[`migration-cli.md`](migration-cli.md) is unblocked but unchanged in its core design. A small alignment patch lands alongside this RFC:

1. Cross-reference at the top of `migration-cli.md` ("Lives under the umbrella defined by [cli-structure.md](cli-structure.md).").

No other changes to `migration-cli.md`.

## Open questions

1. **`hecate mcp-server` backward-compat alias: one release or none?** A soft-deprecation release is kinder to in-the-wild configs but adds a few lines of dispatcher code. Voting recommendation: yes, one release, then remove.
2. **Should `hecate status` poll once or stream by default?** Single-shot keeps it composable with shell pipelines; `--watch` opts into streaming. Voting recommendation: single-shot.
3. **Should `hecate open --start` daemonize, or stay foreground?** Foreground (the current proposal) makes Ctrl+C tear down cleanly and matches `docker compose up` semantics. Daemonizing complicates the lifecycle for marginal convenience. Voting recommendation: foreground only; operators who want a daemonized runtime use bare `hecate &` or a service manager.
4. **Where should `hecate help` text live — inline strings or embedded markdown?** Inline strings (simpler, ships in one file). Markdown gets us nicer rendering with `glow` etc. but adds a dep. Voting recommendation: inline.

## Risks

- **Surface creep.** Once the dispatcher exists, every minor diagnostic tempts a new subcommand. Mitigation: this RFC's verb inventory is the v1 contract; any new verb gets its own RFC or a documented justification.
- **`hecate open` is browser-dependent on Linux.** `xdg-open` is the convention but distros vary. Falls back to printing the URL if launch fails. Documented in `--help`.
- **`hecate status` may be confused with `hecate help`/`hecate version` as "the inert command."** It hits the network, can time out, can fail. The 1-vs-2 exit-code split documented above lets scripts distinguish.

## Alternatives considered

### TUI as bare `hecate`

Considered in an earlier draft of this RFC. Dropped because:

- Bare `hecate` becoming a TUI is a breaking change that ripples through Docker `CMD`, systemd `ExecStart`, the Tauri sidecar spawn, e2e test helpers, and every operator shell habit.
- The web UI is the canonical operator surface; a TUI would be a second surface to maintain.
- SSH-only operators have working alternatives (`ssh -L` port-forward, `hecate status`, `hecate open --url`).

Revisit only if real demand for a TUI appears.

### `hecate up` as a separate "runtime + open browser" verb

Considered when the TUI version was on the table. Without TUI in the picture, `hecate open --start` covers the same use case under a more descriptive verb that ALSO handles the "runtime already running" case. `up` is not added.

### Use `cobra` for the dispatcher

Standard Go CLI library, more ergonomic for large command surfaces. Rejected: ~10 transitive deps for ~50 LOC of dispatch logic. Stdlib `flag` is enough for our verb count.

### Single binary that auto-detects mode by TTY

`hecate` with a TTY → something interactive; without TTY → runtime. Rejected: surprising semantics, breaks `ssh server hecate` and `hecate &` in opposite ways, hard to document.

### Ship `hecate` and `hecate-cli` as separate binaries

Common pattern (one for the daemon, one for the CLI client). Rejected: doubles distribution complexity, requires a second bundle in Tauri, and we already have everything we need in one binary.
