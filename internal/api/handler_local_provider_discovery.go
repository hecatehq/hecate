package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/internal/config"
)

const localProviderDiscoveryTimeout = 700 * time.Millisecond

type localProviderLookPath func(string) (string, error)

type localProviderHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type localHTTPProbeResult struct {
	available bool
	models    []string
	err       string
}

type localHTTPFetchResult struct {
	statusCode int
	body       []byte
	err        string
}

type localProviderProbe struct {
	provider config.BuiltInProvider
	probeURL string
}

type localProviderDiscoveryResult struct {
	command string
	path    string
	http    localHTTPProbeResult
}

type localHTTPProbeTask struct {
	done   chan struct{}
	result localHTTPFetchResult
}

func (h *Handler) HandleLocalProviderDiscovery(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), localProviderDiscoveryTimeout)
	defer cancel()

	items := discoverLocalProviders(ctx, config.BuiltInProviders(), exec.LookPath, http.DefaultClient)
	WriteJSON(w, http.StatusOK, LocalProviderDiscoveryResponse{
		Object: "local_provider_discovery",
		Data:   items,
	})
}

func discoverLocalProviders(ctx context.Context, providers []config.BuiltInProvider, lookPath localProviderLookPath, client localProviderHTTPDoer) []LocalProviderDiscoveryResponseItem {
	localProviders := make([]localProviderProbe, 0, len(providers))
	for _, provider := range providers {
		if provider.Kind != "local" {
			continue
		}
		probeURL := localProviderProbeURL(provider)
		localProviders = append(localProviders, localProviderProbe{provider: provider, probeURL: probeURL})
	}

	results := discoverLocalProviderPairsConcurrently(ctx, localProviders, lookPath, client)

	out := make([]LocalProviderDiscoveryResponseItem, 0, len(localProviders))
	for i, entry := range localProviders {
		provider := entry.provider
		result := results[i]
		command := result.command
		commandPath := result.path
		httpResult := result.http

		status := "not_detected"
		if commandPath != "" {
			status = "installed"
		}
		if httpResult.available {
			status = "running"
		} else if httpResult.err != "" && commandPath != "" {
			status = "installed"
		}

		out = append(out, LocalProviderDiscoveryResponseItem{
			PresetID:         provider.ID,
			Name:             provider.Name,
			BaseURL:          provider.BaseURL,
			ProbeURL:         entry.probeURL,
			Status:           status,
			Command:          command,
			CommandAvailable: commandPath != "",
			CommandPath:      commandPath,
			HTTPAvailable:    httpResult.available,
			ModelCount:       len(httpResult.models),
			Models:           httpResult.models,
			Error:            httpResult.err,
		})
	}

	return out
}

func discoverLocalProviderPairsConcurrently(ctx context.Context, providers []localProviderProbe, lookPath localProviderLookPath, client localProviderHTTPDoer) []localProviderDiscoveryResult {
	results := make([]localProviderDiscoveryResult, len(providers))
	probes := make(map[string]*localHTTPProbeTask)
	var probesMu sync.Mutex
	var wg sync.WaitGroup

	getProbe := func(probeURL string) *localHTTPProbeTask {
		probesMu.Lock()
		defer probesMu.Unlock()
		if task, ok := probes[probeURL]; ok {
			return task
		}
		task := &localHTTPProbeTask{done: make(chan struct{})}
		probes[probeURL] = task
		go func() {
			probeCtx, cancel := context.WithTimeout(ctx, localProviderDiscoveryTimeout)
			defer cancel()
			task.result = fetchLocalProviderHTTP(probeCtx, client, probeURL)
			close(task.done)
		}()
		return task
	}

	for i, entry := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			command, path := findLocalProviderCommand(entry.provider.ID, lookPath)
			task := getProbe(entry.probeURL)
			var httpResult localHTTPProbeResult
			select {
			case <-task.done:
				httpResult = decodeLocalProviderHTTPProbe(task.result, entry.provider.ID)
			case <-ctx.Done():
				httpResult = localHTTPProbeResult{err: compactLocalProbeError(ctx.Err())}
			}
			results[i] = localProviderDiscoveryResult{command: command, path: path, http: httpResult}
		}()
	}
	wg.Wait()
	return results
}

func findLocalProviderCommand(providerID string, lookPath localProviderLookPath) (string, string) {
	for _, command := range localProviderCommandCandidates(providerID) {
		path, err := lookPath(command)
		if err == nil && strings.TrimSpace(path) != "" {
			return command, path
		}
	}
	candidates := localProviderCommandCandidates(providerID)
	if len(candidates) == 0 {
		return "", ""
	}
	return candidates[0], ""
}

func localProviderCommandCandidates(providerID string) []string {
	switch providerID {
	case "ollama":
		return []string{"ollama"}
	case "lmstudio":
		return []string{"lms"}
	case "llamacpp":
		return []string{"llama-server", "llama-server.exe"}
	case "localai":
		return []string{"local-ai", "localai"}
	default:
		return nil
	}
}

func localProviderProbeURL(provider config.BuiltInProvider) string {
	if provider.ID == "ollama" {
		if parsed, err := url.Parse(provider.BaseURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
			parsed.Path = "/api/tags"
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String()
		}
	}

	base := strings.TrimRight(provider.BaseURL, "/")
	if strings.HasSuffix(base, "/models") {
		return base
	}
	return base + "/models"
}

func fetchLocalProviderHTTP(ctx context.Context, client localProviderHTTPDoer, probeURL string) localHTTPFetchResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return localHTTPFetchResult{err: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return localHTTPFetchResult{err: compactLocalProbeError(err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return localHTTPFetchResult{statusCode: resp.StatusCode, err: err.Error()}
	}
	return localHTTPFetchResult{statusCode: resp.StatusCode, body: body}
}

func decodeLocalProviderHTTPProbe(fetch localHTTPFetchResult, providerID string) localHTTPProbeResult {
	if fetch.err != "" {
		return localHTTPProbeResult{err: fetch.err}
	}
	if fetch.statusCode < 200 || fetch.statusCode >= 300 {
		return localHTTPProbeResult{err: fmt.Sprintf("HTTP %d", fetch.statusCode)}
	}

	models, err := decodeLocalProviderModels(fetch.body, providerID)
	if err != nil {
		return localHTTPProbeResult{err: err.Error()}
	}
	return localHTTPProbeResult{available: true, models: models}
}

func decodeLocalProviderModels(body []byte, providerID string) ([]string, error) {
	if providerID == "ollama" {
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
			return nil, fmt.Errorf("invalid %s response: %w", providerID, err)
		}
		models := make([]string, 0, len(payload.Models))
		for _, model := range payload.Models {
			if strings.TrimSpace(model.Name) != "" {
				models = append(models, model.Name)
			}
		}
		return models, nil
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("invalid %s response: %w", providerID, err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if strings.TrimSpace(model.ID) != "" {
			models = append(models, model.ID)
		}
	}
	return models, nil
}

func compactLocalProbeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	if strings.Contains(err.Error(), "connection refused") {
		return "connection refused"
	}
	return err.Error()
}
