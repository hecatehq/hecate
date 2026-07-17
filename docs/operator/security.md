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
- Hosted runtimes must use `HECATE_REMOTE_RUNTIME_MODE=1` behind the Hecate
  trusted proxy. In that mode, non-health requests require trusted
  `X-Hecate-Remote-*` identity headers plus the internal runtime secret, and
  local-only endpoints remain blocked. Local model providers are also disabled
  unless the runtime is explicitly launched with
  `HECATE_REMOTE_ALLOW_LOCAL_PROVIDERS=1` for an isolated sidecar deployment.
  External Agent CLI login state remains disabled unless a single-user personal
  remote runtime explicitly sets `HECATE_PERSONAL_REMOTE_EXTERNAL_AGENT_LOGINS=1`
  and keeps the runtime home/XDG directories on its persistent volume.
  The runtime secret is not public auth; keep the runtime network-private.
- Do not put local-only endpoints such as workspace folder selection, "open in editor", local provider discovery, MCP registry discovery, MCP probe, reset-data, or shutdown behind a forwarding proxy. Those endpoints reject non-loopback sockets and `X-Forwarded-For` / `X-Real-IP` headers because they can inspect host-local state, open local OS UI, spawn diagnostic subprocesses, or mutate local operator state. Reset-data is additionally reserved and currently returns `409 conflict` before mutation.
- Plugin registry APIs are also blocked in remote-runtime mode. Plugin rows are
  metadata for operator review: installing a manifest records requested
  capabilities, permissions, auth bindings, and validated MCP-server mount
  candidates, but does not execute plugin code, start plugin-declared MCP
  servers, mount tools, grant secrets, or make connector network calls.
- Do not run Hecate on a shared host where untrusted local users can access the gateway port or data directory.

## Runtime boundaries

Hecate has different execution and shell-access surfaces with different trust
levels.

| Surface                                               | Boundary                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| ----------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hecate Chat with tools on / native `agent_loop` tasks | Hecate owns the task loop. Tool calls use WorkspaceFS, ProcessRunner, or GitRunner as appropriate, with env sanitisation, output caps, timeouts, policy checks, approvals, and `bwrap` / `sandbox-exec` wrappers where available. This is not a VM or container boundary.                                                                                                                                                                                                                                                                                                                                                 |
| External Agents                                       | Codex, Claude Code, Cursor Agent, Grok Build, and similar integrations run as trusted local subprocesses in the selected workspace. Hecate supervises lifecycle, approvals, diagnostics, Git diffs, and opt-in workspace-scoped ACP terminal RPCs, but it does not sandbox the agent's internal runtime.                                                                                                                                                                                                                                                                                                                  |
| Operator shell access                                 | Hecate can open the operator's normal OS terminal from the workspace menu. It can also expose opt-in operator terminal sessions over the local runtime API when `HECATE_OPERATOR_TERMINALS=1`; those sessions are loopback-only, blocked in remote runtime mode, workspace-scoped, env-sanitized, output-bounded, and routed through the same static command checks / OS wrapper path as shell tools where available. Remote and container operators should otherwise use the surrounding infrastructure shell (`ssh`, `docker exec`, `kubectl exec`, provider console, or equivalent) when they need direct host access. |

If you need a hard isolation boundary, run Hecate and its workspaces inside a VM, container, or dedicated OS user that you are comfortable letting tools modify.

Hecate-owned command execution should go through governed task-runtime tools,
where WorkspaceFS, ProcessRunner, GitRunner, approvals, timeouts, output caps,
and sandbox policies can apply. Direct operator shells from the local OS or
deployment platform are administrative access to the runtime host, not Hecate
agent tools. Operator terminal sessions created through Hecate are an explicit
local opt-in for supervised command processes; they are not a replacement for a
VM/container boundary and they do not grant agents a new tool by themselves.
Hecate owns a terminal's full Windows Job Object or, on Unix, its dedicated
process group; output drain and workspace writer admission continue until the
owned unit is observed empty. Unix spawn and interactive-code validation blocks
known process-group/session detachers, but it is static best-effort enforcement:
sourced/generated code, arbitrary wrappers, custom binaries, and external
service managers can escape the group. Treat terminal commands as trusted local
subprocesses rather than a hard containment boundary.

