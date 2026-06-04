# Sandbox

Hecate executes workspace-bound tool calls through shared seams before
they touch the host. File and search tools resolve paths through
WorkspaceFS. Shell commands use ProcessRunner and the sandbox executor.
Hecate-owned Git helpers use GitRunner where they do not need the broad
`git_exec` shell-shaped interface. Every bounded subprocess still goes
through the same safety model: validate the task's policy, sanitise the
environment, cap output and wall-clock time, optionally wrap with an
OS-level confinement tool (`bwrap` / `sandbox-exec`), then start the
child process. A misbehaving command runs in its own process and cannot
crash the gateway.

There is no separate sandbox daemon. The safety properties below are
applied per-call inside the gateway process.

Code: [`internal/sandbox/`](../../internal/sandbox/) · policy reference: [`agent-runtime.md#network-egress-for-shell_exec--git_exec`](agent-runtime.md#network-egress-for-shell_exec--git_exec).

> Contributing here? Start at [`AGENTS.md`](../../AGENTS.md) and [`docs-ai/README.md`](../../docs-ai/README.md).

## Contents

- [How it works](#how-it-works)
- [Relationship to WorkspaceFS and runners](#relationship-to-workspacefs-and-runners)
- [Pre-execution policy validation](#pre-execution-policy-validation)
- [Isolation layers](#isolation-layers)
- [Environment variables](#environment-variables)
- [Limitations](#limitations)

## How it works

For every Hecate-owned workspace tool call that can touch the filesystem
or start a subprocess, the gateway:

1. **Validates the task's policy.** Allowed-root path containment,
   read-only mode, network gate, host allowlist, private-IP block.
   Best-effort static parsing of the command. Violations return a
   `PolicyError` and the command never runs.
2. **Dispatches through the appropriate seam.** File/search/write tools
   resolve workspace paths through WorkspaceFS. Shell commands build
   `sh -lc <command>` (or `rtk sh -lc <command>` when that specific
   Hecate Chat has compact command output enabled), the sandbox executor
   wraps that argv with bwrap / sandbox-exec when available, then
   ProcessRunner starts the child process — see
   [Layer 2](#layer-2--os-level-isolation). Hecate-owned Git helper calls
   build a GitRunner request rather than shelling out ad hoc; broad
   `git_exec` still goes through the sandbox command executor.
3. **Sanitises subprocess environments** — explicit allowlist of
   variables shell and Git commands will see; gateway secrets stay out
   of scope.
4. **Spawns the subprocess** under the task's wall-clock timeout and
   reads stdout / stderr through bounded pipes. Combined output is
   capped (`HECATE_TASK_MAX_OUTPUT_BYTES`); over-cap commands are
   killed and surface an `OutputLimitExceededError`.
5. **Returns** stdout, stderr, and exit code as a structured `Result`.
   The agent runtime turns that into a tool-result message for the
   LLM.

No daemon. No JSON-RPC envelope. No binary-resolution dance. The child
process is the process boundary, with WorkspaceFS / ProcessRunner /
GitRunner keeping workspace-bound behavior on the shared path.

## Relationship to WorkspaceFS and runners

The sandbox executor is the policy and isolation layer; it is not the
only workspace abstraction.

- **WorkspaceFS** (`internal/workspacefs`) resolves Hecate-mediated
  file, search, and write paths and rejects workspace escapes before
  filesystem IO.
- **ProcessRunner** (`internal/processrunner`) is the bounded process
  seam for command-style subprocesses. It owns cwd, environment,
  timeout, streaming output, and output caps.
- **GitRunner** (`internal/gitrunner`) is the Git-specific seam for
  helper-style operations. It validates the workspace, runs Git with a
  sanitised environment, and routes Git through ProcessRunner instead of
  direct ad hoc subprocesses. The broad `git_exec` tool remains on the
  sandbox command executor because it accepts a shell-shaped Git
  subcommand string.

External-agent subprocess internals are different: Codex, Claude Code,
Cursor Agent, Grok Build, and similar adapters may run their own file,
shell, and Git operations inside the selected workspace. Hecate applies
WorkspaceFS / ProcessRunner / GitRunner to Hecate-mediated calls and ACP
callbacks, not to arbitrary internal behavior of a trusted external
agent.

## Pre-execution policy validation

These checks run before any subprocess is spawned. A failing check
turns into a `PolicyError` returned to the caller; nothing is
executed.

| Control              | Field                                 | Effect                                                                                                                         |
| -------------------- | ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| **Allowed root**     | `sandbox_allowed_root` on the task    | File and path arguments validated to stay under this directory; `..` traversal rejected                                        |
| **Read-only**        | `sandbox_read_only` on the task       | Blocks write operations (`file_write`, shell output redirection, mutating git commands)                                        |
| **Network gate**     | `sandbox_network` on the task         | `false` (default) blocks commands that look like network access; `true` allows egress subject to the host/IP constraints below |
| **Host allowlist**   | `HECATE_TASK_SHELL_ALLOWED_HOSTS`     | Restricts HTTP/S URLs in commands to exact hostnames                                                                           |
| **Private IP block** | `HECATE_TASK_SHELL_ALLOW_PRIVATE_IPS` | Blocks IP literals in RFC1918 / loopback / link-local ranges                                                                   |

Network enforcement is **best-effort static string matching** on the
command text before execution. A sufficiently creative command
(inline Python, base64 encoding, raw sockets via `nc`) can bypass it.
Layer 2 below upgrades this to kernel-enforced network denial when
the platform supports it.

## Isolation layers

The safety properties below are organised from automatic to opt-in.
Approval gates (per-task `shell_exec` / `git_exec` / `file_write`
classes) sit upstream of all of these and are the primary safety
story — the layers are belt-and-suspenders behind the operator's
approval policy. See
[`agent-runtime.md#approval-gating`](agent-runtime.md#approval-gating).

### Layer 0 — Subprocess boundary

Every shell command and Hecate-owned Git helper runs as a child process
spawned by ProcessRunner. Broad `git_exec` also gets its own child
process through the sandbox command executor. A misbehaving or panicking
command can't crash the gateway: when the child exits, the kernel
reclaims its file descriptors, virtual memory, and any descendants. This
is the basic `os/exec` boundary; it's named here only because it is part
of the safety story.

What this layer is **not**: a chroot, a container, or a VM. The
subprocess runs as the same OS user as the gateway and inherits the
host's filesystem visibility (subject to the policy validation above
and Layer 2 wrapping below).

### Layer 1 — Defensive hardening

Cross-platform per-call mitigations applied by the gateway before
spawning the shell. Both are always-on; the cap is configurable.

- **Environment sanitisation** — the shell receives a curated
  allowlist (`PATH`, `HOME`, `TMPDIR`, `LANG`, `TZ`, `GIT_*`, and a
  handful of others) instead of inheriting the gateway's full env.
  Prevents shell tools from reading `OPENAI_API_KEY`, `DATABASE_URL`,
  and other gateway secrets. This is the layer that exists _because_
  Hecate is a server: CLI agents (Claude Code, Codex CLI) deliberately
  inherit the user's environment because that's what the user wants.
  A long-running gateway must not.
- **Output size cap + wall-clock timeout** —
  `HECATE_TASK_MAX_OUTPUT_BYTES` (default 4 MiB) bounds the combined
  stdout + stderr a command can emit; the task's `Timeout` field
  bounds wall-clock. Both kill the subprocess on breach. Together they
  are the per-call budget — runaway commands can't exhaust gateway
  memory or hold a worker indefinitely.
- **Optional RTK compaction** — Hecate Chat can enable compact command
  output per chat. The setting is off by default. If `rtk` is installed
  in the gateway process `PATH`, system stats report `rtk_available`
  and the UI offers an opt-in onboarding hint. When enabled, shell/git
  tools launch as `rtk sh -lc <command>` after policy validation and
  before process start. This is an output-shaping hook, not a policy
  bypass: the original command string is still what the sandbox
  validates, and the process still receives the sanitised environment,
  timeout, output cap, and Layer 2 wrapper. Step telemetry records both
  the pre-wrapper command (`hecate.sandbox.rtk.command.before`) and the
  wrapped command (`hecate.sandbox.rtk.command.after`) so operators can
  audit what RTK changed.

CPU / file-descriptor / address-space caps are _not_ applied
per-call. `RLIMIT_*` values set via `setrlimit` modify the calling
process's limits, and the gateway is long-running — using them per
call would progressively shrink the gateway itself. Operators who
want hard CPU / FD / memory caps should run the gateway under
systemd with the appropriate `CPUQuota=` / `LimitNOFILE=` /
`MemoryMax=` directives, or inside a container with the equivalent
`docker run --cpus= --memory=` flags. Those caps apply to the gateway
and every subprocess it spawns, which is what an operator actually
wants.

### Layer 2 — OS-level isolation

Where the platform ships a sandbox wrapper, the gateway uses it
unconditionally. The choice is made once at startup, logged, and
never reconfigured at runtime — there is no opt-out env var. If the
deployment's platform doesn't have a usable wrapper, the gateway
runs with Layer 0+1 only and surfaces that on `/healthz` so
operators can see what they got.

| Platform                  | Wrapper              | When used                                                                                                          | What it enforces                                                                                                                                                                                                           |
| ------------------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Linux**                 | `bwrap` (bubblewrap) | Always when `/usr/bin/bwrap` is present and a probe call succeeds (catches the unprivileged-userns-disabled case). | Read-only root filesystem, read-write workspace bind-mount, separate network namespace (`--unshare-net`) when the task disallows network. Filesystem-confined and network-denied at the kernel level, not by string match. |
| **macOS**                 | `sandbox-exec`       | Always (binary ships on every supported macOS).                                                                    | Seatbelt SBPL profile: file writes confined to the workspace; network denied when the task disallows it.                                                                                                                   |
| **Linux without `bwrap`** | none                 | When `/usr/bin/bwrap` is absent or the probe fails.                                                                | No additional confinement beyond Layer 0+1.                                                                                                                                                                                |
| **Windows**               | none                 | Always (no equivalent without elevated privileges and Windows Filtering Platform).                                 | No additional confinement beyond Layer 0+1.                                                                                                                                                                                |

The wrapper is auto-detected once at gateway startup. The decision
is reported on `/healthz` under `sandbox.os_isolation` (`bwrap` /
`sandbox-exec` / `none`) and logged at info level on the first call.
This is the same shape Claude Code and Codex CLI use, with one
difference: it's automatic for Hecate (server context — no user
sitting at a TTY to decide), and configured per-call in the local-CLI
case.

The official Linux Docker image is distroless and ships without
`bwrap` or `sh`, so shell-tool calls inside the published image
return an executor error and Layer 2 is unavailable. Operators who
need shell tools should run the gateway directly on a Linux host
(`apt-get install bubblewrap` to activate Layer 2) or roll a custom
image based on `debian-slim` / `ubuntu` that adds `bubblewrap` and a
POSIX shell. The gateway logs whichever wrapper is active at startup
and surfaces the same on `/healthz` so operators can confirm what
they got.

## Environment variables

| Env var                               | Default           | What it controls                                                                                             |
| ------------------------------------- | ----------------- | ------------------------------------------------------------------------------------------------------------ |
| `HECATE_TASK_MAX_OUTPUT_BYTES`        | `4194304` (4 MiB) | Combined stdout + stderr cap per command. Commands exceeding this are killed and return an error. 0 = no cap |
| `HECATE_TASK_SHELL_ALLOW_PRIVATE_IPS` | `false`           | Allow loopback / RFC1918 / link-local IP literals in shell and git command URLs when `sandbox_network=true`  |
| `HECATE_TASK_SHELL_ALLOWED_HOSTS`     | `""`              | Comma-separated exact-host allowlist for URLs in shell and git commands; empty = all public hosts            |

The `http_request` tool has its own parallel pair (`HECATE_TASK_HTTP_*`) — see
[`agent-runtime.md`](agent-runtime.md#configuration-knobs).

## Limitations

- **Not a container.** The subprocess (and its bwrap / sandbox-exec
  wrapping when active) runs as the same OS user as the gateway and
  shares the host's UID/GID, hostname, PID namespace beyond
  `--unshare-pid` (which we don't enable — it breaks job control),
  and any non-restricted resources. For stronger isolation, run the
  gateway inside a container — this is a deployment-time decision,
  not a sandbox-layer property. seccomp-bpf syscall filtering is
  also not implemented; tracked at
  [known-limitations.md](../operator/known-limitations.md#task-runtime-and-sandbox).
- **Linux without `bwrap` runs unwrapped.** Layer 0+1 still apply,
  but filesystem and network confinement reduce to the best-effort
  string match in pre-execution validation. Install `bubblewrap` to
  upgrade. The gateway tells you in the logs and on `/healthz`.
- **Windows runs unwrapped.** Job Objects could provide some of what
  bwrap does (kill-on-close, memory cap, process count) but require
  elevated privileges the gateway doesn't hold by default. Filesystem
  and network isolation on Windows would need WFP integration.
  Tracked separately.
- **Best-effort policy parsing.** The pre-execution checks are static
  string matching on the command text. A clever command (inline
  Python, `nc` raw sockets, base64-encoded URLs) can bypass them.
  Layer 2 (when active) catches the network half at the kernel; for
  the filesystem half there's the workspace bind-mount.
- **No pooling.** Each tool call spawns a fresh subprocess. The cost
  is bounded (~5–10 ms) and dominated by LLM round-trips; pre-warmed
  worker pools haven't earned their keep.
