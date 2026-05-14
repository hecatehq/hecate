package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hecate/agent-runtime/internal/llamacpp"
)

// Response envelopes for /hecate/v1/local-models/*. The shapes follow
// the existing "object + data" convention used by other Hecate-native
// listings so the UI's existing fetch helpers can decode them
// without per-endpoint glue.

type LocalModelsCatalogResponse struct {
	Object string                            `json:"object"`
	Data   []LocalModelsCatalogResponseEntry `json:"data"`
}

type LocalModelsCatalogResponseEntry struct {
	llamacpp.CatalogEntry
	Installed bool `json:"installed"`
}

type LocalModelsInstalledResponse struct {
	Object string                    `json:"object"`
	Data   []llamacpp.InstalledModel `json:"data"`
}

type LocalModelsRuntimeResponse struct {
	Object       string                       `json:"object"`
	State        llamacpp.RuntimeState        `json:"state"`
	Available    bool                         `json:"available"`
	Reason       string                       `json:"reason,omitempty"`
	BinaryPath   string                       `json:"binary_path,omitempty"`
	Active       *llamacpp.RuntimeStatus      `json:"active,omitempty"`
	Availability llamacpp.FeatureAvailability `json:"availability"`
}

type LocalModelsInstallResponse struct {
	Object    string `json:"object"`
	InstallID string `json:"install_id"`
	ModelID   string `json:"model_id"`
}

