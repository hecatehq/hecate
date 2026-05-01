# Sandbox

Hecate executes shell commands, git operations, and file writes inside an
out-of-process executor called `sandboxd`. Every `shell_exec`, `git_exec`, and
`file_write` tool call crosses an exec boundary into a fresh `sandboxd worker`
subprocess. A buggy or misbehaving command crashes the worker, not the gateway.

Code: [`internal/sandbox/`](../internal/sandbox/) · binary: [`cmd/sandboxd/`](../cmd/sandboxd/) · policy reference: [`agent-runtime.md#network-egress-for-shell_exec--git_exec`](agent-runtime.md#network-egress-for-shell_exec--git_exec).

> Contributing here? Start at [`AGENTS.md`](../AGENTS.md) and [`ai/README.md`](../ai/README.md).

## Contents

- [How it works](#how-it-works)
- [Binary resolution](#binary-resolution)
- [Deployment scenarios](#deployment-scenarios)
- [Environment variables](#environment-variables)
- [Policy controls](#policy-controls)
- [Isolation layers](#isolation-layers)
- [Limitations](#limitations)

## How it works

For every `shell_exec`, `git_exec`, or `file_write` tool call the gateway:

1. Serialises the request (command, working directory, policy) to JSON.
2. Spawns `sandboxd worker` as a subprocess with the JSON on stdin.
3. Reads a stream of newline-delimited JSON events from stdout — output chunks
   arrive in real time; a final `result` event carries the exit code.
4. The worker enforces the task's policy before executing anything. A policy
   violation returns a `PolicyError` — the command never runs.

Communication is over a pipe pair. No network socket, no shared memory.

## Binary resolution

The gateway resolves the `sandboxd` binary at first use and caches the result
for the process lifetime. Resolution order — first hit wins:

| Step | Mechanism | When to use |
|---|---|---|
| 1 | `SANDBOXD_BIN` env var | Explicit operator override; path must exist |
| 2 | Next to `os.Executable()` | Bundled app (Tauri desktop, hand-built tarballs) |
| 3 | `$PATH` lookup | Developer machines after `make install` |
| 4 | `go build` from source | Dev / CI only — requires `go` on `$PATH` and repo source |

**Step 4 only works on the machine where Hecate was compiled.** The Go runtime
bakes source file paths into the binary at build time; those paths don't exist
on end-user machines. If `go` is not on `$PATH` and no binary is found via
steps 1–3, the gateway returns a clear error naming the three remediation
options rather than the opaque `executable file not found in $PATH` that
earlier releases produced.

### Triple-suffixed lookup (step 2)

Tauri's `externalBin` bundler copies sidecar binaries with a Rust target-triple
suffix (e.g. `sandboxd-aarch64-apple-darwin`). The gateway probes for the
suffixed name first, then falls back to plain `sandboxd` / `sandboxd.exe`. Both
sit in the same directory as the `hecate` executable.

## Deployment scenarios

### Docker / bare binary

`sandboxd` must be on `$PATH` or co-located with `hecate`. The official Docker
image and release tarballs include it. If you build from source, `make build`
produces `hecate`; you must also build `sandboxd`:

```sh
go build -o sandboxd ./cmd/sandboxd
```

Or set `SANDBOXD_BIN` to point at a pre-built path:

```env
SANDBOXD_BIN=/usr/local/bin/sandboxd
```

### Tauri desktop app

`make tauri-sidecar` builds both `hecate` and `sandboxd` and stages them in
`tauri/src-tauri/binaries/` with the correct triple suffix. Tauri's bundler
then includes both in the distributed `.dmg` / `.deb` / `.msi` / `.AppImage`.
No additional configuration is required — step 2 of binary resolution finds
`sandboxd` automatically next to the running `hecate` executable.

### CI / testing

`go test ./...` uses the `go build` fallback (step 4) to compile sandboxd into
a temp cache on first run. Set `SANDBOXD_BIN` to a pre-built path to skip the
build step and speed up CI:

```env
SANDBOXD_BIN=/path/to/pre-built/sandboxd
```

## Environment variables

| Env var | Default | What it controls |
|---|---|---|
| `SANDBOXD_BIN` | `""` | Explicit path to the sandboxd binary; bypasses all other resolution |
| `GATEWAY_TASK_MAX_OUTPUT_BYTES` | `4194304` (4 MiB) | Combined stdout + stderr cap per command. Commands exceeding this are killed and return an error. 0 = no cap |
| `GATEWAY_SANDBOX_RLIMIT_CPU` | `300` | CPU time cap in seconds (`RLIMIT_CPU`). Complements the wall-clock task timeout. 0 = no cap |
| `GATEWAY_SANDBOX_RLIMIT_NOFILE` | `1024` | Open file descriptor cap (`RLIMIT_NOFILE`). 0 = no cap |
| `GATEWAY_SANDBOX_RLIMIT_AS` | `0` | Virtual address space cap in bytes (`RLIMIT_AS`). 0 = no cap (default off; useful on Linux) |
| `GATEWAY_TASK_SHELL_ALLOW_PRIVATE_IPS` | `false` | Allow loopback / RFC1918 / link-local IP literals in shell and git command URLs when `sandbox_network=true` |
| `GATEWAY_TASK_SHELL_ALLOWED_HOSTS` | `""` | Comma-separated exact-host allowlist for URLs in shell and git commands; empty = all public hosts |

The `http_request` tool has its own parallel pair (`GATEWAY_TASK_HTTP_*`) — see
[`agent-runtime.md`](agent-runtime.md#configuration-knobs).

## Policy controls

Enforced inside the worker before the command runs:

| Control | Field | Effect |
|---|---|---|
| **Allowed root** | `sandbox_allowed_root` on the task | File and path arguments validated to stay under this directory; `..` traversal rejected |
| **Read-only** | `sandbox_read_only` on the task | Blocks write operations (`file_write`, shell output redirection, mutating git commands) |
| **Network gate** | `sandbox_network` on the task | `false` (default) blocks commands that look like network access; `true` allows egress subject to the host/IP constraints below |
| **Host allowlist** | `GATEWAY_TASK_SHELL_ALLOWED_HOSTS` | Restricts HTTP/S URLs in commands to exact hostnames |
| **Private IP block** | `GATEWAY_TASK_SHELL_ALLOW_PRIVATE_IPS` | Blocks IP literals in RFC1918 / loopback / link-local ranges |

Network enforcement is **best-effort static string matching** on the command
text before execution. A sufficiently creative command (inline Python, base64
encoding, raw sockets via `nc`) can bypass it. For hard egress control, run the
gateway in a network namespace or behind a filtering egress proxy.

## Isolation layers

The process boundary that `sandboxd` provides is one layer in a spectrum of
isolation mechanisms. The layers below are ordered from weakest to strongest
and from most to least cross-platform. Layers 0 and 1 apply to every
deployment today; Layers 2 and 3 are planned.

```
weakest ─────────────────────────────────────────── strongest

[Layer 0]    [Layer 1]    [Layer 2]         [Layer 3]
 process      defensive    OS-level          Wasm /
 boundary     hardening    isolation         VM
 (current)    (current)    (planned)         (planned)
```

Layers 0 and 1 are active on every deployment today. Layers 2 and 3 are planned.

### Layer 0 — Process boundary (current)

`sandboxd worker` runs as a separate OS process. A misbehaving or panicking
tool call crashes the worker, not the gateway. Policy violations (network
denial, read-only enforcement, path escape) return a `PolicyError` before the
command runs. Network and write detection is **best-effort static string
matching** — creative commands (inline Python, base64-encoded URLs, `nc`) can
bypass it.

### Layer 1 — Defensive hardening (current)

Cross-platform improvements that require no kernel features and work on Linux,
macOS, and Windows. All three are shipped and active by default.

- **Environment sanitisation** — `newWorkerCommand` passes an explicit allowlist
  (`PATH`, `HOME`, `TMPDIR`, `LANG`, `TZ`, `GIT_*`, and a handful of others) to
  the worker subprocess instead of inheriting the gateway's full environment.
  Prevents the worker and any shell command it spawns from reading
  `OPENAI_API_KEY`, `POSTGRES_DSN`, and other secrets present in the gateway
  process. See `workerEnv()` in `internal/sandbox/worker.go`.
- **Output size cap** — `GATEWAY_TASK_MAX_OUTPUT_BYTES` (default 4 MiB) bounds
  the combined stdout + stderr a command can emit. When the cap is reached the
  command is killed and `OutputLimitExceededError` is returned. Prevents runaway
  commands from exhausting gateway memory. Enforced in `drainProcessOutput`
  inside the sandboxd worker.
- **Resource limits** — `GATEWAY_SANDBOX_RLIMIT_CPU` (default 300 s),
  `GATEWAY_SANDBOX_RLIMIT_NOFILE` (default 1024), and
  `GATEWAY_SANDBOX_RLIMIT_AS` (default 0 = off) are applied via
  `syscall.Setrlimit` on Linux and macOS before the shell subprocess starts.
  The child process inherits the limits. On Windows this is a no-op; Job Object
  support is tracked as part of Layer 2. See `applyProcessResourceLimits` in
  `internal/sandbox/exec_rlimit_{linux,darwin,windows,other}.go`.

### Layer 2 — OS-level isolation (planned)

Platform-adaptive isolation behind a unified `PlatformSandbox` interface with
build-tag implementations. Applied to the `sandboxd worker` process before
spawn — no changes to the worker itself.

| Platform | Mechanism | What it enforces |
|---|---|---|
| Linux | `SysProcAttr.Cloneflags` (namespaces) | `CLONE_NEWNET` — no network interface; `CLONE_NEWPID` — private PID tree; `CLONE_NEWUSER` — no root required |
| macOS | `sandbox-exec` Seatbelt profile | Kernel-enforced network denial, filesystem restrictions; no CGO, system binary always present |
| Windows | Job Objects | Kill-on-close, memory limit, process count — network isolation not achievable without WFP |

Linux namespaces turn the network policy gate from string matching into a
kernel guarantee: `curl`, Python `urllib`, Node `fetch` all fail at the socket
layer because the process has no network interface.

### Layer 3 — WebAssembly / wazero (planned)

A structured tool execution model where tools are compiled to Wasm modules
rather than arbitrary shell commands. The
[wazero](https://github.com/tetratelabs/wazero) runtime (pure Go, no CGO) runs
modules with explicit WASI capability grants — the module receives exactly the
filesystem paths and environment variables you grant, nothing else.

**What this enables:** real cross-platform capability-based isolation for
well-defined tools (formatters, linters, git helpers compiled to WASI).

**What it does not solve:** `shell_exec` with arbitrary bash commands. The
shell itself would need to be a WASI-compiled binary, which is experimental.
This layer is better understood as an architectural path toward a typed tool
plugin model rather than a drop-in replacement for the current shell executor.

## Limitations

- **Process boundary only.** `sandboxd` is not a container, chroot, or VM. The
  subprocess runs as the same OS user as the gateway and can access anything
  that user can access outside the workspace root. Stronger OS-level isolation
  (Linux namespaces, seccomp-bpf) is planned — see
  [known-limitations.md](known-limitations.md#task-runtime-and-sandbox).
- **Memory backend does not persist across restarts.** The binary resolution
  cache resets on gateway restart; step 4 rebuilds the binary on next use.
- **No pooling.** A fresh subprocess is spawned per operation. Pre-warmed
  worker pools are future work.