Native web discovery is opt-in. The `web_search` agent tool is only advertised
when an operator configures a search provider and API key; it returns bounded
search results, while fetching a result URL still goes through `http_request`
and that tool's SSRF/host policy.

## Workspaces

Hecate supports isolated generated workspaces and opt-in in-place workspaces.

- Isolated native task runs create workspaces under a Hecate-managed root from validated task/run IDs.
- In isolated mode, when the task source is a Git repository, Hecate clones it with `git clone --no-hardlinks -- <source> <workspace>`.
- In isolated mode, when the task source is a plain directory, Hecate copies it into the generated workspace.
- Generated workspace paths reject traversal and symlink components before Hecate creates files. Provisioning opens stable root-relative filesystem handles before mutation, populates an owner-writable private staging directory with exclusive no-follow creates, restores copied directory modes while still private, and publishes with an atomic no-replace rename before its final identity check. A destination reservation, path swap, or symlink swap therefore fails closed without redirecting writes or replacing another creator's directory. Failed stages are identity-gated, made owner-writable for cleanup, and report cleanup failures instead of silently leaking private trees. Git sources are first cloned into a private `0700` temporary stage, then copied through the same confined placement path. Platforms without an atomic no-replace primitive fail closed rather than weakening placement.
- Hecate-mediated workspace file operations use the shared WorkspaceFS resolver. This covers native `agent_loop` file/search tools, sandboxed file writes, generated workspace setup, and ACP adapter read/write callbacks. Opt-in ACP terminal RPCs and operator terminal sessions are constrained to the selected workspace before spawn, but still run as trusted subprocess work. They do not sandbox external-agent subprocess internals.
- Operator-selected source directories are canonicalized. Symlinked source repos/folders are allowed by design, because operators may intentionally select them.
- `workspace_mode=in_place` skips clone/copy and runs tools directly in the selected source directory. Treat it as destructive.
- Adding or selecting a workspace in the UI does not clone it. Clone/copy happens only when a native task run provisions an isolated workspace.

Git is used for workspace setup and change review, not as a security boundary. Hecate also uses Git status/diff information to show branch state, changed files, and revertable workspace changes. Hecate-owned Git calls go through the shared GitRunner seam, which validates the workspace directory and runs Git with a sanitized environment; external-agent subprocesses can still run their own Git commands internally.

Workspace discard currently uses the complete raw **unstaged tracked** Git
patch (index → worktree) as optimistic-concurrency authority. GitRunner captures
it through the same hardened passive view used by structured inspection and
scopes a nested workspace to that workspace rather than the surrounding
checkout. This surface does not review or discard staged index changes or
untracked files. Before issuing a revision, GitRunner checks the scoped index;
any staged-only or mixed staged/unstaged state returns `422 invalid_request`
with no revision. The operator must unstage, refresh, and review the resulting
index-to-worktree patch. This fail-closed check prevents staged-only work from
appearing clean and prevents a mixed change from granting authority over only
its visible layer.

The review response carries an opaque SHA-256 revision of the exact unstaged
tracked patch bytes, including any final newline, and the discard request must
return that token. A trimmed patch, diff stat, stored historical message diff,
or truncated prefix is never mutation authority. Unstaged tracked patches above
the 4 MiB review limit fail closed instead of issuing a revision for a partial
view.

Before the final snapshot, Hecate closes and drains the owning chat lifecycle,
then acquires an exclusive closure from one shared process-local workspace
registry. Canonical equal, parent, and child roots conflict; ordinary siblings
remain independent. Darwin and Windows comparisons conservatively case-fold
path components so case aliases, including future paths, share one coordination
domain; this may serialize case-only siblings on a case-sensitive Darwin volume.
Task provisioning/start admission and execution, External Agent
turns, task-patch apply/revert, opt-in operator terminals, each live opt-in ACP
terminal, and each native agent-loop terminal hold writer admission for the
period in which they can change files. ACP and native agent-loop terminals keep
their own leases after the owning turn or execution attempt ends and release
them only after process-unit exit and output drain are observed. A live writer
makes discard return `409`, while the closure rejects or delays new work
according to that runtime's admission contract. Hecate then rereads the session and scans durable
non-terminal task runs and active chats for overlapping workspaces, covering
queued or recovered work that has no currently executing writer lease.

