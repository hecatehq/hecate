package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
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
	httpResults := make(map[string]localHTTPProbeResult)
	out := make([]LocalProviderDiscoveryResponseItem, 0, len(providers))

	for _, provider := range providers {
		if provider.Kind != "local" {
			continue
		}

		command, commandPath := findLocalProviderCommand(provider.ID, lookPath)
		probeURL := localProviderProbeURL(provider)
		result, ok := httpResults[probeURL]
		if !ok {
			result = probeLocalProviderHTTP(ctx, client, probeURL, provider.ID)
			httpResults[probeURL] = result
		}

		status := "not_detected"
		if commandPath != "" {
			status = "installed"
		}
		if result.available {
			status = "running"
		} else if result.err != "" && commandPath != "" {
			status = "installed"
		}

		out = append(out, LocalProviderDiscoveryResponseItem{
			PresetID:         provider.ID,
			Name:             provider.Name,
			BaseURL:          provider.BaseURL,
			ProbeURL:         probeURL,
			Status:           status,
			Command:          command,
			CommandAvailable: commandPath != "",
			CommandPath:      commandPath,
			HTTPAvailable:    result.available,
			ModelCount:       len(result.models),
			Models:           result.models,
			Error:            result.err,
		})
	}

	return out
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

func probeLocalProviderHTTP(ctx context.Context, client localProviderHTTPDoer, probeURL, providerID string) localHTTPProbeResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return localHTTPProbeResult{err: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return localHTTPProbeResult{err: compactLocalProbeError(err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return localHTTPProbeResult{err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	models := decodeLocalProviderModels(resp, providerID)
	return localHTTPProbeResult{available: true, models: models}
}

func decodeLocalProviderModels(resp *http.Response, providerID string) []string {
	if providerID == "ollama" {
		var payload struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil
		}
		models := make([]string, 0, len(payload.Models))
		for _, model := range payload.Models {
			if strings.TrimSpace(model.Name) != "" {
				models = append(models, model.Name)
			}
		}
		return models
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if strings.TrimSpace(model.ID) != "" {
			models = append(models, model.ID)
		}
	}
	return models
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
