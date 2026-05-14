# Local Models — Hecate-managed llama.cpp runtime

> Status: alpha (v1). Implemented and shipping in Hecate desktop builds.
> Design contract: [RFC — Local Models](rfcs/local-models-llamacpp.md).

Hecate desktop bundles `llama-server` from upstream
[llama.cpp](https://github.com/ggml-org/llama.cpp) and uses it to run
open-weight models locally, without an external runtime (Ollama, LM
Studio, etc.) installed first. Operators browse a curated list of
GGUF models on HuggingFace, install one with a click, and the file is
streamed to disk, verified, and registered with the chat composer's
model picker automatically.

This document covers the operator-facing surface: storage layout,
binary resolution, env knobs, and troubleshooting. The architectural
contract lives in [the RFC](rfcs/local-models-llamacpp.md).

## When the feature is available

The feature is **dormant** unless `HECATE_LLAMA_SERVER_BIN` points at
an executable `llama-server` binary at gateway boot. Dormant means:

- `/hecate/v1/local-models/*` endpoints return `503 local_models_unavailable`,
  except `/runtime` which returns `200` with `availability.available=false`
  so the UI can render the "not bundled" card without a second probe.
- `/v1/models` does not include local-model entries.
- The `llamacpp` provider is not auto-registered.
- The Connections card surfaces a dashed "Not bundled" tile with a
  short explanation.

Tauri's desktop sidecar startup sets `HECATE_LLAMA_SERVER_BIN`
automatically when the bundled binary resolves at the standard
externalBin path (`binaries/llama-server-<triple>`). The headless Go
gateway leaves it unset in v1 — there is no CLI auto-download path
yet.

To opt in explicitly from a hand-built gateway:

```sh
export HECATE_LLAMA_SERVER_BIN=/path/to/llama-server
export HECATE_LOCAL_MODELS=on   # optional: forces the feature on even when the path is empty
hecate
```

The HECATE_LOCAL_MODELS knob is mostly diagnostic — in practice setting
HECATE_LLAMA_SERVER_BIN to an executable path is enough.

## Storage layout

Everything Hecate manages lives under `GATEWAY_DATA_DIR` (default
`.data`, or the OS-standard app-data dir under Tauri).

```
GATEWAY_DATA_DIR/
├── hecate.runtime.json              # existing — Tauri sidecar runtime state
├── gateway.log                      # existing — gateway stderr
├── hecate.sqlite                    # existing — sqlite control plane
├── models/                          # new in this release
│   ├── qwen2.5-7b-instruct-q4_k_m.gguf
│   ├── llama-3.2-3b-instruct-q4_k_m.gguf
│   └── …
└── llamacpp/
    └── bin/                         # reserved for v2 (CLI lazy-download path);
                                     # unused in v1 desktop builds
```

The GGUF file on disk is the source of truth. A `installed_models` row
in the sqlite control plane stores enrichment (HuggingFace URL, sha256,
recommended context, last-loaded time). At boot — and on each
`GET /hecate/v1/local-models/installed` call — rows whose file vanished
are dropped from the registry; files that exist without a row are
**not** auto-imported (no provenance, no sha).

## How models reach the chat composer

When a model finishes installing:

1. Installer writes `<data_dir>/models/<slug>.gguf` atomically (writes
   to `<slug>.gguf.part`, fsyncs, renames).
2. Installer upserts the `installed_models` row.
3. The single auto-registered `llamacpp` provider's `BaseURL` points at
   the gateway-internal proxy at
   `http://<gateway>/hecate/internal/llamacpp/v1`.
4. The next `GET /v1/models` aggregation walks the registry and emits
   one entry per installed model:
   ```json
   {
     "id": "qwen2.5-7b-instruct-q4_k_m",
     "owned_by": "llamacpp",
     "metadata": {
       "provider": "llamacpp",
       "provider_kind": "local",
       "discovery_source": "local_model_registry",
       "display_name": "Qwen 2.5 7B Instruct (Q4_K_M)",
       "size_bytes": 4682380000,
       "loaded": false,
       "capabilities": {
         "tool_calling": "none",
         "streaming": true,
         "max_context_tokens": 131072
       }
     }
   }
   ```
5. The chat composer's model picker picks them up on its next refresh.
   No gateway restart required.

The `loaded` flag is true for whichever model the runtime currently
has resident. Most community GGUFs are flagged
`tool_calling: "none"` conservatively — operators can override via
`/hecate/v1/model-capabilities/overrides` if they have evidence a model
behaves.

## Runtime lifecycle

v1 keeps **one model loaded at a time**. Switching models stops the
active `llama-server` child before starting a new one — there's a
cold-load latency (1–30 s depending on model size) on the first
request after a switch.

States:

| State | What it means |
|---|---|
| `idle` | No child running. Sending a chat to a local model triggers a load. |
| `starting` | Child spawned, `/health` polling. UI surfaces a loading state. |
| `running` | Child healthy. Requests proxy through. |
| `stopping` | Operator requested stop or a switch is in flight. |
| `failed` | Last start failed or the child crashed. Surfaces `last_error` until the next start. |

Crashes are not auto-restarted in v1 — the operator clicks Start
again. This keeps a misconfigured model from looping into the same
failure on every page-load.

## Catalog

The catalog is a compiled-in list of pinned HuggingFace GGUF download
URLs sourced from
[bartowski's](https://huggingface.co/bartowski) and
[lmstudio-community's](https://huggingface.co/lmstudio-community)
converter accounts — the same accounts LM Studio defaults to.

v1 entries:

- Llama 3.2 1B / 3B (Q4_K_M)
- Qwen 2.5 0.5B / 3B / 7B (Q4_K_M)
- Mistral 7B Instruct v0.3 (Q4_K_M)
- Phi-3 mini 4K (Q4_K_M)
- Gemma 2 2B IT (Q4_K_M)

Each entry pins a specific quantization variant. The catalog ships
without sha256 values during early bring-up — when present, the
installer hard-fails on mismatch (supply-chain protection). Without a
pin, the installer logs a warning and accepts the download.

Operators can install models outside the catalog via the **paste-URL**
path in the SlideOver. v1 accepts direct GGUF download URLs only —
repo-page URLs and tree URLs return a clear error pointing at the
copy-the-file-URL workflow. Gated repos (HF auth required) surface a
"not supported in v1" error.

HuggingFace browse / search is reserved for v2.

## HTTP API

| Method | Path | Notes |
|---|---|---|
| `GET` | `/hecate/v1/local-models/catalog` | Curated entries with per-entry `installed` flag |
| `GET` | `/hecate/v1/local-models/installed` | Boot-reconciled registry |
| `POST` | `/hecate/v1/local-models/install` | Body: `{catalog_id}` or `{url, sha256?}`. Returns `{install_id}`. |
| `GET` | `/hecate/v1/local-models/install/{id}/events` | SSE stream of `ProgressEvent`s |
| `DELETE` | `/hecate/v1/local-models/install/{id}` | Cancel the in-flight install |
| `DELETE` | `/hecate/v1/local-models/installed/{model_id}` | Uninstall + remove file |
| `GET` | `/hecate/v1/local-models/runtime` | Availability + state snapshot |
| `POST` | `/hecate/v1/local-models/runtime/start` | Body: `{model_id}`. Blocks until running or failed. |
| `POST` | `/hecate/v1/local-models/runtime/stop` | Idempotent |
| `ANY` | `/hecate/internal/llamacpp/v1/{path...}` | Internal — gateway → llama-server reverse-proxy |

Stable error codes:

| Code | Status | Meaning |
|---|---|---|
| `local_models_unavailable` | 503 | Feature is dormant in this build |
| `local_model_not_installed` | 404 | Requested model id is not in the registry |
| `local_model_runtime_unavailable` | 503 | Runtime is not running or failed mid-request |
| `local_model_install_already_running` | 409 | Another install is in flight; v1 serializes |
| `local_model_install_not_found` | 404 | Cancel/events for an unknown install id |

## Env knobs

| Variable | Default | Effect |
|---|---|---|
| `HECATE_LLAMA_SERVER_BIN` | unset | Absolute path to the `llama-server` binary. Setting it activates the feature. Tauri sidecar startup sets it automatically when the bundled binary resolves. |
| `HECATE_LOCAL_MODELS` | unset (off) | When `on`, the feature initializes even if `HECATE_LLAMA_SERVER_BIN` is empty — useful for letting the gateway boot without a usable binary so the dormant `/runtime` shape can be inspected. |
| `HECATE_LOCAL_MODELS_LAZY_DOWNLOAD` | unset (off) | Headless / dev gateways opt into a one-time `llama-server` download from the pinned upstream llama.cpp release. The binary lands at `<data_dir>/llamacpp/bin/llama-server` (chmod +x, atomic rename) and is cached across runs. Tauri builds leave this OFF — they bundle the binary directly via externalBin. Downloads sha-verify when the pin carries a digest; a mismatch hard-fails. |
| `HECATE_LOCAL_MODELS_MAX_RESIDENT` | `1` | LRU keep-warm cap. With the default the runtime acts like v1 (single child, restart-on-switch). Bumping to N keeps the N most-recently-used models resident; the (N+1)th `EnsureLoaded` evicts the coldest. Operators set this based on free RAM; Hecate does not auto-tune. |
| `GATEWAY_DATA_DIR` | `.data` (gateway default) / app-data dir (Tauri) | Root for `models/` and `llamacpp/bin/`. |

The Tauri sidecar passes `HECATE_LLAMA_SERVER_BIN` and
`HECATE_LOCAL_MODELS=on` to the gateway child whenever it resolves a
bundled binary. Operators don't need to manage these manually on a
desktop install.

## Operator override of the auto-registered provider

At boot, the gateway upserts a single provider row with
`preset_id="llamacpp"` and `BaseURL` pointing at the internal proxy.
If the operator already created their own `llamacpp` preset provider —
typically because they were self-managing llama.cpp before Hecate
bundled it — the auto-registration logs a warning and **leaves the
existing row alone**:

```
llamacpp provider already exists; operator override retained
```

The operator's row keeps working as before. To switch to the
Hecate-managed path, remove the operator row through Connections; the
next boot will create the auto-managed one with the gateway-internal
proxy URL.

## Telemetry

Three OTel spans cover the runtime + install + proxy surface:

- `local_model.install` — per install, started on POST /install, ended
  on completed / failed / cancelled. Carries source URL, byte counts,
  sha256 mismatch detail.
- `local_model.runtime` — per running session, from EnsureLoaded to
  stop / crash. Carries port, PID, context size, time-to-first-healthy,
  uptime, and exit code on crash.
- `local_model.proxy.routed` — per proxied chat completion request.

Full attribute table and event names live in
[`docs/telemetry.md` → Local Models Spans](telemetry.md#local-models-spans).
Stable error codes are in
[`internal/api/response.go`](../internal/api/response.go) and
documented in [the API table](#http-api) above.

## Troubleshooting

### "Bundled model runtime — Not bundled"

`HECATE_LLAMA_SERVER_BIN` is unset or the path doesn't point at an
executable file. On a desktop install, this means the Tauri externalBin
resolution failed — check `tauri/src-tauri/binaries/llama-server-<your-triple>`
exists in the staged bundle. Operators rebuilding from source must run
`bun scripts/fetch-llama-server.ts` before `bun run tauri build`.

### Install hangs / SSE never connects

The SSE stream is the catch-up channel — install runs even with no
subscriber connected. Refresh the SlideOver to re-subscribe. Terminal
events (completed / failed / cancelled) stay in the fanout for 60 s
after the install finishes so a tab reload still sees the outcome.

### sha256 mismatch during install

```
{"kind":"failed","error_kind":"sha_mismatch","expected_sha256":"…","actual_sha256":"…"}
```

The downloaded file did not match the expected digest. The partial
file is removed and no registry row is created. Don't blindly retry —
investigate the source first (the catalog entry may be stale, or the
HF account may have been compromised). When you trust the new file,
update the catalog entry's sha256 in
`internal/config/llamacpp_catalog.go` and rebuild.

### Runtime stays "starting" forever

The cold-load deadline is 30 s. Past that, the runtime transitions to
`failed` with `wait for health: context deadline exceeded`. A very
slow disk or a wildly oversized context for the available RAM can both
cause this. Stop, lower `recommended_context` for the model (operator
override after install), and try again. Or, for large models on
memory-constrained machines: pick a smaller quant.

### Model isn't in `/v1/models` after install

Confirm the file exists on disk at the expected path. If the file is
gone but the model still shows in the SlideOver, the registry row is
stale — refreshing `GET /hecate/v1/local-models/installed` triggers
reconciliation and drops the orphaned row. After that the next
`GET /v1/models` will be clean.

## Out of scope (v1)

- Linux / Windows desktop bundles — v1 is macOS arm64 only.
- CUDA / Vulkan / ROCm acceleration.
- HuggingFace browse / search panel (curated + paste-URL covers v1).
- Gated HuggingFace repos.
- Multi-model resident runtime (LRU keep-warm).
- Per-model fine-grained config (LoRA, draft model, speculative
  decoding, custom chat templates).
- Headless gateway lazy-download — `<data_dir>/llamacpp/bin/` is
  reserved for this in a future release.

Each of these has a parking lot in
[the RFC's "Out of scope" section](rfcs/local-models-llamacpp.md#out-of-scope-reserved-for-v2--follow-ups).