With those checks held, Hecate verifies the scoped index is unstaged, captures
the exact scoped index-to-worktree patch, and compares its revision with the
reviewed token. Conditional reverse apply then reserves Git's conventional
index lock, rechecks staged state and the reviewed patch's live index baseline,
and applies those exact captured bytes while holding the reservation. The
transient lock excludes well-behaved Git index writers during that final
recheck and apply; it does not exclude direct filesystem or non-cooperating
index writes. A committed baseline change or overlapping worktree change made
after the snapshot causes an atomic `409`
conflict without altering the selected files; unrelated and non-overlapping
edits are preserved. The registry coordinates one Hecate process only. It is
not a distributed lock or protection against an external editor, so the
durable-owner scan and conditional Git apply remain independent fail-closed
layers rather than a claim of replica-wide atomic exclusion. The revision token
is neither workspace content nor a durable identity, but it can reveal equality
with a known complete unstaged tracked patch. Treat it as sensitive operational
metadata and do not log or persist it.

## Approvals

Approvals are safety gates, not a sandbox.

- Native task approvals block the run until the operator approves or rejects.
- External-agent approvals are prompt-first by default when the adapter asks for permission.
- Durable external-agent grants can be reviewed and revoked from Connections.
- Auto-approval modes are dangerous for interactive use because they let tool requests proceed without operator review.

Review broad grants carefully, especially workspace-wide or adapter-wide grants
for file writes, shell commands, Git commands, network access, and MCP tools.
External-agent grants match Hecate's normalized `tool_kind`, not a vendor's
raw tool label, so an `mcp` grant applies to matching MCP tool requests from
that adapter within the selected scope.

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
- On self-hosted non-loopback starts, Hecate logs warnings when configured
  provider credentials exist and either shared token is missing. Remote runtime
  mode uses trusted proxy identity instead, so those local token warnings do not
  apply there. The self-hosted warning is conservative advisory output and can
  still appear behind an authenticating reverse proxy.

### Dictation audio

Microphone audio is sensitive operator input. Hecate accepts dictation only on
the Hecate-native runtime API, bounds it to 10 MiB, validates its declared media
type against its file signature, and holds at most two requests in process at a
time. The body exists only in transient request/provider memory: Hecate does not
write audio to the chat attachment store, transcript rows, usage events,
traces, metrics, logs, or artifacts.

The operator must select one transcription provider. Hecate captures that
provider's opaque generation and revalidates it immediately before the upstream
call; provider removal, endpoint/account replacement, or capability removal
fails closed before disclosure. Dictation has no Auto route and no
cross-provider failover. A configured cloud provider receives the recording;
the `local` label describes provider configuration and is not a network egress
firewall. Use a local LocalAI endpoint when audio must stay on the operator's
machine, and enforce destination policy outside Hecate for non-loopback custom
URLs.

The returned transcript becomes ordinary editable composer text. It is not
auto-sent, but once the operator sends it, normal chat transcript retention and
provider/agent disclosure rules apply. The desktop app requests OS microphone
permission only when recording starts; browser deployments are subject to the
browser and origin's microphone permission policy.

### Chat attachment data

Files attached to Hecate Chat are operator data and can be sensitive. Hecate
stores their binary bodies in the chat attachment backend (`memory`, SQLite, or
Postgres), separately from transcript JSON. Message rows and API snapshots keep
only immutable metadata; file bytes and base64 are not written to SSE frames,
traces, logs, metrics, errors, or the browser's persisted queued-prompt state.
Deleting a chat deletes its attachment bodies, while deleting an already-linked
individual attachment is blocked to preserve transcript integrity.

