package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
)

type localProviderRoundTrip struct {
	mu    sync.Mutex
	calls map[string]int
	body  map[string]string
	err   map[string]error
}

func (rt *localProviderRoundTrip) Do(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	if rt.calls == nil {
		rt.calls = make(map[string]int)
	}
	rt.calls[req.URL.String()]++
	err := rt.err[req.URL.String()]
	body := rt.body[req.URL.String()]
	rt.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestDiscoverLocalProvidersDedupesSharedHTTPProbe(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "llamacpp", Name: "llama.cpp", Kind: "local", BaseURL: "http://127.0.0.1:8080/v1"},
		{ID: "localai", Name: "LocalAI", Kind: "local", BaseURL: "http://127.0.0.1:8080/v1"},
	}
	rt := &localProviderRoundTrip{
		body: map[string]string{
			"http://127.0.0.1:8080/v1/models": `{"data":[{"id":"local-model"}]}`,
		},
	}

	items := discoverLocalProviders(context.Background(), providers, missingLocalCommand, rt)

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if got := rt.calls["http://127.0.0.1:8080/v1/models"]; got != 1 {
		t.Fatalf("shared endpoint request count = %d, want 1", got)
	}
	for _, item := range items {
		if item.Status != "running" || !item.HTTPAvailable || item.ModelCount != 1 {
			t.Fatalf("item = %+v, want running with one model", item)
		}
	}
}

func TestLocalProviderDiscoveryRejectsRemoteClients(t *testing.T) {
	t.Parallel()

	handler := &Handler{}
	tests := []struct {
		name       string
		remoteAddr string
		header     string
	}{
		{name: "non loopback", remoteAddr: "203.0.113.7:3456"},
		{name: "forwarded loopback", remoteAddr: "127.0.0.1:3456", header: "203.0.113.7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/hecate/v1/settings/providers/local-discovery", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.header != "" {
				req.Header.Set("X-Forwarded-For", tt.header)
			}
			rec := httptest.NewRecorder()

			handler.HandleLocalProviderDiscovery(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

func TestDiscoverLocalProvidersRunsCommandLookupsInParallel(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "ollama", Name: "Ollama", Kind: "local", BaseURL: "http://127.0.0.1:11434/v1"},
		{ID: "lmstudio", Name: "LM Studio", Kind: "local", BaseURL: "http://127.0.0.1:1234/v1"},
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	lookPath := func(command string) (string, error) {
		started <- command
		<-release
		return "/usr/local/bin/" + command, nil
	}
	rt := &localProviderRoundTrip{
		body: map[string]string{
			"http://127.0.0.1:11434/api/tags": `{"models":[]}`,
			"http://127.0.0.1:1234/v1/models": `{"data":[]}`,
		},
	}

	done := make(chan []LocalProviderDiscoveryResponseItem, 1)
	go func() {
		done <- discoverLocalProviders(context.Background(), providers, lookPath, rt)
	}()

	commands := []string{
		receiveLocalDiscoverySignal(t, started),
		receiveLocalDiscoverySignal(t, started),
	}
	if strings.Join(commands, ",") != "ollama,lms" && strings.Join(commands, ",") != "lms,ollama" {
		t.Fatalf("commands started = %#v, want ollama and lms", commands)
	}

	releaseOnce.Do(func() { close(release) })
	items := receiveLocalDiscoveryResult(t, done)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
}

func TestDiscoverLocalProvidersRunsHTTPProbesInParallel(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "ollama", Name: "Ollama", Kind: "local", BaseURL: "http://127.0.0.1:11434/v1"},
		{ID: "lmstudio", Name: "LM Studio", Kind: "local", BaseURL: "http://127.0.0.1:1234/v1"},
	}
	rt := &blockingLocalProviderRoundTrip{
		started: make(chan string, 2),
		release: make(chan struct{}),
		body: map[string]string{
			"http://127.0.0.1:11434/api/tags": `{"models":[]}`,
			"http://127.0.0.1:1234/v1/models": `{"data":[]}`,
		},
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(rt.release) })

	done := make(chan []LocalProviderDiscoveryResponseItem, 1)
	go func() {
		done <- discoverLocalProviders(context.Background(), providers, missingLocalCommand, rt)
	}()

	urls := []string{
		receiveLocalDiscoverySignal(t, rt.started),
		receiveLocalDiscoverySignal(t, rt.started),
	}
	if strings.Join(urls, ",") != "http://127.0.0.1:11434/api/tags,http://127.0.0.1:1234/v1/models" &&
		strings.Join(urls, ",") != "http://127.0.0.1:1234/v1/models,http://127.0.0.1:11434/api/tags" {
		t.Fatalf("HTTP probes started = %#v, want both unique endpoints", urls)
	}

	releaseOnce.Do(func() { close(rt.release) })
	items := receiveLocalDiscoveryResult(t, done)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
}

