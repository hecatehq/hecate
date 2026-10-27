package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/githubapp"
)

type fakeGitHubInspector func(context.Context, string) (githubapp.Inspection, error)

func (f fakeGitHubInspector) Inspect(ctx context.Context, url string) (githubapp.Inspection, error) {
	return f(ctx, url)
}

func TestGitHubSettings_PassiveAndPathFree(t *testing.T) {
	h := &Handler{config: config.Config{GitHub: githubapp.Config{ClientID: "app", InstallationID: 12, PrivateKeyFile: filepath.Join(t.TempDir(), "key.pem"), Repositories: []string{"owner/repo"}}}, githubConnection: fakeGitHubInspector(func(context.Context, string) (githubapp.Inspection, error) {
		t.Fatal("passive GET contacted GitHub")
		return githubapp.Inspection{}, nil
	})}
	w := httptest.NewRecorder()
	h.HandleGitHubSettings(w, httptest.NewRequest("GET", "/hecate/v1/settings/github", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "key.pem") || !strings.Contains(w.Body.String(), `"configured":true`) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
}

func TestGitHubInspect_RejectsMalformedBodiesBeforeDispatch(t *testing.T) {
	h := &Handler{githubConnection: fakeGitHubInspector(func(context.Context, string) (githubapp.Inspection, error) {
		t.Fatal("invalid input dispatched")
		return githubapp.Inspection{}, nil
	})}
	for _, body := range []string{`{}`, `null`, `{"url":"https://evil.example"}`, `{"url":"https://github.com/owner/repo/pull/7","token":"secret"}`, `{"url":"https://github.com/owner/repo/pull/7"} {}`, strings.Repeat("x", 2049)} {
		w := httptest.NewRecorder()
		h.HandleGitHubPullRequestInspect(w, httptest.NewRequest("POST", "/hecate/v1/github/pull-request/inspect", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestGitHubInspect_SafeErrorsAndRemoteClassification(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{githubapp.ErrUnconfigured, 503}, {githubapp.ErrRepositoryDenied, 403}, {githubapp.ErrHeadChanged, 409}, {githubapp.ErrBusy, 429}, {errors.New("private credential /host/path"), 502}, {&githubapp.HTTPError{Status: 429, RetryAfter: 60}, 429}} {
		h := &Handler{githubConnection: fakeGitHubInspector(func(context.Context, string) (githubapp.Inspection, error) { return githubapp.Inspection{}, test.err })}
		w := httptest.NewRecorder()
		h.HandleGitHubPullRequestInspect(w, httptest.NewRequest("POST", "/hecate/v1/github/pull-request/inspect", strings.NewReader(`{"url":"https://github.com/owner/repo/pull/7"}`)))
		if w.Code != test.status || strings.Contains(w.Body.String(), "/host/path") {
			t.Fatalf("response=%d %s", w.Code, w.Body.String())
		}
	}
	for _, route := range []struct{ method, path string }{{http.MethodGet, "/hecate/v1/settings/github"}, {http.MethodPost, "/hecate/v1/github/pull-request/inspect"}} {
		if reason := remoteRuntimeEndpointBlockReason(route.method, route.path); reason != "" {
			t.Fatalf("remote route blocked: %s", reason)
		}
	}
}
