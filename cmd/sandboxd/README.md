# sandboxd

Out-of-process sandbox executor for Hecate's task runtime.

The gateway runner invokes `sandboxd worker` via `exec.Command` for each shell, file, or git task. The subprocess reads a JSON-encoded request from stdin, executes the operation under the configured policy controls (allowed root, read-only flag, timeout, network denial), and writes a JSON-encoded result to stdout. Communication is over the parent's pipe pair — no network socket, no shared memory. A buggy or misbehaving tool crashes the subprocess, not the gateway process.

## Usage

```
sandboxd worker
```

`worker` is the only sub-command. It is not intended to be invoked directly by operators; the gateway runner manages the lifecycle. The binary must be on `$PATH` or co-located with the `hecate` binary so the runner can find it at startup.

## Policy controls

Enforced by `internal/sandbox` at the worker level:

- **Allowed root** — file and path arguments are validated to stay under the task's workspace root. Paths that escape via `..` traversal are rejected.
- **Read-only mode** — when enabled, write operations (`file_write`, `shell_exec` with output redirection) are blocked before execution.
- **Timeout** — each operation runs under a per-task deadline; the worker kills the child process and returns a timeout error if exceeded.
- **Network denial** — `sandbox_network=false` (default) blocks outbound network access for `shell_exec` and `git_exec` at the policy layer. Best-effort static URL parsing; not a kernel-level firewall.

## Isolation note

`sandboxd` provides a **process boundary**, not OS-level isolation. It is not a container, chroot, seccomp filter, or VM. A sufficiently privileged or creative command can still affect the host filesystem outside the workspace root if the OS user running the gateway has the access. Stronger isolation (namespaces, seccomp, gVisor) is future work. See [docs/known-limitations.md](../../docs/known-limitations.md#task-runtime-and-sandbox).
