# Local Models — Hecate-managed llama.cpp runtime (RFC)

> **Status:** draft. Not implemented.
> **Related:** [Architecture](../architecture.md), [Events](../events.md),
> [Runtime API](../runtime-api.md), [Desktop app](../desktop-app.md),
> [Known limitations](../known-limitations.md).
> **Owner:** see [`AGENTS.md`](../../AGENTS.md).

Hecate should be able to download and run open-weight models on the operator's
machine without requiring them to install a separate runtime (Ollama, LM Studio)
first. Today the gateway can only *route to* local runtimes the operator
manages themselves. This RFC adds **Hecate-managed local models** via a bundled
`llama.cpp` server.

The core distinction with the existing local-provider preset:

| Concept | Examples | Who manages the binary | Who manages the model file |
|---|---|---|---|
| External local runtime | Ollama, LM Studio, LocalAI, operator-installed llama.cpp | Operator | Operator |
| **Hecate-managed local model** *(this RFC)* | `qwen2.5:7b`, `llama-3.2-3b`, … | **Hecate (Tauri sidecar)** | **Hecate (`<data_dir>/models/`)** |
| Cloud provider | OpenAI, Anthropic | n/a | n/a |

Hecate already ships the gateway and the ACP bridge as Tauri sidecars; adding
`llama-server` as a third sidecar is the same pattern. The new surface is
catalog browse, model install with progress, runtime lifecycle, and an
internal HTTP proxy that exposes the active model through Hecate's existing
OpenAI-compatible routing.

## Problem

A first-time desktop operator who wants to try a local model today has to:

1. Install Ollama or LM Studio.
2. Pull a model through that tool's CLI / UI.
3. Add the runtime as a local provider in Hecate's Connections workspace.
4. Hope discovery picks it up.

Each step is a place to bounce off. "Try a local model" should be one click in
Hecate, the same way it's one click in Ollama or LM Studio themselves.

## Goals

- Bundle a `llama.cpp` `llama-server` binary in the Hecate desktop app so the
  operator can run local models without installing anything else.
- Curate a small list of well-known GGUF models the operator can install with
  one click, sourced from HuggingFace.
- Let the operator paste a HuggingFace GGUF download URL for models outside
  the curated list.
- Expose installed models through Hecate's existing `/v1/models` aggregation
  so the chat composer's model picker surfaces them automatically.
- Stream download progress so the UI can show it inline and the operator can
  cancel mid-flight.
- Keep the lifecycle observable through the existing OTel surface (named span
  events, stable error codes, `docs/events.md` registry).
- Preserve operator override: if the operator has their own `llama-server`
  running, Hecate must not stomp on it.

## Non-goals

- No Ollama-style re-hosted model registry. HuggingFace is the source of
  truth for GGUF distribution; we do not run a CDN.
- No HuggingFace browse UI in v1 (search, faceting, pagination, gated-repo
  auth). Catalog is compiled-in; URL paste covers the rest.
- No multi-model resident runtime ("keep N models warm"). One active model at
  a time, restart-on-switch. LRU keep-warm is reserved for a follow-up.
- No headless / CLI distribution. The Go gateway alone, without a bundled
  binary, leaves the feature dormant.
- No Linux / Windows desktop bundles in this RFC. They follow the broader
  Tauri matrix expansion.
- No fine-grained per-model knobs (LoRA, draft model, speculative decoding,
  custom chat templates). Catalog ships recommended defaults; advanced tuning
  is out of scope for v1.
- No model deletion from the chat-composer model picker. Lifecycle stays in
  the Connections "Bundled model runtime" surface.

## Recommended Shape

Five pieces, each thin:

1. **Bundled binary** — Tauri sidecar adds `llama-server`. Gateway looks for
   the binary via `HECATE_LLAMA_SERVER_BIN`; if unset, feature is dormant.
2. **Storage** — installed model files live at `<data_dir>/models/<slug>.gguf`.
   A new `installed_models` field on the persisted `controlplane.State`
   holds enrichment (HuggingFace URL, sha256, recommended params,
   last-loaded time). Persisted by every controlplane Store
   implementation. The repo ships memory + sqlite today; the shared
   apply helpers are written so a future postgres / remote store can
   adopt the surface without touching the helpers.
