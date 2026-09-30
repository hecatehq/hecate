package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/browserapp"
)

type BrowserCandidate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type BrowserSettingsData struct {
	Readiness  BrowserEvidenceRuntimeReadinessResponse `json:"readiness"`
	Source     string                                  `json:"source"`
	Selected   *BrowserCandidate                       `json:"selected,omitempty"`
	Candidates []BrowserCandidate                      `json:"candidates"`
	Backend    string                                  `json:"backend"`
}

type BrowserSettingsResponse struct {
	Object string              `json:"object"`
	Data   BrowserSettingsData `json:"data"`
}

// Browser paths are intentionally isolated from the remote-safe settings
// snapshot. Enforce the boundary here as well as in route middleware so direct
// handler composition cannot expose host paths or change browser configuration.
func (h *Handler) requireBrowserSettings(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.config.Server.RemoteRuntimeMode {
		WriteError(w, http.StatusForbidden, errCodeForbidden, remoteRuntimeLocalOnlyMessage)
		return false
	}
	return requireLoopbackClient(w, r, "browser setup")
}

func (h *Handler) HandleBrowserSettings(w http.ResponseWriter, r *http.Request) {
	if !h.requireBrowserSettings(w, r) {
		return
	}
	settings, err := h.browserRuntime.Settings(r.Context())
	writeBrowserSettings(w, settings, err)
}

func (h *Handler) HandleBrowserSettingsEnable(w http.ResponseWriter, r *http.Request) {
	if !h.requireBrowserSettings(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		CandidateID string `json:"candidate_id"`
	}
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.CandidateID) == "" {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "select a browser from the current discovered candidates")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must contain one browser selection")
		return
	}
	settings, err := h.browserRuntime.Enable(r.Context(), request.CandidateID)
	writeBrowserSettings(w, settings, err)
}

func (h *Handler) HandleBrowserSettingsDisable(w http.ResponseWriter, r *http.Request) {
	if !h.requireBrowserSettings(w, r) {
		return
	}
	settings, err := h.browserRuntime.Disable(r.Context())
	writeBrowserSettings(w, settings, err)
}

func writeBrowserSettings(w http.ResponseWriter, settings browserapp.Settings, err error) {
	if err != nil {
		switch {
		case errors.Is(err, browserapp.ErrRemote):
			WriteError(w, http.StatusForbidden, errCodeForbidden, remoteRuntimeLocalOnlyMessage)
		case errors.Is(err, browserapp.ErrManaged):
			WriteError(w, http.StatusConflict, "browser.environment_managed", "Browser setup is managed by HECATE_TASK_BROWSER_EXECUTABLE. Change that setting and restart Hecate.")
		case errors.Is(err, browserapp.ErrCandidateChanged):
			WriteError(w, http.StatusConflict, "browser.candidate_changed", "The selected browser is no longer available as discovered. Refresh and select it again.")
		default:
			// Storage/OS errors can contain filesystem paths; never reflect them.
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, "Browser settings could not be read or saved. Refresh before trying again.")
		}
		return
	}
	data := BrowserSettingsData{
		Source:     settings.Source,
		Backend:    settings.Backend,
		Candidates: []BrowserCandidate{},
		Readiness: BrowserEvidenceRuntimeReadinessResponse{
			Available:      settings.Readiness.Available,
			Status:         settings.Readiness.Status,
			Message:        settings.Readiness.Message,
			OperatorAction: settings.Readiness.OperatorAction,
		},
	}
	if settings.Selected != nil {
		data.Selected = &BrowserCandidate{ID: settings.Selected.ID, Name: settings.Selected.Name, Path: settings.Selected.Path}
	}
	for _, candidate := range settings.Candidates {
		data.Candidates = append(data.Candidates, BrowserCandidate{ID: candidate.ID, Name: candidate.Name, Path: candidate.Path})
	}
	WriteJSON(w, http.StatusOK, BrowserSettingsResponse{Object: "browser_settings", Data: data})
}
