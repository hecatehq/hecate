package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/hecatehq/hecate/internal/config"
)

type providerPreset struct {
	ID           string
	Name         string
	Kind         string
	Protocol     string
	BaseURL      string
	APIKeyEnv    string
	APIVersion   string
	DefaultModel string
	DocsURL      string
	Description  string
	EnvSnippet   string
}

func (h *Handler) HandleProviderPresets(w http.ResponseWriter, r *http.Request) {
	items := providerPresets()
	data := make([]ProviderPresetResponseItem, 0, len(items))
	for _, item := range items {
		data = append(data, ProviderPresetResponseItem{
			ID:           item.ID,
			Name:         item.Name,
			Kind:         item.Kind,
			Protocol:     item.Protocol,
			BaseURL:      item.BaseURL,
			APIKeyEnv:    item.APIKeyEnv,
			APIVersion:   item.APIVersion,
			DefaultModel: item.DefaultModel,
			DocsURL:      item.DocsURL,
			Description:  item.Description,
			EnvSnippet:   item.EnvSnippet,
		})
	}

	WriteJSON(w, http.StatusOK, ProviderPresetResponse{
		Object: "provider_presets",
		Data:   data,
	})
}

func providerPresets() []providerPreset {
	items := config.BuiltInProviders()
	out := make([]providerPreset, 0, len(items))
	for _, item := range items {
		out = append(out, newProviderPreset(
			item.ID,
			item.Name,
			item.Kind,
			item.Protocol,
			item.BaseURL,
			item.APIKeyEnv,
			item.APIVersion,
			item.DefaultModel,
			item.DocsURL,
			item.Description,
		))
	}
	return out
}

func newProviderPreset(id, name, kind, protocol, baseURL, apiKeyEnv, apiVersion, defaultModel, docsURL, description string) providerPreset {
	var envLines []string
	prefix := "PROVIDER_" + strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(id, "-", "_"), ".", "_")) + "_"
	if apiKeyEnv != "" {
		envLines = append(envLines, fmt.Sprintf("%s=your_api_key_here", apiKeyEnv))
	}
	if apiVersion != "" {
		envLines = append(envLines, fmt.Sprintf("%s=%s", prefix+"API_VERSION", apiVersion))
	}
	if baseURL != "" {
		envLines = append(envLines, fmt.Sprintf("%s=%s", prefix+"BASE_URL", baseURL))
	}

	return providerPreset{
		ID:           id,
		Name:         name,
		Kind:         kind,
		Protocol:     protocol,
		BaseURL:      baseURL,
		APIKeyEnv:    apiKeyEnv,
		APIVersion:   apiVersion,
		DefaultModel: defaultModel,
		DocsURL:      docsURL,
		Description:  description,
		EnvSnippet:   strings.Join(envLines, "\n"),
	}
}