3. **Lifecycle** — single child process at a time, started on demand, killed
   on operator request or process crash. Operator-visible loading state for
   the cold-load duration.
4. **Provider integration** — one auto-registered `llamacpp` provider points
   at a gateway-internal proxy. The proxy is a small reverse-proxy mounted at
   `/hecate/internal/llamacpp/v1/...` that forwards to the active child's
   port. Existing OpenAI-compat routing changes nothing.
5. **API + UI** — Hecate-native endpoints under `/hecate/v1/local-models/`.
   UI lives as a new card in Connections that opens a SlideOver for catalog
   browse + install progress + lifecycle control.

### Binary resolution

```text
Gateway startup:
  if env HECATE_LLAMA_SERVER_BIN is set and the file is executable:
      use it
  else:
      feature dormant — log "local models unavailable: binary not bundled"
      /hecate/v1/local-models/* mounts but returns 503 local_models_unavailable
      /v1/models excludes llamacpp entries
      Connections card renders the "not available in this build" state
```

Tauri sidecar startup sets `HECATE_LLAMA_SERVER_BIN` to the resolved path of
the bundled `llama-server` before spawning the gateway. The resolution path
mirrors the existing `hecate-acp` sidecar logic in
`tauri/src-tauri/src/sidecar.rs`: dev mode finds the binary in
`binaries/llama-server`, packaged mode finds the per-triple variant Tauri's
`externalBin` bundler produces. Build-time tooling (`tauri/scripts/fetch-llama-server.sh`
or a Cargo `build.rs` hook) downloads and verifies the pinned llama.cpp
release for each target before `bun run tauri build`.

v1 platform: **macOS arm64 only** (Metal-enabled by default in llama.cpp's
prebuilt macOS arm64 build). Linux / Windows ride the wider Tauri matrix
expansion with Vulkan + CPU once that lands.

### Storage layout

```text
<GATEWAY_DATA_DIR>/
  hecate.runtime.json       (existing)
  gateway.log               (existing)
  hecate.sqlite             (existing)
  models/                   (new)
    qwen2.5-7b-instruct-q4_k_m.gguf
    llama-3.2-3b-instruct-q4_k_m.gguf
    ...
  llamacpp/                 (new, optional)
    bin/                    (unused in v1 desktop; reserved for future
                             headless lazy-download path)
```

The file on disk is the source of truth. The `installed_models` row is
enrichment; boot reconciliation drops rows whose file vanished and leaves
unknown files alone (they don't appear in the registry until imported).

`InstalledModel` record:

```go
type InstalledModel struct {
    ID                 string    // catalog slug, e.g. "qwen2.5-7b-q4_k_m"
    DisplayName        string    // "Qwen 2.5 7B Instruct (Q4_K_M)"
    FilePath           string    // relative to GATEWAY_DATA_DIR
    SourceURL          string    // HuggingFace direct GGUF URL
    SHA256             string    // pinned; verified post-download
    SizeBytes          int64
    RecommendedContext int       // n_ctx
    Capabilities       Capabilities // tool_calling, vision, streaming — usually "none"/"none"/"supported" for community GGUFs
    InstalledAt        time.Time
    LastLoadedAt       time.Time // zero until first load
}
```

`controlplane.Store` gains:

```go
UpsertInstalledModel(ctx, m InstalledModel) (InstalledModel, error)
DeleteInstalledModel(ctx, id string) error
```

(List is served via the existing `Snapshot()` — `State.InstalledModels`.)

Implemented in `store_memory.go` and `store_sqlite.go`. The repo
doesn't ship a postgres store today (the entire controlplane is
memory + sqlite); when one lands, the shared
`applyUpsertInstalledModel` / `applyDeleteInstalledModel` helpers
plug in unchanged. The sqlite store keeps its existing
JSON-blob-in-a-row shape — adding `InstalledModels []InstalledModel`
to the `State` struct *is* the migration.

### Lifecycle — single child, restart on switch

