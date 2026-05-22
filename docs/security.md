# Security

Hecate is local-first, single-operator software. This page documents the security model that exists today, what Hecate tries to protect, and what remains the operator's responsibility.

## Threat model

Hecate assumes the operator trusts their own machine, local user account, and selected workspaces.

- The gateway binds to `127.0.0.1:8765` by default.
- Browser requests are same-origin checked, but same-origin is not a network security boundary.
- Hecate is not designed to be exposed directly on a network.
- If you bind Hecate to anything other than loopback, put your own firewall, reverse proxy, or access-control layer in front.
- Do not run Hecate on a shared host where untrusted local users can access the gateway port or data directory.

## Runtime boundaries

Hecate has two different execution surfaces with different trust levels.

| Surface                                               | Boundary                                                                                                                                                                                                                                        |
| ----------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hecate Chat with tools on / native `agent_loop` tasks | Hecate owns the task loop. Tool calls run as per-call subprocesses with env sanitisation, output caps, timeouts, policy checks, approvals, and `bwrap` / `sandbox-exec` wrappers where available. This is not a VM or container boundary.       |
| External Agent adapters                               | Codex, Claude Code, Cursor Agent, Grok Build, and similar adapters run as trusted local subprocesses in the selected workspace. Hecate supervises lifecycle, approvals, diagnostics, and Git diffs, but it does not sandbox the adapter's internal runtime. |

If you need a hard isolation boundary, run Hecate and its workspaces inside a VM, container, or dedicated OS user that you are comfortable letting tools modify.

## Workspaces

Hecate supports isolated generated workspaces and opt-in in-place workspaces.

- Isolated native task runs create workspaces under a Hecate-managed root from validated task/run IDs.
- In isolated mode, when the task source is a Git repository, Hecate clones it with `git clone --no-hardlinks -- <source> <workspace>`.
- In isolated mode, when the task source is a plain directory, Hecate copies it into the generated workspace.
- Generated workspace paths reject traversal and existing symlink components before Hecate creates files.
- Operator-selected source directories are canonicalized. Symlinked source repos/folders are allowed by design, because operators may intentionally select them.
- `workspace_mode=in_place` skips clone/copy and runs tools directly in the selected source directory. Treat it as destructive.
- Adding or selecting a workspace in the UI does not clone it. Clone/copy happens only when a native task run provisions an isolated workspace.

Git is used for workspace setup and change review, not as a security boundary. Hecate also uses Git status/diff information to show branch state, changed files, and revertable workspace changes.

## Approvals

Approvals are safety gates, not a sandbox.

- Native task approvals block the run until the operator approves or rejects.
- External-agent approvals are prompt-first by default when the adapter asks for permission.
- Durable external-agent grants can be reviewed and revoked from Connections.
- Auto-approval modes are dangerous for interactive use because they let tool requests proceed without operator review.

Review broad grants carefully, especially workspace-wide or adapter-wide grants for file writes, shell commands, Git commands, and network access.

## Secrets and local state

Hecate stores local configuration and operational state on disk.

