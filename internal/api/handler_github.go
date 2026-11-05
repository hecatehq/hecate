package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/hecatehq/hecate/internal/githubapp"
)

type githubInspector interface {
	Inspect(context.Context, string) (githubapp.Inspection, error)
}

type GitHubConnectionData struct {
	Configured   bool     `json:"configured"`
	Mode         string   `json:"mode"`
	Repositories []string `json:"repositories"`
	SetupHint    string   `json:"setup_hint"`
}

type GitHubConnectionResponse struct {
	Object string               `json:"object"`
	Data   GitHubConnectionData `json:"data"`
}

type GitHubPullRequestResponse struct {
	Object string               `json:"object"`
	Data   githubapp.Inspection `json:"data"`
}

// Settings reads are passive; only the explicit inspect action reads the key
// and contacts GitHub. Neither response may expose credentials or host paths.
func (h *Handler) HandleGitHubSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	configured := h.config.GitHub.Configured() && h.config.GitHub.Validate() == nil
	WriteJSON(w, http.StatusOK, GitHubConnectionResponse{Object: "github_connection", Data: GitHubConnectionData{
		Configured: configured, Mode: "read_only", Repositories: append([]string{}, h.config.GitHub.Repositories...), SetupHint: githubapp.ConfigurationHint(),
	}})
}

func (h *Handler) HandleGitHubPullRequestInspect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		URL string `json:"url"`
	}
	if err := decoder.Decode(&request); err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "provide one pull request URL")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, "request body must contain one object")
		return
	}
	if _, err := githubapp.ParseReference(request.URL); err != nil {
		WriteError(w, http.StatusBadRequest, "github.invalid_reference", err.Error())
		return
	}
	if h.githubConnection == nil {
		WriteError(w, http.StatusServiceUnavailable, "github.not_configured", githubapp.ErrUnconfigured.Error())
		return
	}
	inspection, err := h.githubConnection.Inspect(r.Context(), request.URL)
	if err != nil {
		status, code, message := http.StatusBadGateway, "github.unavailable", "GitHub inspection could not be completed. Try again."
		switch {
		case errors.Is(err, githubapp.ErrUnconfigured), errors.Is(err, githubapp.ErrInvalidConfiguration):
			status, code, message = http.StatusServiceUnavailable, "github.not_configured", githubapp.ErrUnconfigured.Error()
		case errors.Is(err, githubapp.ErrRepositoryDenied):
			status, code, message = http.StatusForbidden, "github.repository_not_enabled", githubapp.ErrRepositoryDenied.Error()
		case errors.Is(err, githubapp.ErrBusy):
			status, code, message = http.StatusTooManyRequests, "github.busy", githubapp.ErrBusy.Error()
			w.Header().Set("Retry-After", "2")
		case errors.Is(err, githubapp.ErrHeadChanged):
			status, code, message = http.StatusConflict, "github.head_changed", githubapp.ErrHeadChanged.Error()
		case errors.Is(err, githubapp.ErrCredential):
			code, message = "github.credential_unavailable", githubapp.ErrCredential.Error()
		case errors.Is(err, githubapp.ErrScope):
			code, message = "github.scope_mismatch", githubapp.ErrScope.Error()
		default:
			var upstream *githubapp.HTTPError
			if errors.As(err, &upstream) {
				message = upstream.Error()
				if upstream.RetryAfter > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(upstream.RetryAfter))
				}
				if upstream.Status == 429 {
					status = http.StatusTooManyRequests
				}
			}
		}
		WriteError(w, status, code, message)
		return
	}
	WriteJSON(w, http.StatusOK, GitHubPullRequestResponse{Object: "github_pull_request", Data: inspection})
}