Queued chat message idempotency stores a session-scoped client request id, a
SHA-256 fingerprint of the canonical request payload, ownership/commit state,
and the committed message id. It does not store the prompt, system prompt, MCP
configuration, or attachment body a second time. The fingerprint still reveals
request equality and may be guessable for low-entropy content, so protect
SQLite files and Postgres backups as operator data.

Before commit, the operator UI stores each queued text prompt and route snapshot
in a separate local-storage record. Same-origin tabs in the same browser profile
can therefore observe that content and coordinate its delivery fence. The queue
is not copied to Hecate's server, another browser profile, or another device
until the message API commits the user transcript row. Browser-profile access
should be treated as access to unsent prompt content.
Hecate verifies queue writes and removals instead of assuming the browser
accepted them. Each mutation writes a new immutable physical key containing the
queue-lineage generation (called the reset generation internally), a random
revision, and the logical item id, verifies that
record, and only then retires the exact previous revision. Multiple surviving
revisions are quarantined instead of choosing one silently. Failed edits do not
revoke an ambiguous `submitting` fence; a failed edit of a ready item removes
only its exact older revision and blocks the in-memory replacement.

A logical Remove first records an immutable, generation-scoped tombstone for the
SHA-256 fingerprint of the exact canonical queue payload (excluding its storage
revision). A matching payload is suppressed; a different payload with the same
logical id is preserved as a manual conflict, and a concurrent `submitting`
replacement remains fenced for status reconciliation. Tombstones contain no
prompt, but their hashes reveal payload equality and may be guessable for
low-entropy prompts. They remain until explicit queue cleanup or browser site-data
cleanup, so protect browser-profile storage as operator data.

Earlier queue formats used mutable revisionless keys. Because browser storage
has no compare-and-delete operation, Hecate cannot safely erase one of those
keys while another tab may be replacing it. Migration writes a prompt-free
SHA-256 fingerprint marker and leaves the mutable row as a suppressed legacy
shadow. A matching shadow stays consumed after its immutable revision is
submitted or removed; a changed shadow is surfaced as blocked conflict state.
The legacy prompt and equality-revealing marker can remain until explicit queue cleanup or
manual browser site-data cleanup.

New queue records retain their originating project id solely for browser-side
deletion cleanup; legacy records whose project owner cannot be inferred and raw
records that cannot be audited make that cleanup fail closed. Single-chat
deletion first writes a queue-lineage-generation-scoped session tombstone containing no
prompt metadata; queue admission, cross-tab events, reload, and cleanup all use
that fence to prevent a stale tab from resurrecting work for the deleted chat.
After any project
tombstone, unknown-owner records are quarantined from delivery because Hecate
cannot prove they are unrelated. An explicit empty id distinguishes a newly
project-free prompt from a legacy prompt with unknown ownership. Project
deletion writes a profile-wide id tombstone, stamped with the queue-lineage generation,
before cleanup; enqueue admission, cross-tab notifications, and reload recovery
use it to reject or purge late records. Explicit all-session browser-queue
cleanup can advance the profile-wide queue-lineage epoch to quarantine stale
open tabs; the disabled server reset endpoint does not invoke this action. That
cleanup snapshots and post-audits item revisions, project and session tombstones, exact-item
tombstones, legacy-migration markers, and the prompt-bearing legacy whole-array key;
old-generation cleanup cannot address a fresh same-id revision in the new
generation. Unreadable epoch, item, marker, or tombstone metadata fails closed.
Empty or whitespace-padded epochs are noncanonical and fail closed too.

Content reads are session-scoped Hecate-native API requests. Loopback clients
remain inside the normal local operator boundary; remote-runtime and protected
self-hosted clients must send the configured runtime credential. Supported
PNG, JPEG, and WebP responses use `Content-Disposition: inline`; other files
use `Content-Disposition: attachment`. Every response carries its normalized
stored media type, `X-Content-Type-Options: nosniff`,
`Content-Security-Policy: sandbox; default-src 'none'`, and private/no-store
caching. The UI fetches content through its normal API client, which adds the
runtime-token header when that optional guard is configured, and uses revocable
Blob URLs for image display. A four-slot process gate is acquired before the
attachment store is read and held through integrity validation and the body
write. Saturated reads fail with a typed `429`; admitted writes have a
route-local 30-second socket deadline. Content delivery is also a counted chat
lifecycle operation, so chat deletion waits for an admitted lookup and write
instead of deleting the body while it is being served.

