# Security

Hecate is local-first, single-operator software. This page documents the security model that exists today, what Hecate tries to protect, and what remains the operator's responsibility.

## Threat model

Hecate assumes the operator trusts their own machine, local user account, and selected workspaces.

- The gateway binds to `127.0.0.1:8765` by default.
- Browser requests are same-origin checked: by default, an `Origin` header must match the gateway host. Custom browser frontends must be listed in `HECATE_ALLOWED_ORIGINS`.
- `HECATE_RUNTIME_TOKEN` can require `X-Hecate-Runtime-Token` on Hecate-native `/hecate/v1/*` APIs. This protects the Hecate control plane, including Hecate-native chat and task routes that can spend configured provider credentials. It is an opt-in local guard for Hecate-aware clients, not multi-user authentication, and it does not wrap `/v1/*` endpoints.
- `HECATE_INFERENCE_TOKEN` can require `Authorization: Bearer <token>` or `x-api-key: <token>` on the provider-compatible inference routes: `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/messages`. It does not protect Hecate-native `/hecate/v1/*`, `/healthz`, static UI assets, or OTLP `/v1/traces`, `/v1/metrics`, and `/v1/logs`.
- Hecate is not designed to be exposed directly on a network.
- If you bind Hecate to anything other than loopback, startup requires `HECATE_ALLOW_NON_LOOPBACK_BIND=1`. Set it only when you have your own firewall, reverse proxy, or access-control layer in front.
- Hosted runtimes must use `HECATE_CLOUD_RUNTIME_MODE=1` behind the Hecate
  Cloud proxy. In that mode, non-health requests require trusted
  `X-Hecate-Cloud-*` identity headers plus the internal runtime secret, and
  local-only endpoints remain blocked. The runtime secret is not public auth;
  keep the runtime network-private.
- Do not put local-only endpoints such as workspace folder selection, "open in editor", local provider discovery, MCP registry discovery, MCP probe, reset-data, or shutdown behind a forwarding proxy. Those endpoints reject non-loopback sockets and `X-Forwarded-For` / `X-Real-IP` headers because they can inspect host-local state, open local OS UI, spawn diagnostic subprocesses, or mutate local operator state.
- Do not run Hecate on a shared host where untrusted local users can access the gateway port or data directory.

## Runtime boundaries

Hecate has two different execution surfaces with different trust levels.

| Surface                                               | Boundary                                                                                                                                                                                                                                                                  |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hecate Chat with tools on / native `agent_loop` tasks | Hecate owns the task loop. Tool calls use WorkspaceFS, ProcessRunner, or GitRunner as appropriate, with env sanitisation, output caps, timeouts, policy checks, approvals, and `bwrap` / `sandbox-exec` wrappers where available. This is not a VM or container boundary. |
| External Agents                                       | Codex, Claude Code, Cursor Agent, Grok Build, and similar integrations run as trusted local subprocesses in the selected workspace. Hecate supervises lifecycle, approvals, diagnostics, and Git diffs, but it does not sandbox the agent's internal runtime.             |

If you need a hard isolation boundary, run Hecate and its workspaces inside a VM, container, or dedicated OS user that you are comfortable letting tools modify.

## Workspaces

Hecate supports isolated generated workspaces and opt-in in-place workspaces.

- Isolated native task runs create workspaces under a Hecate-managed root from validated task/run IDs.
- In isolated mode, when the task source is a Git repository, Hecate clones it with `git clone --no-hardlinks -- <source> <workspace>`.
- In isolated mode, when the task source is a plain directory, Hecate copies it into the generated workspace.
- Generated workspace paths reject traversal and existing symlink components before Hecate creates files.
- Hecate-mediated workspace file operations use the shared WorkspaceFS resolver. This covers native `agent_loop` file/search tools, sandboxed file writes, generated workspace setup, and ACP adapter read/write callbacks. It does not sandbox external-agent subprocess internals.
- Operator-selected source directories are canonicalized. Symlinked source repos/folders are allowed by design, because operators may intentionally select them.
- `workspace_mode=in_place` skips clone/copy and runs tools directly in the selected source directory. Treat it as destructive.
- Adding or selecting a workspace in the UI does not clone it. Clone/copy happens only when a native task run provisions an isolated workspace.

Git is used for workspace setup and change review, not as a security boundary. Hecate also uses Git status/diff information to show branch state, changed files, and revertable workspace changes. Hecate-owned Git calls go through the shared GitRunner seam, which validates the workspace directory and runs Git with a sanitized environment; external-agent subprocesses can still run their own Git commands internally.

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
- Missing SQLite data directories are created as owner-only on POSIX (`0700`), and database files are created or repaired as `0600`. Hecate does not chmod existing parent directories supplied by the operator.
- Postgres DSNs often include usernames and passwords. Store
  `HECATE_POSTGRES_URL` / `DATABASE_URL` in your secret manager, not in code,
  traces, artifacts, or screenshots.
- Do not commit `.env`, SQLite databases, Postgres dumps or DSNs, release keys,
  update signing keys, or platform credential files.
- External agent credentials belong to the underlying CLI account. Hecate can probe and surface auth failures, but it does not own, proxy, or pool those accounts. See [External Agents](../runtime/external-agents.md#credential-and-account-boundaries) for credential and billing notes for Codex, Claude Code, Cursor Agent, and Grok Build.
- Stdio MCP servers inherit only runtime-essential environment variables from the gateway. Server credentials must be configured explicitly on that MCP server entry.
- If you expose Hecate beyond loopback while provider credentials are configured, anyone who can reach an unprotected inference path may be able to spend those credentials. Use your own network access control; set `HECATE_INFERENCE_TOKEN` for provider-compatible `/v1/*` clients and `HECATE_RUNTIME_TOKEN` for Hecate-native chat, task, and control-plane clients.

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
- macOS release bundles cut by `release.yml` with the `APPLE_*` secrets configured are signed + notarized (Developer ID Application) and are the only desktop bundles maintainers currently launch-test.
- Linux and Windows desktop artifacts are CI-built but have not yet been manually tested on real machines. Treat them as experimental and expect platform-specific bugs until that smoke coverage exists.
- Windows bundles are not yet code-signed, so SmartScreen warnings are expected on first launch once the MSI is manually tested.
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

Use the repository-level [security policy](../../SECURITY.md) for supported
versions, reporting steps, and response expectations. If private vulnerability
reporting is unavailable, open a minimal public issue asking for a private
security contact and avoid posting exploit details publicly.
