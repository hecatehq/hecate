package config

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

type BuiltInProvider struct {
	ID           string
	Name         string
	Kind         string
	Protocol     string
	BaseURL      string
	APIKeyEnv    string
	APIVersion   string
	ChatPath     string
	ModelsPath   string
	DefaultModel string
	DocsURL      string
	Description  string
	StubResponse string
	// Models is a curated list of well-known model IDs for the provider, used as
	// the static catalog when API-driven discovery isn't possible (no API key, or
	// the upstream is unreachable). When discovery succeeds, the live `/v1/models`
	// response replaces this. Local providers leave this empty since their catalog
	// is fully dynamic (depends on what the user has loaded).
	Models []string
}

var builtInProviders = []BuiltInProvider{
	{
		ID:           "anthropic",
		Name:         "Anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.com",
		APIKeyEnv:    "PROVIDER_ANTHROPIC_API_KEY",
		APIVersion:   "2023-06-01",
		DocsURL:      "https://platform.claude.com/docs/en/about-claude/models/overview",
		Description:  "Native Anthropic Messages API preset. This uses Hecate's Anthropic protocol path and discovers available models from /v1/models.",
		StubResponse: "Stubbed Anthropic response.",
		Models: []string{
			"claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-4-6",
			"claude-haiku-4-5", "claude-3-7-sonnet-latest", "claude-3-5-sonnet-latest",
			"claude-3-5-haiku-latest",
		},
	},
	{
		ID:           "deepseek",
		Name:         "DeepSeek",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.deepseek.com/v1",
		APIKeyEnv:    "PROVIDER_DEEPSEEK_API_KEY",
		DocsURL:      "https://api-docs.deepseek.com/",
		Description:  "OpenAI-compatible preset for DeepSeek hosted APIs. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models:       []string{"deepseek-chat", "deepseek-reasoner"},
	},
	{
		ID:           "gemini",
		Name:         "Google Gemini",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta/openai",
		APIKeyEnv:    "PROVIDER_GEMINI_API_KEY",
		DocsURL:      "https://ai.google.dev/gemini-api/docs/openai",
		Description:  "OpenAI-compatible preset for Gemini through Google's compatibility layer. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite",
			"gemini-2.0-flash", "gemini-2.0-flash-lite", "gemini-1.5-pro", "gemini-1.5-flash",
		},
	},
	{
		ID:           "groq",
		Name:         "Groq",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.groq.com/openai/v1",
		APIKeyEnv:    "PROVIDER_GROQ_API_KEY",
		DocsURL:      "https://console.groq.com/docs/models",
		Description:  "OpenAI-compatible preset for Groq's low-latency inference API. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "llama-3.1-70b-versatile",
			"openai/gpt-oss-120b", "openai/gpt-oss-20b",
			"deepseek-r1-distill-llama-70b", "qwen/qwen3-32b",
			"mixtral-8x7b-32768", "gemma2-9b-it",
		},
	},
	{
		ID:           "llamacpp",
		Name:         "llama.cpp",
		Kind:         "local",
		Protocol:     "openai",
		BaseURL:      "http://127.0.0.1:8080/v1",
		DocsURL:      "https://github.com/ggerganov/llama.cpp/tree/master/examples/server",
		Description:  "Local preset for llama.cpp-compatible OpenAI endpoints. Hecate discovers models from /v1/models and uses the first available model when no model is pinned.",
		StubResponse: "Stubbed local provider response.",
	},
	{
		ID:           "lmstudio",
		Name:         "LM Studio",
		Kind:         "local",
		Protocol:     "openai",
		BaseURL:      "http://127.0.0.1:1234/v1",
		DocsURL:      "https://lmstudio.ai/docs/app/api/endpoints/openai",
		Description:  "Local preset for LM Studio's OpenAI-compatible server. Hecate discovers models from /v1/models and uses the first available model when no model is pinned.",
		StubResponse: "Stubbed local provider response.",
	},
	{
		ID:           "localai",
		Name:         "LocalAI",
		Kind:         "local",
		Protocol:     "openai",
		BaseURL:      "http://127.0.0.1:8080/v1",
		DocsURL:      "https://localai.io/features/openai-functions/",
		Description:  "Local preset for LocalAI's OpenAI-compatible API surface. Hecate discovers models from /v1/models and uses the first available model when no model is pinned.",
		StubResponse: "Stubbed local provider response.",
	},
	{
		ID:           "mistral",
		Name:         "Mistral",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.mistral.ai/v1",
		APIKeyEnv:    "PROVIDER_MISTRAL_API_KEY",
		DocsURL:      "https://docs.mistral.ai/getting-started/models/models_overview/",
		Description:  "OpenAI-compatible preset for Mistral hosted models. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest",
			"codestral-latest", "ministral-8b-latest", "ministral-3b-latest",
			"pixtral-large-latest", "open-mistral-nemo",
		},
	},
	{
		ID:           "ollama",
		Name:         "Ollama",
		Kind:         "local",
		Protocol:     "openai",
		BaseURL:      "http://127.0.0.1:11434/v1",
		DocsURL:      "https://github.com/ollama/ollama/blob/main/docs/openai.md",
		Description:  "Local preset for Ollama's OpenAI-compatible endpoint. Hecate discovers models from /v1/models and uses the first available model when no model is pinned.",
		StubResponse: "Stubbed local provider response.",
	},
	{
		ID:           "openai",
		Name:         "OpenAI",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.openai.com",
		APIKeyEnv:    "PROVIDER_OPENAI_API_KEY",
		DocsURL:      "https://developers.openai.com/api/docs/models",
		Description:  "Default cloud preset using the OpenAI-compatible Chat Completions API. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"gpt-4o", "gpt-4o-mini", "gpt-4o-2024-11-20",
			"gpt-4.1", "gpt-4.1-mini", "gpt-4.1-nano",
			"o1", "o1-mini", "o3", "o3-mini", "o4-mini",
			"gpt-4-turbo", "gpt-3.5-turbo",
		},
	},
	{
		ID:           "perplexity",
		Name:         "Perplexity",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.perplexity.ai",
		APIKeyEnv:    "PROVIDER_PERPLEXITY_API_KEY",
		ChatPath:     "/chat/completions",
		ModelsPath:   "/v1/models",
		DocsURL:      "https://docs.perplexity.ai/docs/sonar/openai-compatibility",
		Description:  "OpenAI-compatible preset for Perplexity Sonar models. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"sonar", "sonar-pro", "sonar-reasoning-pro", "sonar-deep-research",
		},
	},
	{
		ID:           "together_ai",
		Name:         "Together AI",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.together.xyz/v1",
		APIKeyEnv:    "PROVIDER_TOGETHER_AI_API_KEY",
		DocsURL:      "https://docs.together.ai/docs/inference-models",
		Description:  "OpenAI-compatible preset for Together AI hosted models. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo",
			"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
			"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo",
			"meta-llama/Llama-3.3-70B-Instruct-Turbo",
			"deepseek-ai/DeepSeek-V3", "deepseek-ai/DeepSeek-R1",
			"Qwen/Qwen2.5-72B-Instruct-Turbo", "mistralai/Mixtral-8x22B-Instruct-v0.1",
		},
	},
	{
		ID:           "xai",
		Name:         "xAI",
		Kind:         "cloud",
		Protocol:     "openai",
		BaseURL:      "https://api.x.ai/v1",
		APIKeyEnv:    "PROVIDER_XAI_API_KEY",
		DocsURL:      "https://docs.x.ai/docs/models",
		Description:  "OpenAI-compatible preset for xAI Grok models. Hecate discovers available models from /v1/models.",
		StubResponse: "Stubbed response from the AI Agent Runtime MVP.",
		Models: []string{
			"grok-4", "grok-3", "grok-3-mini", "grok-2", "grok-2-vision",
			"grok-beta", "grok-vision-beta",
		},
	},
}

