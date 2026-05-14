// Package llamacpp owns the Hecate-managed local-model subsystem: it
// drives the bundled llama-server binary, tracks GGUF model files
// under <data_dir>/models/, and exposes them through a gateway-internal
// proxy that fronts a single auto-registered "llamacpp" provider.
//
// The package is structured as four collaborating concerns:
//
//   - Catalog   — the compiled-in list of recommended HuggingFace GGUFs
//     plus the operator's "paste a URL" path.
//   - Installer — downloads a GGUF, verifies sha256 (when known),
//     streams progress to subscribers.
//   - Runtime   — supervises at most one llama-server child at a time;
//     restarts on model switch.
//   - Proxy     — minimal reverse-proxy that forwards OpenAI-compat
//     requests to the active child.
//
// All four are wired together by a top-level Service which the api
// package mounts at /hecate/v1/local-models/* and the internal proxy
// at /hecate/internal/llamacpp/v1/*. The feature is dormant unless
// HECATE_LLAMA_SERVER_BIN points at an executable llama-server binary;
// Tauri's sidecar startup sets this for desktop builds, and the Go
// gateway alone leaves it unset (no v1 lazy-download path).
//
// See docs/rfcs/local-models-llamacpp.md for the accepted shape and the
// trade-offs being accepted (single-child runtime, restart-on-switch,
// HuggingFace-as-registry, no re-hosted CDN).
package llamacpp

import (
	"time"

	"github.com/hecate/agent-runtime/internal/controlplane"
)

// Capabilities is a type alias over controlplane's persisted shape.
// Kept here for ergonomic imports — UI code and HTTP handlers reach
// for llamacpp.Capabilities, but it round-trips losslessly through
// the controlplane store.
type Capabilities = controlplane.InstalledModelCapabilities

// InstalledModel is the persisted record describing a GGUF on disk.
// Aliased to controlplane.InstalledModel so the llamacpp package
// stays the public-facing import without duplicating the schema.
type InstalledModel = controlplane.InstalledModel

// CatalogEntry is one curated GGUF the operator can install with a
// click. The set is compiled into the gateway binary (see catalog.go).
// Operators can install models outside this list via the paste-URL
// path on the install endpoint.
type CatalogEntry struct {
	// ID is the stable slug used everywhere — registry rows, the
	// chat composer's model picker, /v1/models entries, OTel
	// attributes. Lowercase, hyphen-separated, includes the quant
	// suffix so two quants of the same model can coexist.
	ID string `json:"id"`

	// DisplayName is the human-readable label shown in the catalog
	// browser and the chat composer model picker.
	DisplayName string `json:"display_name"`

	// Description is one or two short sentences, intent on what the
	// model is good for. Surface text only, no policy implications.
	Description string `json:"description,omitempty"`

	// HuggingFaceURL is the *direct* GGUF download URL — not the
	// repo page. Format:
	//     https://huggingface.co/<repo>/resolve/<rev>/<file>.gguf
	// Pinned at compile time so a converter account compromise can't
	// silently swap in a doctored file under our nose.
	HuggingFaceURL string `json:"huggingface_url"`

	// SHA256 is the lowercase hex digest of the GGUF file. Optional
	// during early bring-up: when empty, the installer logs a warning
	// and accepts the download as-is. Empty entries are flagged in
	// catalog_test.go via a TODO list so they don't ship empty in a
	// stable release.
	SHA256 string `json:"sha256,omitempty"`

	// SizeBytes is the on-disk size after download. Used to pre-show
	// a download bar even before HuggingFace responds with a
	// Content-Length, and to gate "do I have enough disk" checks.
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// RecommendedContext is the n_ctx llama-server should be started
	// with for this model. Each GGUF has a hard max, but most models
	// behave better with a smaller window than the absolute cap.
	RecommendedContext int `json:"recommended_context,omitempty"`

	// Capabilities flags what Hecate's chat composer should let the
	// operator do with the model. Most community GGUFs are "none"
	// for tool calling.
	Capabilities Capabilities `json:"capabilities,omitempty"`

	// License is an SPDX-style hint surfaced in the catalog browser.
	// Not enforced — operators read it themselves.
	License string `json:"license,omitempty"`
}

// RuntimeState enumerates the lifecycle states observable through the
// /hecate/v1/local-models/runtime endpoint. See the RFC for the
// transition table.
type RuntimeState string

const (
	// RuntimeIdle: no llama-server child is running. The proxy
	// returns local_model_runtime_unavailable for any inbound
	// request unless start was triggered.
	RuntimeIdle RuntimeState = "idle"

	// RuntimeStarting: child has been spawned, /health is being
	// polled. Proxy requests block on this state until the child
	// is ready (or fail after the cold-load deadline).
	RuntimeStarting RuntimeState = "starting"

	// RuntimeRunning: child is healthy, accepting requests.
	RuntimeRunning RuntimeState = "running"

	// RuntimeStopping: operator requested stop or model switch.
	// Kill signal sent; waiting for exit.
	RuntimeStopping RuntimeState = "stopping"

	// RuntimeFailed: last start attempt failed, or the child
	// crashed without an operator stop. Surfaces the failure
	// until the next start is requested.
	RuntimeFailed RuntimeState = "failed"
)

