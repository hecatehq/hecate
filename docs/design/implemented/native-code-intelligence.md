# Native Code Intelligence — Implemented Design Record

> **Status:** first read-only slice implemented.
> **Current source of truth:** [Agent runtime](../../runtime/agent-runtime.md).
> **Next action:** dogfood latency and result quality before adding per-run
> language-server pooling or any write-capable refactoring operation.

Hecate's native agent loop already had bounded file reads, regular-expression
search, and shell access. Those tools are enough to inspect a repository, but
they cannot reliably answer semantic questions such as “which declaration does
this call resolve to?”, “what references will this edit affect?”, or “what did
the language server diagnose after the patch?”.

This slice adds one read-only `code_intelligence` tool with three layers:

1. LSP for type-aware navigation, hover, symbols, and diagnostics.
2. Optional `ast-grep` for syntax-aware structural patterns.
3. Existing bounded `grep` as the always-available fallback.

## Decisions

### LSP is the semantic layer

The [Language Server Protocol](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.18/specification/)
already defines the operations Hecate needs and lets each language's own tooling
retain responsibility for build graphs, types, and editor semantics. The first
allowlist deliberately covers only Hecate's own stack: `gopls` and TypeScript
7's native stdio LSP. Third-party TypeScript bridges can load workspace language
engines/plugins, and Rust Analyzer explicitly treats build scripts, proc macros,
and Cargo configuration as trusted code. Those providers do not belong in an
ungated read-family tool until Hecate has a stronger project-execution policy.

Hecate uses protocol-native `Content-Length` framing over private pipes. It does
not reuse the newline-framed MCP transport or the lossy merged-output terminal
surface. Every query owns a fresh bounded process and performs initialize,
document open, request, shutdown, and exit. A later cache must be per run and
workspace, evict on protocol failure, version open documents, and prove cleanup
under race tests before it replaces this simpler lifecycle.

### ast-grep is optional; Tree-sitter embedding is deferred

[ast-grep's run command](https://ast-grep.github.io/reference/cli/run.html)
already provides read-only Tree-sitter-backed patterns and structured JSON for
many languages. Hecate invokes the `ast-grep` binary with fixed argv and never
falls back to an ambiguous `sg` executable. The model may optionally select the
matched node within a contextual pattern through `--selector`, but only after
Hecate validates one bounded ASCII tree-sitter node-kind token. Rewrite flags
and arbitrary CLI options are not exposed.

The process starts outside the project and receives an explicit validated path.
Hecate also creates a minimal trusted `sgconfig.yml` in that private runtime
directory and passes its absolute path through a fixed `--config` argument.
This matters because [ast-grep project configuration](https://ast-grep.github.io/guide/project/project-config.html)
can otherwise discover a repository or ancestor `sgconfig.yml` and register
custom language libraries. Treating one of those configs as trusted would
quietly turn a read tool into project code execution. Explicit search targets
must be regular files or directories; FIFOs, sockets, and devices fail before
provider discovery or invocation.

Direct [Tree-sitter](https://tree-sitter.github.io/tree-sitter/) embedding is
deferred. It would require Hecate to bundle, version, and audit grammars while
still not supplying type-aware definitions or references. Revisit it only if
dogfooding shows a need for always-available incremental syntax indexes that
the optional structural subprocess cannot meet.

## Security contract

- The model chooses an operation and query plus, for structural search only, an
  optional validated node-kind selector; it never chooses an executable or
  arbitrary argv.
- Providers resolve from the gateway's trusted global `PATH` or an exact
  operator-configured executable path; Hecate never auto-installs. PATH
  discovery rejects marked project boundaries and shared markerless ancestors
  such as `/repo/bin`, while an exact override remains forbidden inside the
  active project boundary. Provider subprocesses receive a separately filtered
  PATH with empty, relative, non-directory, project-owned, shared-ancestor, and
  symlink-escaping entries removed; a validated exact provider may retain its
  own containing directory.
- Workspace input paths resolve through `WorkspaceFS`; traversal and symlink
  components fail closed.
- The provider receives a sanitized environment with a fresh per-query
  temporary home and private analysis/build caches, then requests a read-only,
  network-denied OS wrapper. Hecate removes that runtime after the query;
  gopls receives only validated external `GOPATH`/`GOMODCACHE` directories and
  TypeScript receives only a validated external `VOLTA_HOME`, so trusted
  providers can resolve existing dependencies and shims without inheriting
  project-controlled indirect executables. Semantic calls fail closed when the
  resolved task policy needs an isolation property that the active wrapper
  cannot enforce.
- Every returned URI must be a `file:` URI that resolves back inside the same
  workspace. External SDK/module-cache results are omitted and counted.
- Protocol frames, total bytes, stderr, result count, source preview, wall time,
  and structural-search output are bounded.
- Native grep/glob fallback work is bounded independently across traversal
  entries, aggregate file bytes, reusable glob-state transitions, matches, and
  rendered output; a cutoff is surfaced rather than presented as a complete
  no-match result.
- Server-requested edits cannot mutate the workspace: `workspace/applyEdit` is
  denied, safe registration/configuration requests get minimal responses, and
  unknown methods receive a JSON-RPC method-not-found response. The server is a
  trusted local executable; direct writes by that process are prevented only
  where the active OS wrapper enforces them.
- `code_intelligence` belongs to the existing `read_file` approval family.
  Capability discovery and structural search remain usable by read-only presets;
  semantic calls in those presets require Linux `bwrap`. Rename, code actions,
  and rewrites are not part of this contract.
- The report-only `workflow_mode="qa"` v0 contract is narrower than an ordinary
  read-only preset: its catalog omits `code_intelligence`, and dispatch rejects
  every operation before approval or provider startup, including capability
  discovery and structural search.

The OS wrapper prevents writes and network where available, but it does not make
an operator-installed language server untrusted. In particular, current macOS
Seatbelt handling only confines network, and wrapper-less hosts inherit the
gateway account's read access. URI filtering prevents those out-of-workspace
locations from entering model tool results; it is not full host-read isolation.

## Graduation gates

Before adding write-side semantic operations:

- measure cold-query latency and useful-result rate on Hecate's Go and
  TypeScript code;
- add a visible readiness/probe surface with provider version, handshake stage,
  negotiated encoding, supported operations, and a repair hint;
- prove a per-run pool has bounded size/idle lifetime, single-flight startup,
  document versioning, cancellation, crash eviction, and process-tree cleanup;
- make rename/code actions produce proposed patches first, then route approved
  application through Hecate's existing patch, writer-lease, and audit paths.