func BuiltInProviders() []BuiltInProvider {
	out := make([]BuiltInProvider, len(builtInProviders))
	copy(out, builtInProviders)
	slices.SortFunc(out, func(a, b BuiltInProvider) int {
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func BuiltInProviderByID(name string) (BuiltInProvider, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	normalized := builtInProviderLookupKey(name)
	for _, item := range builtInProviders {
		if item.ID == name || strings.ToLower(item.Name) == name || builtInProviderLookupKey(item.ID) == normalized || builtInProviderLookupKey(item.Name) == normalized {
			return item, true
		}
	}
	return BuiltInProvider{}, false
}

var builtInProviderLookupSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func builtInProviderLookupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return builtInProviderLookupSanitizer.ReplaceAllString(value, "")
}

// DefaultProviderTimeout returns the HTTP client timeout used per
// provider LLM call when the operator hasn't configured one
// explicitly. Local providers (LMStudio, Ollama, etc.) get a much
// larger budget than cloud providers because a cold local model can
// take 30–120s just to produce its first token after a fresh load;
// the previous unconditional 30s default tripped agent loops on
// LMStudio with `context deadline exceeded`. Cloud providers get
// 60s, which still fits well within p99 for chat completions but
// leaves slack for slow upstreams. Operators who want a different
// budget can set `Timeout` on the provider config directly.
func DefaultProviderTimeout(kind string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(kind), "local") {
		return 5 * time.Minute
	}
	return 60 * time.Second
}

func (p BuiltInProvider) RuntimeConfig() OpenAICompatibleProviderConfig {
	return OpenAICompatibleProviderConfig{
		Name:         p.ID,
		Kind:         p.Kind,
		Protocol:     p.Protocol,
		BaseURL:      p.BaseURL,
		APIVersion:   p.APIVersion,
		ChatPath:     p.ChatPath,
		ModelsPath:   p.ModelsPath,
		Timeout:      DefaultProviderTimeout(p.Kind),
		StubMode:     false,
		StubResponse: p.StubResponse,
		DefaultModel: p.DefaultModel,
		KnownModels:  append([]string(nil), p.Models...),
	}
}