Hecate-owned Tools-off direct-model turns accept PNG, JPEG, and WebP images;
External Agent turns also accept arbitrary files as opaque inputs. Both modes
accept at most four files per message, with a 5 MiB per-file limit and a 12 MiB
combined-message limit. Direct-model rasters require MIME/magic-byte agreement,
full decoding, an 8000-pixel axis limit, and a 16-megapixel decoded limit. An
External Agent file detected as one of those rasters is promoted to image
handling only after the same bounded decode succeeds; malformed or
over-dimension image-like bytes remain accepted as an inert
`application/octet-stream` file. Hecate does not decode, scan, or sanitize
other External Agent file formats. It normalizes filenames, generates ids,
computes SHA-256 digests, and preserves the accepted original bytes. In particular, it
does not strip EXIF, GPS, color-profile, or other embedded image metadata.
Remove sensitive metadata before uploading. The normalized filename and raw SHA-256
digest are also returned in Hecate-native upload and session metadata. They are
operator-visible metadata, not confidentiality controls: a filename can reveal
sensitive wording, while a stable digest reveals repeated content and lets a
client that can read the session API test for a known file. Rename sensitive
files before upload when the display name itself matters, and do not treat the
digest as proof of secrecy or ownership. Protect session API responses,
database snapshots, and backups as sensitive metadata even when attachment
bodies are handled separately. SVG and other arbitrary formats are not
direct-model image inputs; an External Agent receives them only as files.
Remote image URLs, filesystem paths supplied by the client, and Tools-on Hecate
turns remain outside this attachment surface.
A fixed two-slot gate per Hecate process bounds concurrent upload body reads and
image decodes; a saturated upload is rejected with `429` before
its body is read. Each admitted body must finish reading within 60 seconds;
Hecate applies a route-local socket read deadline, closes a stalled body and its
expired HTTP/1 connection, and releases its slot without imposing a global
server read timeout on SSE or other streaming routes. Unlinked drafts are
bounded to eight and 40 MiB per session. They become reclaimable after 24 hours
and are deleted when another attachment is staged anywhere in the attachment store;
linked transcript bodies
never expire before chat deletion. Draft and linked bodies together are capped
at 512 MiB per chat. The aggregate cap is 512 MiB for the memory backend and 4
GiB for SQLite/Postgres. Direct-model history has a 12 MiB stored-image budget
and fails closed on stored filename, creation-time, size, or digest mismatch.
A separate two-slot process gate bounds concurrent attachment claims,
historical body reads, base64 expansion, provider serialization, and provider
calls for image-bearing turns. Saturation returns `429 chat.image_turn_busy`
before draft claim or transcript mutation; text-only turns remain available.
A second two-slot process gate bounds External Agent attachment claims,
hydration, private staging, and synchronous ACP prompt lifetime across
workspaces. Saturation returns `429 chat.external_file_turn_busy` before draft
claim or transcript mutation; text-only External Agent turns remain available.
Immediately before persistence, an upload rechecks the per-session lifecycle
generation captured before its first session read. That snapshot is a bounded
request lease rather than a permanent process tombstone. Delete and native
close serialize with each other, reread the authoritative session, and wait for
writes admitted first; an upload delayed behind destructive ownership is
rejected without persisting. If an independent attachment store commits after
owner loss and its compensating draft delete cannot be verified, Hecate returns
a fixed `500` without attachment identifiers, filenames, digests, body bytes,
or raw storage errors. Clients treat that result as ambiguous and do not
automatically retry the bytes. Unknown upload, content-read, draft-delete, and
session-cleanup failures use the same fixed-message boundary; SQL text,
database connection strings, and store error bodies remain internal. A replay
of the exact `client_request_id` and attachment-bearing payload repairs a pending
claim for an already-committed user message before it reports success.