- Provider credentials and settings are local to the gateway data directory / desktop app data directory.
- Do not commit `.env`, SQLite databases, release keys, update signing keys, or platform credential files.
- External agent credentials belong to the underlying CLI account. Hecate can probe and surface auth failures, but it does not own, proxy, or pool those accounts. See [External agent adapters](external-agent-adapters.md#credential-and-account-boundaries) for credential and billing notes for Codex, Claude Code, Cursor Agent, and Grok Build.
- If you expose Hecate beyond loopback while provider credentials are configured, anyone who can reach the gateway may be able to spend those credentials.

### Bootstrap key today

Persisted provider and MCP literal credentials are encrypted
with a gateway-local AES-GCM control-plane key. Hecate resolves that key at
startup:

1. If `HECATE_CONTROL_PLANE_SECRET_KEY` is set, Hecate validates that
   base64-encoded 32-byte key, uses it for this run, and persists it to the
   bootstrap file.
2. Otherwise Hecate loads `hecate.bootstrap.json` from the data directory, or
   from `HECATE_BOOTSTRAP_FILE` when that path is set.
3. If no bootstrap file exists, Hecate generates a new key and writes the file.

The file-backed bootstrap path is intentionally local and boring:

- An env-provided key is not env-only storage. On startup it overwrites the
  bootstrap file at the resolved path, and that path must be writable.
- POSIX platforms create the bootstrap file with `0600` permissions and repair
  broader group/world modes on startup. Stricter owner-only modes such as
  `0400` are accepted.
- Windows uses Go's cross-platform file-mode APIs for the file-backed path, but
  those APIs do not rewrite existing DACLs. Treat the OS account and data
  directory ACL as part of the local operator boundary on Windows.
- Docker and headless installs keep using the file-backed path by default. If
  you mount the data directory or `HECATE_BOOTSTRAP_FILE` separately, keep the
  host-side permissions private to the operator or service account.

If Hecate cannot validate or secure the bootstrap source, startup fails closed.
The desktop app startup screen and `gateway.log` include the affected path or
environment override; fix ownership, ACLs, POSIX mode bits, or unset an invalid
`HECATE_CONTROL_PLANE_SECRET_KEY` override before restarting.

This protects against accidental disclosure from the settings database alone,
but it is not a vault boundary. A process running as the same OS user that can
read both the database and bootstrap key can decrypt stored credentials.

Back up the settings database and bootstrap key together when you want stored
credentials to survive a restore. If the bootstrap key is deleted, lost, or
changed while keeping the old database, existing encrypted credentials cannot be
decrypted. Changing `HECATE_CONTROL_PLANE_SECRET_KEY` has the same effect
unless encrypted rows are rekeyed at the same time; today the recovery path is
to restore the old key or re-enter the provider and MCP credentials.

### Key storage roadmap

The file-backed key is good enough for the local operator console today, but
desktop builds should eventually prefer OS-backed storage:

- macOS: store the bootstrap key in Keychain, scoped to the signed Hecate app
  or current user, with the file-backed path kept for explicit overrides and
  non-desktop launches.
- Windows: store the key in Credential Manager or a DPAPI/CNG-protected secret
  bound to the current user profile. Do not claim DACL hardening for existing
  files until Hecate actively manages those ACLs.
- Linux desktop: use Secret Service/libsecret when a user session service is
  available, with file-backed bootstrap as the fallback for servers, CI, Docker,
  and minimal window managers.

The migration should be explicit in metadata: record which key source is in
use, import an existing file-backed key into the OS key store on first eligible
desktop launch, keep migration idempotent, and preserve `HECATE_BOOTSTRAP_FILE`
and `HECATE_CONTROL_PLANE_SECRET_KEY` as operator-controlled escape hatches.
Tests should cover missing keychain items, locked or unavailable keychains,
idempotent migration, fallback behavior, and recovery messaging.

## Native app and sidecar

The desktop app is a Tauri shell that bundles the main `hecate` runtime, which
it launches in gateway mode as its sidecar.

- The app launches `hecate` as a sidecar on a free loopback port.
- Windows bundles are not yet code-signed, so SmartScreen warnings are expected on first launch. macOS bundles cut by `release.yml` with the `APPLE_*` secrets configured are signed + notarized (Developer ID Application) and don't trip Gatekeeper.
- Quitting the app should stop the sidecar; closing a window may not quit the app on every platform.

## Dependency and advisory handling

Hecate uses GitHub Dependabot and CodeQL to catch dependency and code-scanning issues.

- Fixable advisories should be handled by updating dependencies or hardening the relevant code path.
- Some transitive advisories can be upstream-blocked. For example, the current Tauri Linux stack still depends on `gtk ^0.18`, which requires `glib ^0.18`; `glib >=0.20` cannot be forced safely until the Tauri/GTK stack moves.
- Upstream-blocked alerts should be documented in the relevant PR or release notes, then revisited when upstream releases a compatible fix.

## Operator checklist

- Keep the default loopback bind unless you add your own network protection.
- Use trusted workspaces, especially for in-place mode and External Agent sessions.
- Prefer prompt-mode approvals for interactive use.
- Revoke durable grants you no longer need.
- Keep Hecate, the desktop app, and external agent CLIs updated.
- Run high-risk workflows under a dedicated OS user, VM, or container.

## Reporting vulnerabilities

Use the repository-level [security policy](../SECURITY.md) for supported
versions, reporting steps, and response expectations. If private vulnerability
reporting is unavailable, open a minimal public issue asking for a private
security contact and avoid posting exploit details publicly.
