package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hecate/agent-runtime/internal/config"
)

type localProviderRoundTrip struct {
	calls map[string]int
	body  map[string]string
	err   map[string]error
}

func (rt *localProviderRoundTrip) Do(req *http.Request) (*http.Response, error) {
	if rt.calls == nil {
		rt.calls = make(map[string]int)
	}
	rt.calls[req.URL.String()]++
	if err := rt.err[req.URL.String()]; err != nil {
		return nil, err
	}
	body := rt.body[req.URL.String()]
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