```text
Runtime states:
  idle      — no child running
  starting  — spawned, waiting for /health
  running   — accepting requests
  stopping  — kill signal sent, waiting for exit
  failed    — last start failed; surface error until next start attempt
```

State transitions are observable:

| From → To | Trigger |
|---|---|
| idle → starting | EnsureLoaded(modelID) called and idle |
| running(A) → stopping → starting(B) | EnsureLoaded(B) called while running A; A killed first |
| running → failed | child exits non-zero outside of operator-requested stop |
| any → idle | operator clicks Stop, or process crashes after retry policy gives up |

v1 retry policy: if a child crashes, surface the failure and stay in `failed`
until the operator (or the next proxied request) requests a restart. No
exponential backoff loop; the operator's eyeballs are the retry policy.

The runtime owns:

- The active child `*exec.Cmd`.
- The chosen TCP port (picked from a free-port pool, same pattern as the
  existing Tauri gateway-port picker).
- A `/health` poll goroutine that flips `starting → running` when the child's
  health endpoint responds; aborts with a structured error after 30 s.
- A "stop the world" hook the gateway calls on shutdown so the child doesn't
  outlive the parent.

### Provider integration — single proxy

On gateway boot, the local-models subsystem ensures one provider row exists:

```text
id:        llamacpp
kind:      local
protocol:  openai
base_url:  http://127.0.0.1:<gateway-port>/hecate/internal/llamacpp/v1
enabled:   true
```

The row is auto-managed: hidden from the Connections "Add connection" preset
picker, not editable from the per-provider Edit drawer, removable only by
disabling the feature flag. If an operator-created `llamacpp` provider
already exists (legacy from when `llamacpp` was just a preset for a
self-managed binary), the auto-registration logs a structured warning and
skips. Operator override wins.

The internal proxy is a small reverse-proxy mounted at
`/hecate/internal/llamacpp/v1/{path:.+}` that:

1. Buffers the request body just enough to read `model` from the JSON.
2. Calls `Runtime.EnsureLoaded(ctx, modelID)`. Blocks until the runtime
   reports `running` for that model, or returns 503 with
   `local_model_runtime_unavailable` on error.
3. Forwards the full request to `http://127.0.0.1:<child-port>/v1/<path>`.
   Streaming preserved end-to-end — no buffering past the model peek.
4. Emits the per-request `local_model.proxy.routed` span event with model_id.