type localModelsInstallRequest struct {
	CatalogID string `json:"catalog_id,omitempty"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	// HFToken is the HuggingFace Hub access token for gated repos.
	// Not persisted by the gateway — the token rides the request
	// only. Operators with a gated download paste the token into
	// the UI alongside the URL; CI / headless users prefer the
	// HUGGINGFACE_TOKEN env var on the gateway host.
	HFToken string `json:"hf_token,omitempty"`
}

type localModelsRuntimeStartRequest struct {
	ModelID string `json:"model_id"`
}

// localModelsService is the small contract the handlers reach for —
// nil-checking once here keeps each handler's preamble tight.
func (h *Handler) localModelsService(w http.ResponseWriter) (*llamacpp.Service, bool) {
	if h.localModels == nil {
		WriteError(w, http.StatusServiceUnavailable, errCodeLocalModelsUnavailable,
			"local model runtime is not available in this build")
		return nil, false
	}
	return h.localModels, true
}

// HandleLocalModelsCatalog — GET /hecate/v1/local-models/catalog
// Returns the curated entries with a per-entry `installed` flag so
// the UI can render Install / Installed badges without a second
// roundtrip.
func (h *Handler) HandleLocalModelsCatalog(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	installed, err := svc.ListInstalled(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	installedIDs := make(map[string]struct{}, len(installed))
	for _, m := range installed {
		installedIDs[m.ID] = struct{}{}
	}

	entries := svc.Catalog().Entries()
	out := make([]LocalModelsCatalogResponseEntry, 0, len(entries))
	for _, entry := range entries {
		_, installedFlag := installedIDs[entry.ID]
		out = append(out, LocalModelsCatalogResponseEntry{
			CatalogEntry: entry,
			Installed:    installedFlag,
		})
	}
	WriteJSON(w, http.StatusOK, LocalModelsCatalogResponse{
		Object: "local_models.catalog",
		Data:   out,
	})
}

// HandleLocalModelsInstalled — GET /hecate/v1/local-models/installed
// Boot-reconciled list of installed models. Drops registry rows
// whose .gguf vanished on disk before returning.
func (h *Handler) HandleLocalModelsInstalled(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	rows, err := svc.ListInstalled(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, LocalModelsInstalledResponse{
		Object: "local_models.installed",
		Data:   rows,
	})
}

// HandleLocalModelsInstall — POST /hecate/v1/local-models/install
// Resolves the spec, kicks off the install on a background goroutine,
// returns {install_id, model_id}. Progress is streamed via
// GET /install/{install_id}/events. v1 serializes installs — a
// second call while another is in flight returns 409
// local_model_install_already_running.
func (h *Handler) HandleLocalModelsInstall(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	var req localModelsInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	handle, err := svc.Installer().Install(r.Context(), llamacpp.InstallSpec{
		CatalogID: req.CatalogID,
		URL:       req.URL,
		SHA256:    req.SHA256,
		HFToken:   req.HFToken,
	})
	if err != nil {
		writeInstallError(w, err)
		return
	}
	// Detach the events channel via a fan-out: a background drainer
	// keeps the install alive even if no SSE client connects. The
	// SSE handler subscribes to the same fan-out by install id. v1
	// supports a single subscriber (the UI), and the drainer fills
	// in the gap when the operator closes the SlideOver mid-download.
	h.localModels.Installer().AttachInstall(handle)
	WriteJSON(w, http.StatusAccepted, LocalModelsInstallResponse{
		Object:    "local_models.install",
		InstallID: handle.InstallID,
		ModelID:   handle.ModelID,
	})
}

// HandleLocalModelsInstallEvents — GET /hecate/v1/local-models/install/{install_id}/events
// SSE stream of ProgressEvents for the specified install. The stream
// closes when the install reaches a terminal state (completed /
// failed / cancelled).
func (h *Handler) HandleLocalModelsInstallEvents(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	installID := r.PathValue("install_id")
	if installID == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "install_id is required")
		return
	}
	events, ok := svc.Installer().Subscribe(installID)
	if !ok {
		WriteError(w, http.StatusNotFound, errCodeLocalModelInstallNotFound, "install not found")
		return
	}
	streamSSE(w, r, events)
}

// HandleLocalModelsCancelInstall — DELETE /hecate/v1/local-models/install/{install_id}
// Cancels the in-flight install. Returns 200 on success, 404 when no
// install matches.
func (h *Handler) HandleLocalModelsCancelInstall(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	installID := r.PathValue("install_id")
	if err := svc.Installer().Cancel(installID); err != nil {
		if errors.Is(err, llamacpp.ErrInstallNotFound) {
			WriteError(w, http.StatusNotFound, errCodeLocalModelInstallNotFound, "install not found")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"object": "local_models.install.cancelled"})
}

// HandleLocalModelsUninstall — DELETE /hecate/v1/local-models/installed/{model_id}
// Removes the file from disk and the registry row. If the runtime
// is currently running this model it is stopped first.
func (h *Handler) HandleLocalModelsUninstall(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	modelID := r.PathValue("model_id")
	if strings.TrimSpace(modelID) == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "model_id is required")
		return
	}
	if err := svc.Uninstall(r.Context(), modelID); err != nil {
		if errors.Is(err, llamacpp.ErrInstalledModelNotFound) {
			WriteError(w, http.StatusNotFound, errCodeLocalModelNotInstalled, "model not installed")
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"object": "local_models.uninstalled"})
}

// HandleLocalModelsRuntimeStatus — GET /hecate/v1/local-models/runtime
// Returns the merged availability + runtime state snapshot. UI uses
// this on first render and after operator-driven transitions.
func (h *Handler) HandleLocalModelsRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if h.localModels == nil {
		// Honour the dormant-feature shape: surface availability=false
		// instead of erroring, so the UI can render "not available in
		// this build" without a second probe.
		WriteJSON(w, http.StatusOK, LocalModelsRuntimeResponse{
			Object:       "local_models.runtime",
			State:        llamacpp.RuntimeIdle,
			Available:    false,
			Reason:       "binary_not_found",
			Availability: llamacpp.FeatureAvailability{Available: false, Reason: "binary_not_found"},
		})
		return
	}
	fa := h.localModels.FeatureAvailability()
	status := h.localModels.Runtime().Status()
	out := LocalModelsRuntimeResponse{
		Object:       "local_models.runtime",
		State:        status.State,
		Available:    fa.Available,
		Reason:       fa.Reason,
		BinaryPath:   fa.BinaryPath,
		Availability: fa,
	}
	if status.State != llamacpp.RuntimeIdle {
		statusCopy := status
		out.Active = &statusCopy
	}
	WriteJSON(w, http.StatusOK, out)
}

// HandleLocalModelsRuntimeStart — POST /hecate/v1/local-models/runtime/start
// {model_id} required. Calls Runtime.EnsureLoaded. Blocking — returns
// when the runtime reaches RuntimeRunning or fails. UI shows a
// loading state during the cold-load.
func (h *Handler) HandleLocalModelsRuntimeStart(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	var req localModelsRuntimeStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, fmt.Sprintf("decode request: %v", err))
		return
	}
	if strings.TrimSpace(req.ModelID) == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "model_id is required")
		return
	}
	if _, err := svc.Runtime().EnsureLoaded(r.Context(), req.ModelID); err != nil {
		writeRuntimeError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, LocalModelsRuntimeResponse{
		Object:       "local_models.runtime",
		State:        llamacpp.RuntimeRunning,
		Available:    true,
		Availability: svc.FeatureAvailability(),
	})
}

// HandleLocalModelsHFSearch — GET /hecate/v1/local-models/huggingface/search
// Server-side proxy to HF's `/api/models` so the browser doesn't have
// to deal with CORS and the operator's HF token never leaves the
// gateway. Query string passthrough: q (search term), limit, token.
// v2 keeps the filter pinned to "gguf" inside the client; operators
// can't disable it.
func (h *Handler) HandleLocalModelsHFSearch(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		// Env fallback — same pattern the installer uses.
		token = strings.TrimSpace(os.Getenv("HUGGINGFACE_TOKEN"))
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			limit = v
		}
	}
	results, err := svc.HuggingFace().SearchModels(r.Context(), q, token, limit)
	if err != nil {
		writeHFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "local_models.huggingface.search",
		"data":   results,
	})
}

// HandleLocalModelsHFRepoFiles — GET /hecate/v1/local-models/huggingface/repos/{owner}/{name}
// Returns the GGUF files in the named HF repo with their LFS metadata
// (sha256 + size). Each file carries a pre-computed DownloadURL the
// install endpoint accepts as-is.
func (h *Handler) HandleLocalModelsHFRepoFiles(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	owner := r.PathValue("owner")
	name := r.PathValue("name")
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(name) == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "owner and name are required")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("HUGGINGFACE_TOKEN"))
	}
	files, err := svc.HuggingFace().ListRepoFiles(r.Context(), owner+"/"+name, token)
	if err != nil {
		writeHFError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "local_models.huggingface.files",
		"data":   files,
	})
}

func writeHFError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llamacpp.ErrHuggingFaceGated):
		WriteError(w, http.StatusForbidden, errCodeHuggingFaceGated, err.Error())
	case errors.Is(err, llamacpp.ErrHuggingFaceNotFound):
		WriteError(w, http.StatusNotFound, errCodeHuggingFaceNotFound, err.Error())
	default:
		WriteError(w, http.StatusBadGateway, errCodeHuggingFaceUpstream, err.Error())
	}
}

// HandleLocalModelsRuntimeStop — POST /hecate/v1/local-models/runtime/stop
// Idempotent. Body is ignored.
func (h *Handler) HandleLocalModelsRuntimeStop(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.localModelsService(w)
	if !ok {
		return
	}
	if err := svc.Runtime().Stop(r.Context()); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, LocalModelsRuntimeResponse{
		Object:       "local_models.runtime",
		State:        llamacpp.RuntimeIdle,
		Available:    true,
		Availability: svc.FeatureAvailability(),
	})
}

// streamSSE pumps a ProgressEvent channel to a text/event-stream
// connection. Closes when the channel closes or the client disconnects.
func streamSSE(w http.ResponseWriter, r *http.Request, events <-chan llamacpp.ProgressEvent) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError,
			"streaming not supported by this response writer")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	encode := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			if ev.EmittedAt.IsZero() {
				ev.EmittedAt = time.Now().UTC()
			}
			fmt.Fprintf(w, "event: %s\n", ev.Kind)
			_, _ = w.Write([]byte("data: "))
			_ = encode.Encode(ev)
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}
	}
}

func writeInstallError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llamacpp.ErrInstallInProgress):
		WriteError(w, http.StatusConflict, errCodeLocalModelInstallInProgress, err.Error())
	case errors.Is(err, llamacpp.ErrInstallSpecEmpty),
		errors.Is(err, llamacpp.ErrInstallSpecAmbiguous),
		errors.Is(err, llamacpp.ErrPasteURLEmpty),
		errors.Is(err, llamacpp.ErrPasteURLInvalid),
		errors.Is(err, llamacpp.ErrPasteURLNotGGUF),
		errors.Is(err, llamacpp.ErrPasteURLNotDirect),
		errors.Is(err, llamacpp.ErrCatalogIDRequired):
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
	case errors.Is(err, llamacpp.ErrCatalogEntryNotFound):
		WriteError(w, http.StatusNotFound, errCodeNotFound, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

func writeRuntimeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, llamacpp.ErrRuntimeUnavailable):
		WriteError(w, http.StatusServiceUnavailable, errCodeLocalModelsUnavailable, err.Error())
	case errors.Is(err, llamacpp.ErrRuntimeNotRunning),
		errors.Is(err, llamacpp.ErrRuntimeWrongModel):
		WriteError(w, http.StatusServiceUnavailable, errCodeLocalModelRuntimeUnavailable, err.Error())
	default:
		if isLocalModelNotInstalled(err) {
			WriteError(w, http.StatusNotFound, errCodeLocalModelNotInstalled, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
	}
}

// isLocalModelNotInstalled mirrors the proxy.go check — best-effort
// string match until the lookup adapter grows a sentinel. Centralized
// here so a future swap to errors.Is is a one-line change.
func isLocalModelNotInstalled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// Context-unused linter pacifier — go vet flags unused ctx args in
// stub functions during scaffolding. Real handlers above consume the
// request context.
var _ = context.Background