func TestDiscoverLocalProvidersChecksCommandPresence(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "ollama", Name: "Ollama", Kind: "local", BaseURL: "http://127.0.0.1:11434/v1"},
	}
	rt := &localProviderRoundTrip{
		err: map[string]error{
			"http://127.0.0.1:11434/api/tags": errors.New("connection refused"),
		},
	}

	items := discoverLocalProviders(context.Background(), providers, func(command string) (string, error) {
		if command == "ollama" {
			return "/usr/local/bin/ollama", nil
		}
		return "", errors.New("missing")
	}, rt)

	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Status != "installed" {
		t.Fatalf("status = %q, want installed", item.Status)
	}
	if !item.CommandAvailable || item.CommandPath != "/usr/local/bin/ollama" {
		t.Fatalf("command detection = available %v path %q", item.CommandAvailable, item.CommandPath)
	}
	if item.HTTPAvailable {
		t.Fatal("HTTPAvailable = true, want false")
	}
}

func TestDiscoverLocalProvidersFindsLMStudioInNativeAppPath(t *testing.T) {
	t.Setenv("HOME", "/Users/alice")

	providers := []config.BuiltInProvider{
		{ID: "lmstudio", Name: "LM Studio", Kind: "local", BaseURL: "http://127.0.0.1:1234/v1"},
	}
	rt := &localProviderRoundTrip{
		err: map[string]error{
			"http://127.0.0.1:1234/v1/models": errors.New("connection refused"),
		},
	}
	lookPath := func(command string) (string, error) {
		if command == "/Users/alice/.lmstudio/bin/lms" {
			return command, nil
		}
		return "", errors.New("missing")
	}

	items := discoverLocalProviders(context.Background(), providers, lookPath, rt)

	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Command != "lms" {
		t.Fatalf("command = %q, want lms", item.Command)
	}
	if item.CommandPath != "/Users/alice/.lmstudio/bin/lms" {
		t.Fatalf("command path = %q, want native LM Studio path", item.CommandPath)
	}
	if item.Status != "installed" {
		t.Fatalf("status = %q, want installed", item.Status)
	}
}

func TestDiscoverLocalProvidersOllamaInstalledStoppedAndRunning(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "ollama", Name: "Ollama", Kind: "local", BaseURL: "http://127.0.0.1:11434/v1"},
	}
	lookPath := func(command string) (string, error) {
		if command == "ollama" {
			return "/usr/local/bin/ollama", nil
		}
		return "", errors.New("missing")
	}

	tests := []struct {
		name          string
		rt            *localProviderRoundTrip
		wantStatus    string
		wantHTTP      bool
		wantModelList []string
	}{
		{
			name: "stopped",
			rt: &localProviderRoundTrip{
				err: map[string]error{
					"http://127.0.0.1:11434/api/tags": errors.New("connection refused"),
				},
			},
			wantStatus: "installed",
			wantHTTP:   false,
		},
		{
			name: "running without models",
			rt: &localProviderRoundTrip{
				body: map[string]string{
					"http://127.0.0.1:11434/api/tags": `{"models":[]}`,
				},
			},
			wantStatus: "running",
			wantHTTP:   true,
		},
		{
			name: "running",
			rt: &localProviderRoundTrip{
				body: map[string]string{
					"http://127.0.0.1:11434/api/tags": `{"models":[{"name":"llama3.1:8b"},{"name":"qwen2.5:7b"}]}`,
				},
			},
			wantStatus:    "running",
			wantHTTP:      true,
			wantModelList: []string{"llama3.1:8b", "qwen2.5:7b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			items := discoverLocalProviders(context.Background(), providers, lookPath, tt.rt)
			if len(items) != 1 {
				t.Fatalf("items = %d, want 1", len(items))
			}
			item := items[0]
			if item.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", item.Status, tt.wantStatus)
			}
			if !item.CommandAvailable || item.Command != "ollama" || item.CommandPath != "/usr/local/bin/ollama" {
				t.Fatalf("command detection = command %q available %v path %q", item.Command, item.CommandAvailable, item.CommandPath)
			}
			if item.HTTPAvailable != tt.wantHTTP {
				t.Fatalf("HTTPAvailable = %v, want %v", item.HTTPAvailable, tt.wantHTTP)
			}
			if strings.Join(item.Models, ",") != strings.Join(tt.wantModelList, ",") {
				t.Fatalf("models = %#v, want %#v", item.Models, tt.wantModelList)
			}
		})
	}
}