// RuntimeStatus is the snapshot returned by GET /runtime. The shape
// is deliberately wide so the UI doesn't need a second roundtrip to
// render a useful state.
type RuntimeStatus struct {
	State RuntimeState `json:"state"`

	// ActiveModelID is non-empty in states starting / running /
	// stopping. It is left set after a crash so the operator's
	// "what was loaded?" question has an answer; cleared on the
	// next successful start of a different model.
	ActiveModelID string `json:"active_model_id,omitempty"`

	// Port is the loopback port the live child is bound to. Non-zero
	// only while the state is starting / running. The proxy
	// forwards there; surfaced for diagnostics.
	Port int `json:"port,omitempty"`

	// PID of the child process. Diagnostic only.
	PID int `json:"pid,omitempty"`

	// StartedAt marks the most recent successful transition to
	// running. Used to render uptime in the UI.
	StartedAt time.Time `json:"started_at,omitempty"`

	// LastError carries the operator-facing failure message when
	// state is failed. Format is short, lowercase, terminal-free —
	// e.g. "child exited with code 134".
	LastError string `json:"last_error,omitempty"`

	// LastErrorAt timestamps LastError so the UI can decide when
	// to dismiss a stale failure banner.
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// InstallSpec is the input shape for POST /install. Exactly one of
// CatalogID or URL must be non-empty.
type InstallSpec struct {
	// CatalogID picks a curated entry. The installer resolves the
	// rest (URL, sha, etc.) from the compiled-in catalog.
	CatalogID string `json:"catalog_id,omitempty"`

	// URL is a direct GGUF download URL. The slug is derived from
	// the filename; sha256 verification is skipped unless the
	// optional SHA256 field is set.
	URL string `json:"url,omitempty"`

	// SHA256 lets a paste-URL install assert a known digest. Empty
	// means "trust the download" — the installer logs a warning.
	SHA256 string `json:"sha256,omitempty"`

	// HFToken carries a HuggingFace Hub auth token for gated repos
	// (Meta's official Llama checkpoints, Google's official Gemma,
	// etc.). The installer attaches it as an Authorization: Bearer
	// header on the download request. When unset, the installer
	// falls back to the HUGGINGFACE_TOKEN env var if present.
	//
	// Not persisted: the token lives in the request and on the
	// SSE stream only — encrypted at-rest storage is a future
	// follow-up. Operators on gated repos pass the token per
	// install; the headless gateway can also pick it up from
	// HUGGINGFACE_TOKEN in the environment.
	HFToken string `json:"hf_token,omitempty"`
}

// ProgressKind enumerates the SSE event types streamed from the
// install endpoint.
type ProgressKind string

const (
	ProgressStarted   ProgressKind = "started"
	ProgressProgress  ProgressKind = "progress"
	ProgressCompleted ProgressKind = "completed"
	ProgressFailed    ProgressKind = "failed"
	// ProgressCancelled is fired when DELETE /install/{id} arrives
	// while the download is in flight. The partial file is removed
	// before this event is emitted.
	ProgressCancelled ProgressKind = "cancelled"
)

// ProgressEvent is the unit emitted on the install SSE stream. Each
// event carries the full state needed to render a UI without keeping
// per-stream state on the client — useful when the operator
// reconnects mid-install after a tab reload.
type ProgressEvent struct {
	Kind ProgressKind `json:"kind"`

	// ModelID is the slug being installed.
	ModelID string `json:"model_id,omitempty"`

	// BytesDownloaded / BytesTotal carry the running progress. Total
	// may be zero if the source did not provide a Content-Length;
	// the UI falls back to a spinner in that case.
	BytesDownloaded int64 `json:"bytes_downloaded,omitempty"`
	BytesTotal      int64 `json:"bytes_total,omitempty"`

	// SHA256 is set on the completed event so the UI can show the
	// digest if the operator wants to verify it manually.
	SHA256 string `json:"sha256,omitempty"`

	// ExpectedSHA256 / ActualSHA256 are populated on a failed event
	// caused by sha mismatch so the operator has both values to
	// compare without grepping logs.
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	ActualSHA256   string `json:"actual_sha256,omitempty"`

	// ErrorKind is one of "network", "sha_mismatch", "cancelled",
	// "disk", "gated", "invalid_url", "unknown". The stable code
	// the UI maps to a recovery hint.
	ErrorKind string `json:"error_kind,omitempty"`

	// Message is a human-readable description of the failure.
	// Never carries secrets; safe to log.
	Message string `json:"message,omitempty"`

	// EmittedAt timestamps the event server-side.
	EmittedAt time.Time `json:"emitted_at"`
}

// FeatureAvailability summarizes whether the local-models feature is
// usable in the current build. Returned by the runtime status endpoint
// when the operator first opens the Connections card, so the UI can
// render "not available in this build" without probing every endpoint.
type FeatureAvailability struct {
	// Available is true when both the feature flag is on and the
	// llama-server binary resolved to an executable file.
	Available bool `json:"available"`

	// Reason is non-empty when Available is false. One of
	// "flag_off", "binary_not_found", "binary_not_executable".
	Reason string `json:"reason,omitempty"`

	// BinaryPath is the resolved llama-server path when Available
	// is true. Diagnostic surface only.
	BinaryPath string `json:"binary_path,omitempty"`
}