Attaching a file to an External Agent turn authorizes Hecate to disclose it to
that selected local agent process. At the final `session/prompt` boundary,
Hecate uses only the live capabilities returned when that exact ACP session was
initialized. A supported raster becomes an ACP `image` block only when the
session advertises image input. Otherwise Hecate uses an embedded `resource`
only when embedded context is advertised. The baseline fallback is a
`resource_link` to an exact read-only file in a private per-turn temporary
directory. Rich blocks share a cumulative 768 KiB encoded wire budget, leaving
256 KiB of headroom below the supported adapters' 1 MiB JSON-RPC line limit.
The accounting uses actual serialized block sizes, so prompt text, base64
expansion, and JSON escaping all consume that budget; an overflow file is
staged even when the corresponding rich capability is advertised, while prompt
text that exceeds the budget fails before ACP dispatch. Hecate preflights raw
payload size before allocating base64 or serialized rich-block copies.
Built-in command bridges render the staged URI only into the originating
provider command. Their bounded transcript records attachment name and MIME
metadata, never the ephemeral URI, so a deleted path is not replayed in a later
turn.
An already-cancelled turn also fails before prompt construction, and the
runtime rechecks cancellation immediately before `session/prompt`. Hecate's ACP
filesystem callback allows only that exact staged path, never widens the
workspace filesystem boundary, and rejects every other read or write inside the
stage namespace before workspace fallback. Immediately before dispatch, Hecate
rechecks the retained parent, ancestor, directory, and child identities.
If the adapter proves that the provider-native conversation is missing before
any output, Hecate may rebuild and disclose the same prompt blocks once to a
fresh session of that same selected adapter. This requires a durable transcript
with no successful or ambiguous agent turn, and Hecate persists the replacement
native id before retrying. A persistence failure, cancellation before the
commit, partial output, file change, completed tool, compacted history, unknown
activity, or unclassified adapter error prevents redisclosure.
The current turn must contain only the command bridge's outer prompt-subprocess
start and failed-finish records; any provider update or inner tool fails closed.
Historical raw diagnostics must either contain that exact pair or the narrowly
recognized process-command-not-found failure. When file privacy replaces raw
diagnostics with the fixed withheld marker, one matching failed outer-command
activity is required instead. Unknown or empty activity statuses are never
treated as proof that a tool was harmless.
The separate process-scoped adapter path may replace an unloadable id after an
adapter restart because that catalog contract explicitly says the id cannot
outlive the process. It keeps the fresh in-memory session unpublished while the
durable callback runs, then uses the same persist-before-prompt rule. Durable
and unknown scopes do not take that path. After either callback commits, every
shutdown or cancellation result retains the replacement id so final settlement
cannot write the stale id back.

On Darwin and Linux, Hecate retains handles for the canonical temporary parent
and its ancestors, requires root/current-user ownership, requires sticky
protection on writable directories, and rejects extended ACLs. It creates the
random directory and children relative to those handles with no-follow,
exclusive operations. Every verified Darwin handle must report `MNT_LOCAL`
before Hecate relies on mode bits and the native ACL tools. Every verified Linux
handle must report ext2/3/4, XFS, Btrfs, tmpfs, overlayfs, ramfs, or F2FS through
`fstatfs`; every other model fails closed, including NFS, SMB/CIFS, FUSE, ZFS,
AUFS, eCryptfs, and 9p. Linux tolerates an unsupported POSIX ACL xattr operation
only after that filesystem check, where mode bits remain the complete effective
model. Darwin and Linux remove and read back ACL state before any prompt byte is written;
Hecate also verifies the directory as `0700` and each empty file as `0600`, then
seals files as `0400` and the directory as `0500`. A custom temporary path and
every canonical ancestor mount must satisfy these checks. When the default does
not, launch Hecate with `TMPDIR` on an allowlisted local filesystem whose
canonical ancestors also pass. Other Unix builds fail this fallback closed
rather than relying on inherited defaults.