`/v1/models` aggregation gets a new source: the `installed_models` registry
contributes one entry per row, attributed to `provider: "llamacpp"`, with
`discovery_source: "local_model_registry"`. A freshly-installed model
appears in `/v1/models` without restarting the gateway. Capabilities default
conservatively (`tool_calling: "none"` unless the catalog entry says
otherwise — most community GGUFs don't reliably tool-call).

### Catalog — pinned HuggingFace GGUF URLs

Compiled-in catalog at `internal/config/llamacpp_catalog.go`. Each entry is
one specific GGUF file, pinned by sha256, sourced from a trusted converter
account (`bartowski`, `lmstudio-community`).

```go
type CatalogEntry struct {
    ID                 string // slug; matches InstalledModel.ID once installed
    DisplayName        string
    Description        string
    HuggingFaceURL     string // direct .../resolve/main/file.gguf URL
    SHA256             string
    SizeBytes          int64
    RecommendedContext int
    Capabilities       Capabilities
    License            string // SPDX-style hint; not enforced
}
```

v1 entries (subject to confirmation):

| Slug | Display name | Size |
|---|---|---|
| `llama-3.2-1b-q4_k_m` | Llama 3.2 1B Instruct (Q4_K_M) | ~770 MB |
| `llama-3.2-3b-q4_k_m` | Llama 3.2 3B Instruct (Q4_K_M) | ~2.0 GB |
| `qwen2.5-0_5b-q4_k_m` | Qwen 2.5 0.5B Instruct (Q4_K_M) | ~370 MB |
| `qwen2.5-3b-q4_k_m` | Qwen 2.5 3B Instruct (Q4_K_M) | ~1.9 GB |
| `qwen2.5-7b-q4_k_m` | Qwen 2.5 7B Instruct (Q4_K_M) | ~4.7 GB |
| `mistral-7b-v0.3-q4_k_m` | Mistral 7B Instruct v0.3 (Q4_K_M) | ~4.4 GB |
| `phi-3-mini-q4_k_m` | Phi-3 Mini 4K Instruct (Q4_K_M) | ~2.4 GB |
| `gemma-2-2b-q4_k_m` | Gemma 2 2B IT (Q4_K_M) | ~1.6 GB |

Pinning specific shas means an HF account compromise can't push a doctored
GGUF through Hecate without us shipping a new release.

URL-paste path: accepts a direct GGUF URL only
(`https://huggingface.co/<repo>/resolve/<rev>/<file>.gguf`). Pasting a repo
URL surfaces a clear error explaining the user needs to copy the specific
file's download URL. Gated repos (HF auth required) surface a 401/403 with
"this model is gated; not supported in v1" — operator can still install it
manually through HuggingFace CLI and import the file (out of scope for v1
but the file-import path is what makes that work later).

### HTTP API

All under `/hecate/v1/local-models/`:

```text
GET    /catalog                           — curated entries + installed flag per entry
GET    /installed                         — registry rows
POST   /install                           — body: {model_id} or {url, sha256?}
                                            returns: {install_id}
GET    /install/{install_id}/events       — SSE: progress events
DELETE /install/{install_id}              — cancel in-flight install
DELETE /installed/{model_id}              — uninstall (deletes file + row)
POST   /runtime/start                     — body: {model_id}
POST   /runtime/stop                      — body: {}
GET    /runtime                           — status (idle/starting/running/failed)
GET    /runtime/events                    — SSE: state transitions, useful for UI
```

Install SSE events:

```text
event: progress    data: {bytes_downloaded, bytes_total, eta_seconds}
event: completed   data: {model_id, file_path, sha256}
event: failed      data: {error_kind, message, expected_sha256?, actual_sha256?}
```

Internal proxy (not part of the public API but in the same package):

```text
ANY    /hecate/internal/llamacpp/v1/{path:.+}
```

### UI surface

A new card in the Connections workspace, above the existing "Local runtimes"
group:

```text
┌─ Bundled model runtime ─────────────────────┐
│ [llama.cpp]    Idle · 0 models installed    │
│                                              │
│ Run open-weight models locally without       │
│ installing Ollama or LM Studio.              │
│                                              │
│           [ Browse models → ]                │
└──────────────────────────────────────────────┘
```

States:

- **Not available** — feature dormant. Card grayed out with explanation.
- **Idle, no models** — primary CTA "Browse models".
- **Idle, models installed** — list of installed models with Start buttons.
- **Loading X** — progress indicator, Stop button.
- **Running X** — model name + uptime + Stop button + Switch picker.

Clicking the card opens a SlideOver:

```text
┌─ Bundled model runtime ─────────────────────┐
│ ◐ Loading qwen2.5-7b-q4_k_m… (28% loaded)   │
│                                              │
│ Installed (2)                                │
│ ▸ qwen2.5-7b-q4_k_m  4.7 GB  ●Running       │
│ ▸ llama-3.2-3b-q4_k_m  2.0 GB ○Idle  [Start]│
│                                              │
│ Catalog                                      │
│ ┌────────────────────────────────────────┐  │
│ │ Llama 3.2 1B    770 MB  [Install]      │  │
│ │ Llama 3.2 3B    2.0 GB  [✓ Installed]  │  │
│ │ Qwen 2.5 0.5B   370 MB  [Install]      │  │
│ │ ...                                     │  │
│ └────────────────────────────────────────┘  │
│                                              │
│ Custom: Paste a HuggingFace GGUF URL         │
│ [ https://huggingface.co/...        ] [Add]  │
└──────────────────────────────────────────────┘
```

Pull-progress also surfaces as a transient pill in the global status bar so
it stays visible when the operator navigates away.

### OTel surface

New events under the `gateway` span tree (registered in
`internal/telemetry/contract.go` and documented in `docs/events.md`):

| Event | Required attributes |
|---|---|
| `local_model.install.started` | `hecate.local_model.id`, `hecate.local_model.source_url`, `hecate.local_model.install.bytes_total` |
| `local_model.install.progress` | `hecate.local_model.id`, `hecate.local_model.install.bytes_downloaded`, `hecate.local_model.install.bytes_total` (sampled) |
| `local_model.install.completed` | `hecate.local_model.id`, duration_ms, sha256 |
| `local_model.install.failed` | `hecate.local_model.id`, `hecate.error_kind`, `error.message`, expected/actual sha256 if mismatch |
| `local_model.runtime.starting` | `hecate.local_model.id`, `hecate.local_model.runtime.port`, `hecate.local_model.runtime.params.context_size` |
| `local_model.runtime.started` | `hecate.local_model.id`, ttft_ms (time to `/health` ok) |
| `local_model.runtime.stopped` | `hecate.local_model.id`, `hecate.local_model.runtime.reason` (operator/switch/error), uptime_ms |
| `local_model.runtime.crashed` | `hecate.local_model.id`, exit_code |
| `local_model.proxy.routed` | `hecate.local_model.id` (debug; per-request) |

New attribute namespace `hecate.local_model.*` — added to the requiredEventAttrs
map in `contract.go` alongside the existing GenAI + Hecate attributes.

New stable error codes (in `internal/api/error_mapping.go`):

- `local_models_unavailable` — feature dormant or binary not resolved.
- `local_model_not_installed` — proxied request specified a model that's not
  in the registry.
- `local_model_runtime_unavailable` — start failed, child crashed, or operator
  stopped during request.
- `local_model_install_already_running` — second install kicked off while one
  is in flight (v1 serializes installs).

## Decisions

### Bundled binary, single platform in v1

Tauri sidecar bundles the macOS arm64 Metal-enabled `llama-server` from a
pinned llama.cpp release tag, verified by sha256 at build time. Bundle-size
delta acceptable per operator direction. CPU-only Linux/Windows and Vulkan
acceleration follow when Tauri matrix expands.

Trade-off: gateway-only deployments (no Tauri) get nothing in v1. Acceptable
because there is no production headless distribution today.

### Single proxy provider, not per-model auto-registration

One `llamacpp` provider row, gateway-internal proxy fans out to the active
child by model id. Alternative considered: synthesize a provider row per
installed model. Rejected because it pollutes the provider table with rows
the operator didn't create, mixing operator intent with Hecate's
auto-management.

Trade-off: real reverse-proxy code to test (streaming, cancellation, header
pass-through), but small and bounded.

### One child at a time, restart on switch

Cold-load latency on switch (1–30 s) hidden behind a UI loading state.
Alternative considered: multiple children with LRU keep-warm. Rejected for
v1 because memory pressure detection and a sensible default-N require more
runway than this RFC.

Trade-off: switching models has noticeable latency. Documented; addressed in
a follow-up RFC.

### HuggingFace-only catalog, no re-hosted registry

Curated list of pinned HF GGUF URLs + paste-HF-URL path. Alternative
considered: re-host like Ollama. Rejected because we'd own a CDN bill and a
support surface for someone else's models.

Trade-off: dependent on HuggingFace's availability and the stability of the
chosen converter accounts. Mitigation: pinned shas + a thin
operator-supplied-URL escape hatch.

### Feature flag default

`HECATE_LOCAL_MODELS` env var; absent / on for Tauri builds, absent / off
for Go gateway builds. Tauri sidecar sets `HECATE_LOCAL_MODELS=on`
explicitly. Disabling the flag at runtime drops the API routes and hides
the UI card without deleting any downloaded files.

## Acceptance criteria

1. `/race` passes.
2. From a fresh `~/Library/Application Support/sh.hecate.app/`, the desktop
   app boots, the Connections card reads "Bundled model runtime · idle, 0
   models", and the SlideOver lists the curated catalog with each entry's
   size.
3. Installing the smallest catalog entry (Qwen2.5 0.5B Q4_K_M, ~370 MB)
   shows progress via SSE, completes inside 60 s on a 50 Mb/s link, the file
   appears at `<data_dir>/models/qwen2.5-0_5b-q4_k_m.gguf` with the
   catalog's sha256, and a corresponding `installed_models` row exists in
   both memory and sqlite.
4. The freshly-installed model appears in `GET /v1/models` without
   restarting the gateway, attributed to `provider: "llamacpp"` with
   `discovery_source: "local_model_registry"`.
5. Clicking "Start" loads the model in ≤30 s; runtime status reads
   `running`. Sending a chat completion that pins `model:
   "qwen2.5-0_5b-q4_k_m"` routes through the internal proxy and gets a
   non-empty streamed response.
6. Picking a *different* installed model triggers stop → start; the first
   chat after the switch waits in a "loading" state and eventually responds.
7. Stopping the runtime mid-request returns 503 with error code
   `local_model_runtime_unavailable`. A `local_model.runtime.stopped` span
   event records `reason=operator`.
8. Killing `Hecate.app` and relaunching: registry row intact, SlideOver
   shows the installed model, Start works without re-downloading.
9. Pasting a valid GGUF URL installs the file with HF's published sha256
   (if returned in the response headers) or with no sha (if absent — the
   operator opts in to unverified install via a checkbox).
10. Pasting an HF repo URL (not a direct GGUF) returns a clean error
    pointing at the right copy-URL workflow.
11. Pasting a gated-repo URL returns a clean "model is gated; not supported"
    error with no partial file left behind.
12. `docs/events.md` enumerates every new event with full attribute lists.
    `docs/local-models.md` (new) covers storage layout, binary resolution,
    env knobs, troubleshooting. `.env.example` updates with
    `HECATE_LOCAL_MODELS` and `HECATE_LLAMA_SERVER_BIN`.
13. UI: `LocalModelsCard.test.tsx` snapshot covers the five visible states
    (not-available, idle-empty, idle-with-models, loading, running). `bun
    run typecheck && bun run build` clean.
14. Migration: a Hecate install at the previous release upgraded to this
    release boots cleanly. SQLite auto-creates `installed_models`. Postgres
    migration applies forward without manual intervention.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| macOS bundle size balloons past acceptable threshold | Measure before merge. Operator has signed off on the +8–20 MB band; anything larger triggers a separate go/no-go. |
| `llama-server` crashes mid-stream | Proxy detects connection drop, emits `local_model.runtime.crashed` span event, returns 503 to caller. No auto-restart in v1 — operator clicks Start. |
| sha256 mismatch on download | Hard-fail the install, delete partial file, surface mismatch with both expected and actual values in the SSE error event. Don't auto-retry — could be supply-chain compromise. |
| Long downloads on slow networks block the UI | All install operations are SSE-streamed; the SlideOver and status-bar pill are non-modal. Cancel button issues `DELETE /install/{id}` which removes the partial file. |
| Port collision picking a free port | Same `free_port()` pattern Tauri uses for the gateway port. Failure returns a structured error pointing at runtime status. |
| HuggingFace down at install time | The install endpoint returns a network error; SSE `failed` event with `error_kind=network`. Operator retries. Curated catalog stays compiled-in so browse still works offline. |
| Operator already has a `llamacpp` provider configured | Boot reconciliation: log a structured warning, leave the existing row alone, skip auto-registration. Document the override in `docs/local-models.md`. |
| Gated HF repos look broken to the operator | Clean error message that explicitly says "gated; not supported in v1" so the failure isn't mistaken for a Hecate bug. Documented escape hatch (manual download + paste a `file://` URL) deferred to v2. |
| Bundle binary diverges from upstream llama.cpp's wire shape | Pinned release tag at build time; `llama-server --version` logged on each spawn for diagnostic correlation. |

## Migration and rollback

- **Env knobs.** New: `HECATE_LOCAL_MODELS` (default on for Tauri builds,
  default off otherwise), `HECATE_LLAMA_SERVER_BIN` (set by Tauri sidecar
  to the resolved bundled path; operator may override to point at a
  self-managed `llama-server`). `.env.example` ships with both documented.
- **Schema migration.** A new `InstalledModels` field on the
  controlplane `State` struct. The sqlite store persists `State` as a
  single JSON blob in a single row, so adding the field *is* the
  migration — no new table, no DDL. Memory store gains a
  corresponding slice + lock; the shared `applyUpsertInstalledModel` /
  `applyDeleteInstalledModel` helpers serve both backends. Pre-feature
  gateways read newer databases fine (extra field unmarshals as a nil
  slice). Post-feature
  gateways read pre-feature databases fine (table auto-created on first
  start).
- **Wire-shape compatibility.** No changes to provider-compatible `/v1/*`
  ingress. `/v1/models` gains entries when local models are installed —
  additive, no removed fields. New `/hecate/v1/local-models/*` routes.
- **Rollback path.** Set `HECATE_LOCAL_MODELS=off` and restart the gateway.
  API routes drop to 404 with no compatibility shim. Connections card
  hides. Already-downloaded model files remain on disk. Re-enabling
  restores the registry from disk + sqlite. Worst-case rollback to a
  pre-feature build leaves the `installed_models` table behind — harmless.
- **Retention.** New retention subsystem `local_models` ages out unused
  installed models when the operator opts in. Default: never auto-evict —
  the operator paid bandwidth for the download, Hecate doesn't delete it
  without explicit policy. Implemented in `internal/retention/retention.go`.

## Out of scope (reserved for v2 / follow-ups)

- HuggingFace browse / search panel.
- ~~Gated-repo support (HF token entry, model-card preview).~~
  Token-entry landed in v2 — `InstallSpec.hf_token` (per-install,
  not persisted) plus a `HUGGINGFACE_TOKEN` env fallback for
  headless. The installer attaches `Authorization: Bearer <token>`.
  Model-card preview is still TBD.
- ~~Multi-model resident runtime with LRU keep-warm.~~
  Landed in v2 — `HECATE_LOCAL_MODELS_MAX_RESIDENT=N` keeps the N
  most-recently-used models loaded; the (N+1)th `EnsureLoaded`
  evicts the coldest. Default 1 preserves v1 single-child behavior.
- CUDA / Vulkan / ROCm acceleration on Linux / Windows desktop bundles.
- Auto-update of the bundled `llama-server` binary independent of Hecate
  releases.
- Per-model fine-grained config (LoRA, draft model, speculative decoding,
  custom chat templates).
- ~~Headless / CLI lazy-download codepath (the dormant
  `<data_dir>/llamacpp/bin/` directory is reserved for this).~~
  Landed in v2 — `HECATE_LOCAL_MODELS_LAZY_DOWNLOAD=on` triggers an
  on-demand fetch from the pinned upstream release; the binary
  caches at `<data_dir>/llamacpp/bin/llama-server` across runs. See
  `internal/llamacpp/binary_resolver.go`.
- MLX or other non-llama.cpp local runtimes. The
  `/hecate/v1/local-models/*` URL prefix is engine-agnostic; a future MLX
  engine would join the same namespace.

## Implementation hand-off

After this RFC is accepted, work parallelizes:

- [`hecate-backend`](../../docs-ai/skills/backend/SKILL.md) — runtime
  package, controlplane Store additions (memory + sqlite, postgres
  when the repo grows it), HTTP handlers, OTel events, error codes.
- [`hecate-tauri`](../../docs-ai/skills/tauri/SKILL.md) — sidecar
  bundling, build-time `llama-server` fetch + verify, env-var wiring.
- [`hecate-ui`](../../docs-ai/skills/ui/SKILL.md) — Connections card,
  SlideOver, install-progress SSE consumer, status-bar pill, model
  picker integration.
- [`hecate-devops`](../../docs-ai/skills/devops/SKILL.md) — `.env.example`
  update, `docs/events.md` registry, `docs/local-models.md`, release-note
  draft, bundle-size measurement.

Each skill's verification ladder applies as documented. The
[`hecate-tester`](../../docs-ai/skills/tester/SKILL.md) skill produces the
coverage matrix at the end.
