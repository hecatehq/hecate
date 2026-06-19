package config

import (
	"net/url"
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
}

const FireworksDefaultAccountID = "fireworks"

func FireworksModelsPath(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		accountID = FireworksDefaultAccountID
	}
	return "https://api.fireworks.ai/v1/accounts/" + url.PathEscape(accountID) + "/models"
}

var builtInProviders = []BuiltInProvider{
	{
		ID:          "alibaba",
		Name:        "Alibaba Cloud Qwen",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		APIKeyEnv:   "PROVIDER_ALIBABA_API_KEY",
		DocsURL:     "https://help.aliyun.com/zh/model-studio/compatibility-of-openai-with-dashscope",
		Description: "Alibaba Cloud Model Studio through DashScope's OpenAI-compatible endpoint. Hosts Qwen, Qwen-Coder, and other Alibaba-served models.",
	},
	{
		ID:          "anthropic",
		Name:        "Anthropic",
		Kind:        "cloud",
		Protocol:    "anthropic",
		BaseURL:     "https://api.anthropic.com",
		APIKeyEnv:   "PROVIDER_ANTHROPIC_API_KEY",
		APIVersion:  "2023-06-01",
		DocsURL:     "https://platform.claude.com/docs/en/about-claude/models/overview",
		Description: "Anthropic's hosted API. Claude Opus, Sonnet, and Haiku tiers via the native Messages protocol, with prompt caching and native tool use.",
	},
	{
		ID:          "cerebras",
		Name:        "Cerebras",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.cerebras.ai/v1",
		APIKeyEnv:   "PROVIDER_CEREBRAS_API_KEY",
		DocsURL:     "https://inference-docs.cerebras.ai/api-reference/chat-completions",
		Description: "Cerebras Inference. OpenAI-shaped chat completions on fast Cerebras-hosted models, including GPT-OSS and other reasoning-capable models.",
	},
	{
		ID:          "cohere",
		Name:        "Cohere",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.cohere.ai/compatibility/v1",
		APIKeyEnv:   "PROVIDER_COHERE_API_KEY",
		DocsURL:     "https://docs.cohere.com/docs/compatibility-api",
		Description: "Cohere's hosted API. Command-family models tuned for RAG, multi-step tool use, and citation-grounded answers.",
	},
	{
		ID:          "deepinfra",
		Name:        "DeepInfra",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.deepinfra.com/v1/openai",
		APIKeyEnv:   "PROVIDER_DEEPINFRA_API_KEY",
		DocsURL:     "https://docs.deepinfra.com/chat/overview",
		Description: "DeepInfra OpenAI-compatible chat completions for hosted open-weight models, including DeepSeek, Qwen, Llama, and coding-focused variants.",
	},
	{
		ID:          "deepseek",
		Name:        "DeepSeek",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.deepseek.com/v1",
		APIKeyEnv:   "PROVIDER_DEEPSEEK_API_KEY",
		DocsURL:     "https://api-docs.deepseek.com/",
		Description: "DeepSeek's hosted API. Chat and chain-of-thought reasoning models at a fraction of frontier cost.",
	},
	{
		ID:          "fireworks",
		Name:        "Fireworks AI",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.fireworks.ai/inference/v1",
		APIKeyEnv:   "PROVIDER_FIREWORKS_API_KEY",
		ModelsPath:  FireworksModelsPath(""),
		DocsURL:     "https://docs.fireworks.ai/getting-started/introduction",
		Description: "Fireworks.ai serverless inference. Hosts an evolving catalog of open-weight models; model IDs are namespaced (`accounts/fireworks/models/<slug>`).",
	},
	{
		ID:          "gemini",
		Name:        "Google Gemini",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://generativelanguage.googleapis.com/v1beta/openai",
		APIKeyEnv:   "PROVIDER_GEMINI_API_KEY",
		DocsURL:     "https://ai.google.dev/gemini-api/docs/openai",
		Description: "Google Gemini through the OpenAI-compatible endpoint. Long-context (1M+ token) and multimodal models from the Gemini family.",
	},
	{
		ID:          "groq",
		Name:        "Groq",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.groq.com/openai/v1",
		APIKeyEnv:   "PROVIDER_GROQ_API_KEY",
		DocsURL:     "https://console.groq.com/docs/models",
		Description: "Groq LPU inference. Sub-second time-to-first-token on open-weight models — among the lowest end-to-end latency for chat completions.",
	},
	{
		ID:          "huggingface",
		Name:        "Hugging Face",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://router.huggingface.co/v1",
		APIKeyEnv:   "PROVIDER_HUGGINGFACE_API_KEY",
		DocsURL:     "https://huggingface.co/docs/inference-providers",
		Description: "Hugging Face Inference Providers. OpenAI-shaped router across hosted partners — quick way to try any HF-cataloged model with one key.",
	},
	{
		ID:          "llamacpp",
		Name:        "llama.cpp",
		Kind:        "local",
		Protocol:    "openai",
		BaseURL:     "http://127.0.0.1:8080/v1",
		DocsURL:     "https://github.com/ggerganov/llama.cpp/tree/master/examples/server",
		Description: "Local llama.cpp server. Run any GGUF model on CPU or GPU via the official `llama-server` binary; OpenAI-shaped endpoint.",
	},
	{
		ID:          "lmstudio",
		Name:        "LM Studio",
		Kind:        "local",
		Protocol:    "openai",
		BaseURL:     "http://127.0.0.1:1234/v1",
		DocsURL:     "https://lmstudio.ai/docs/app/api/endpoints/openai",
		Description: "Local LM Studio server. Desktop app that exposes downloaded GGUF and MLX models on a one-click OpenAI-compatible endpoint.",
	},
	{
		ID:          "localai",
		Name:        "LocalAI",
		Kind:        "local",
		Protocol:    "openai",
		BaseURL:     "http://127.0.0.1:8080/v1",
		DocsURL:     "https://localai.io/features/openai-functions/",
		Description: "Local LocalAI server. Self-hosted OpenAI drop-in with backends for GGUF, MLX, Transformers, Whisper, and image generation.",
	},
	{
		ID:          "moonshot",
		Name:        "Moonshot AI",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.moonshot.ai/v1",
		APIKeyEnv:   "PROVIDER_MOONSHOT_API_KEY",
		DocsURL:     "https://platform.moonshot.ai/docs",
		Description: "Moonshot AI's hosted Kimi models through an OpenAI-compatible endpoint. Useful for long-context and agentic coding workflows.",
	},
	{
		ID:          "mistral",
		Name:        "Mistral",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.mistral.ai/v1",
		APIKeyEnv:   "PROVIDER_MISTRAL_API_KEY",
		DocsURL:     "https://docs.mistral.ai/getting-started/models/models_overview/",
		Description: "Mistral's hosted API. Mistral, Codestral, Pixtral, and Ministral product tiers for chat, code, vision, and edge workloads.",
	},
	{
		ID:          "nvidia",
		Name:        "NVIDIA",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://integrate.api.nvidia.com/v1",
		APIKeyEnv:   "PROVIDER_NVIDIA_API_KEY",
		DocsURL:     "https://build.nvidia.com/explore/discover",
		Description: "NVIDIA NIM via build.nvidia.com. OpenAI-shaped inference for a broad catalog of open-weight models; free-trial credits available.",
	},
	{
		ID:          "ollama",
		Name:        "Ollama",
		Kind:        "local",
		Protocol:    "openai",
		BaseURL:     "http://127.0.0.1:11434/v1",
		DocsURL:     "https://github.com/ollama/ollama/blob/main/docs/openai.md",
		Description: "Local Ollama server. The fastest path to running open-weight models on your machine — single binary, one-command model pull.",
	},
	{
		ID:          "openai",
		Name:        "OpenAI",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.openai.com",
		APIKeyEnv:   "PROVIDER_OPENAI_API_KEY",
		DocsURL:     "https://developers.openai.com/api/docs/models",
		Description: "OpenAI's hosted API. GPT chat models and the o-series reasoning models via the canonical Chat Completions endpoint.",
	},
	{
		ID:          "openrouter",
		Name:        "OpenRouter",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://openrouter.ai/api/v1",
		APIKeyEnv:   "PROVIDER_OPENROUTER_API_KEY",
		DocsURL:     "https://openrouter.ai/docs/api/reference/overview",
		Description: "OpenRouter model router. One OpenAI-shaped endpoint for a broad multi-provider catalog with provider routing and fallback options.",
	},
	{
		ID:          "perplexity",
		Name:        "Perplexity",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.perplexity.ai",
		APIKeyEnv:   "PROVIDER_PERPLEXITY_API_KEY",
		ChatPath:    "/chat/completions",
		ModelsPath:  "/v1/models",
		DocsURL:     "https://docs.perplexity.ai/docs/sonar/openai-compatibility",
		Description: "Perplexity Sonar API. Search-grounded responses with inline citations; the Sonar Pro tier adds deeper research synthesis.",
	},
	{
		ID:          "requesty",
		Name:        "Requesty",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://router.requesty.ai/v1",
		APIKeyEnv:   "PROVIDER_REQUESTY_API_KEY",
		DocsURL:     "https://docs.requesty.ai/",
		Description: "Requesty model router. OpenAI-compatible gateway for routing requests across hosted model providers from a single API key.",
	},
	{
		ID:          "together_ai",
		Name:        "Together AI",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.together.xyz/v1",
		APIKeyEnv:   "PROVIDER_TOGETHER_AI_API_KEY",
		DocsURL:     "https://docs.together.ai/docs/inference-models",
		Description: "Together AI inference. Broad catalog of open-weight models at competitive per-token pricing.",
	},
	{
		ID:          "vercel_ai_gateway",
		Name:        "Vercel AI Gateway",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://ai-gateway.vercel.sh/v1",
		APIKeyEnv:   "PROVIDER_VERCEL_AI_GATEWAY_API_KEY",
		DocsURL:     "https://vercel.com/docs/ai-gateway",
		Description: "Vercel AI Gateway. OpenAI-compatible hosted gateway for routing through Vercel-managed model provider integrations.",
	},
	{
		ID:          "xai",
		Name:        "xAI",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.x.ai/v1",
		APIKeyEnv:   "PROVIDER_XAI_API_KEY",
		DocsURL:     "https://docs.x.ai/docs/models",
		Description: "xAI's hosted Grok API. Reasoning models from the Grok family with multimodal (text + image) input.",
	},
	{
		ID:          "zai",
		Name:        "z.ai",
		Kind:        "cloud",
		Protocol:    "openai",
		BaseURL:     "https://api.z.ai/api/paas/v4",
		APIKeyEnv:   "PROVIDER_ZAI_API_KEY",
		DocsURL:     "https://docs.z.ai/guides/llm/glm-4.6",
		Description: "Zhipu's z.ai. International-facing API for the GLM family of Chinese-trained models; OpenAI-shaped.",
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
		DefaultModel: p.DefaultModel,
	}
}