On Windows, the temporary parent and every retained ancestor must resolve to
non-reparse directories on a local drive owned by the current user,
LocalSystem, Administrators, or the fixed Windows Modules Installer
(TrustedInstaller) service SID and without an effective untrusted DACL grant
for delete-child, direct delete, DACL replacement, or owner replacement.
TrustedInstaller is accepted only while auditing pre-existing managed parents
and ancestors; Hecate-created staging directories remain current-user-owned and
never grant that service access.
Inherit-only entries do not control the directory on which they appear; every
existing descendant is audited separately and the new stage cannot inherit
them. The stage directory is created relative to the retained parent with a
current-user owner and a protected inheritable DACL granting only the current
user and LocalSystem in the atomic create operation. Hecate reads that exact
shape back before creating children. Each empty child is created relative to
the retained directory with an explicit current-user owner and protected DACL
for the current user and LocalSystem, then read back before writing. Completed
files receive protected read-only DACLs, while the completed directory receives
the same process-user and LocalSystem access without inheritable ACEs. Both are
verified again. Before dispatch, Hecate replaces each
write-capable construction handle with a read/identity-only retained handle
whose sharing permits ordinary readers that decline write sharing. After prompt
settlement and identity verification, cleanup first reopens the exact directory
relative to the retained parent with owner `WRITE_DAC` plus identity rights,
verifies the sealed DACL, and restores a non-inheritable private full-control
DACL through that handle. Avoiding propagation during seal and cleanup also
avoids filesystem normalization of generic inheritable ACEs. Hecate records
that intermediate state across retries, then reacquires the
same directory with mutation rights. After re-verifying child identity, Hecate
closes its own retained child handles immediately before the same-parent
quarantine rename because Windows forbids renaming a directory with open
descendants. A still-open external reader makes that transition retry and
eventually report failure rather than weakening identity checks. UNC,
remote-drive, or reparse resolutions fail before staging.

Prompt completion first clears Hecate's body references, moves the retained
stage to a fresh random quarantine name relative to its retained parent, and
removes children and the directory relative to retained handles. The complete
quarantine, permission-preparation, and removal transition is retried four
times with bounded backoff. After removal succeeds, Hecate records that state
and gives the retained-handle removal proof its own bounded retries; it never
re-enters pathname-based quarantine or permission work after the name may have
disappeared. Cleanup never changes permissions through the
advertised pathname; substitution or identity drift fails closed and preserves
the retained identity for a later retry. If every synchronous attempt fails,
the turn reports an error and transfers the exact identities to one
runtime-owned cleanup quarantine. Its single background janitor retries them,
including after the originating ACP session has retired, and session shutdown
wakes it again after the agent process releases its handles. At most four
failed stages are retained for one session and at most 16 file-turn
reservations or failed stages exist process-wide. Further file-bearing turns
fail closed when either bound is full until cleanup makes room; text-only turns
remain available. A persistently protected remnant can remain in the OS
temporary directory; stop Hecate and its agent processes before removing it.

Session shutdown closes new-turn admission before observing the active turn.
Each turn owns that lifecycle from before workspace-diff capture through final
result settlement. After bounded cancellation and provider-process termination,
shutdown drains that full-turn owner again before it snapshots or waits for the
cleanup backlog. A stubborn turn or stage returns an explicit close error while
the runtime-owned bounded janitor keeps the retained identity for later cleanup.
After the final handle-bound identity audit, Hecate rechecks turn cancellation
immediately before `session/prompt`. If cancellation won during that audit, it
clears the in-memory prompt files and removes or retains cleanup ownership
without disclosing the resource link to the agent.