func TestDiscoverLocalProvidersRejectsInvalidHTTPProbeBody(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "lmstudio", Name: "LM Studio", Kind: "local", BaseURL: "http://127.0.0.1:1234/v1"},
	}
	rt := &localProviderRoundTrip{
		body: map[string]string{
			"http://127.0.0.1:1234/v1/models": `not-json`,
		},
	}

	items := discoverLocalProviders(context.Background(), providers, missingLocalCommand, rt)

	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Status != "not_detected" {
		t.Fatalf("status = %q, want not_detected", item.Status)
	}
	if item.HTTPAvailable {
		t.Fatal("HTTPAvailable = true, want false")
	}
	if item.ModelCount != 0 || len(item.Models) != 0 {
		t.Fatalf("models = %#v count = %d, want none", item.Models, item.ModelCount)
	}
	if !strings.Contains(item.Error, "invalid lmstudio response") {
		t.Fatalf("error = %q, want invalid response detail", item.Error)
	}
}

func TestDiscoverLocalProvidersDecodesSharedHTTPProbePerProvider(t *testing.T) {
	t.Parallel()

	providers := []config.BuiltInProvider{
		{ID: "llamacpp", Name: "llama.cpp", Kind: "local", BaseURL: "http://127.0.0.1:8080/v1"},
		{ID: "localai", Name: "LocalAI", Kind: "local", BaseURL: "http://127.0.0.1:8080/v1"},
	}
	rt := &localProviderRoundTrip{
		body: map[string]string{
			"http://127.0.0.1:8080/v1/models": `not-json`,
		},
	}

	items := discoverLocalProviders(context.Background(), providers, missingLocalCommand, rt)

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if got := rt.calls["http://127.0.0.1:8080/v1/models"]; got != 1 {
		t.Fatalf("shared endpoint request count = %d, want 1", got)
	}
	for _, item := range items {
		if item.HTTPAvailable {
			t.Fatalf("%s HTTPAvailable = true, want false", item.PresetID)
		}
		if !strings.Contains(item.Error, "invalid "+item.PresetID+" response") {
			t.Fatalf("%s error = %q, want provider-specific decode error", item.PresetID, item.Error)
		}
	}
}

func TestLocalProviderProbeURLUsesOllamaNativeTagsEndpoint(t *testing.T) {
	t.Parallel()

	got := localProviderProbeURL(config.BuiltInProvider{
		ID:      "ollama",
		BaseURL: "http://127.0.0.1:11434/v1",
	})
	if got != "http://127.0.0.1:11434/api/tags" {
		t.Fatalf("probe URL = %q, want Ollama native tags endpoint", got)
	}
}

func missingLocalCommand(string) (string, error) {
	return "", errors.New("missing")
}

type blockingLocalProviderRoundTrip struct {
	started chan string
	release chan struct{}
	body    map[string]string
}

func (rt *blockingLocalProviderRoundTrip) Do(req *http.Request) (*http.Response, error) {
	url := req.URL.String()
	rt.started <- url
	select {
	case <-rt.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(rt.body[url])),
	}, nil
}

func receiveLocalDiscoverySignal(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for parallel discovery signal")
		return ""
	}
}

func receiveLocalDiscoveryResult(t *testing.T, ch <-chan []LocalProviderDiscoveryResponseItem) []LocalProviderDiscoveryResponseItem {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for discovery result")
		return nil
	}
}