The per-turn redactor retains only staged path aliases, never attachment bodies.
It masks complete path/URI aliases and ordinary accumulated ACP chunking from
visible output, activities, terminal previews (including late exits), stop
reasons, and errors. Permission requests are reconstructed from sanitized typed
data before approval persistence and fail closed if a protocol identifier would
change. Available commands, config-option updates, and direct config-write
responses redact human-facing fields and drop entries whose protocol identifiers
or values would change. The body-free alias redactors remain for the ACP session
lifetime, so delayed permission requests and direct config responses stay covered
after the originating turn and cleanup proof. Original typed-control wire records
are withheld so a delayed update cannot repersist an old alias in a later turn.
Raw ACP diagnostics are also withheld for turns that
emitted staged-turn raw data because arbitrary protocol chunking cannot be
reconstructed safely. This
turn redaction is complemented by a process-wide ACP SDK diagnostic boundary:
the receive loop cannot start until its structural logger is installed, and
peer-controlled protocol payloads, identifiers, methods, and error values are
dropped before local or exported logging. Only fixed diagnostic event names and
bounded queue counters survive. Native close/delete RPC failures likewise retain
only a fixed classification and numeric code, never peer message or data.
Together, these controls defend against
accidental disclosure; they are not DLP against the selected trusted
agent: an agent can deliberately transform a path or segment it into unrelated
short message/activity records that are not individually identifiable as an
alias. The exact callback read remains available only while the prompt is
active. Its body-free stage and quarantine namespaces remain denied across
later callbacks and turns until handle-bound cleanup proves removal, so a
protected remnant cannot fall through to ordinary workspace reads. Absolute,
file-URI, and relative spellings are compared against lexical and canonical
workspace roots, and alias registration cannot cross an in-flight WorkspaceFS
read or write.

Adapter stderr is captured only as a bounded startup-failure diagnostic. After
initialization and initial session/model/config selection succeed, Hecate
zeroes that buffer under its writer lock and discards later stderr for the
session lifetime before any prompt can carry attachment bytes or staged paths.

This stage is not an isolation boundary against another process running as the
same OS user. Such a process can change owner-controlled modes or DACLs, or
inspect the stage if it discovers the path. Staging limits accidental exposure
and untrusted-principal inheritance; it is a disclosure mechanism for the
selected trusted subprocess, not a sandbox around that subprocess. The agent
and any service it contacts can process supplied content under their own
security and retention terms.

Attaching an image authorizes Hecate to send that image to an upstream model
provider. With an explicit provider route, that is the selected provider. With
Auto, Hecate resolves an eligible image-capable provider only when the turn is
sent, so the concrete egress destination cannot be reviewed in advance. Select
an explicit provider when deterministic provider or account egress matters,
and review the image contents before sending. Local-first storage does not make
a cloud provider call local. A same-provider retry can resend the image after a
transient failure, and later text turns can resend recent stored images to that
same configured provider generation as bounded model history. Hecate binds
hydrated bytes to both the canonical runtime name and an opaque, non-secret
configuration generation, then revalidates both immediately before the provider
call. Credential rotation, endpoint/account/configuration changes,
delete-and-recreate, and same-name replacement therefore cannot inherit prior
image access. Legacy rows
without a generation and recreated runtime-only providers omit historical
bytes. A turn that fails before any provider call also keeps no generation, so
its stored bytes are omitted from later history. The internal generation is
persisted only as a disclosure fence and is not returned by APIs or written to
telemetry, logs, or errors. Hecate also
disables cross-provider failover for hydrated image requests and omits
historical bytes when an Auto provider boundary is unresolved.

Provider-compatible `/v1/chat/completions` and `/v1/messages` image blocks are
not retained as Hecate attachments and do not require Hecate-native capability
admission, so custom providers can receive their native rich-content shape.
Their encoded JSON body is capped at 32 MiB and must arrive within 60 seconds.
Auto routing may choose the initial destination, and a same-provider retry may
resend the image, but Hecate does not fail the request over to a second
provider. The selected opaque provider instance is revalidated immediately
before each non-streaming or streaming dispatch, so a same-name replacement
cannot retarget the request. Compatibility image URLs are limited to HTTP(S) or
valid base64 image data URIs. Provider HTTP and SSE errors are converted to typed failures;
Hecate constrains error-type tokens and removes hierarchical URL userinfo,
query, and fragment components before error text reaches clients, logs, traces,
telemetry, or provider health. URL paths remain diagnostic text; do not place
credentials in path segments.

Hecate does not apply application-layer encryption to attachment bodies. The
memory backend holds them as process memory; SQLite and Postgres persist the
original bytes as database blobs. At-rest, transport, snapshot, and backup
protection therefore comes from the host filesystem or volume encryption and
the configured database. Restrict access to the Hecate data directory (for
Docker, `/data`), Postgres roles and network paths, volume snapshots, and
backups as carefully as the source attachments.

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
