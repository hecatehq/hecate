package api

import "encoding/json"

// AnthropicMessagesRequest is the inbound shape for POST /v1/messages.
// Mirrors https://docs.anthropic.com/en/api/messages closely enough for
// drop-in SDK compatibility. Fields not used by the gateway are still
// parsed (as json.RawMessage where structure is varied) so we can error
// on malformed payloads without silently dropping them.
type AnthropicMessagesRequest struct {
	Model         string                    `json:"model"`
	System        json.RawMessage           `json:"system,omitempty"`
	Messages      []AnthropicInboundMessage `json:"messages"`
	MaxTokens     int                       `json:"max_tokens"`
	Temperature   float64                   `json:"temperature,omitempty"`
	TopP          float64                   `json:"top_p,omitempty"`
	TopK          int                       `json:"top_k,omitempty"`
	StopSequences []string                  `json:"stop_sequences,omitempty"`
	Metadata      *AnthropicInboundMetadata `json:"metadata,omitempty"`
	Tools         []AnthropicInboundTool    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage           `json:"tool_choice,omitempty"`
	Stream        bool                      `json:"stream,omitempty"`

	// Extended thinking — pass {"type":"enabled","budget_tokens":N}.
	Thinking json.RawMessage `json:"thinking,omitempty"`
	// Anthropic beta features (e.g. ["interleaved-thinking-2025-02-19"]).
	// Hecate forwards these as the anthropic-beta request header.
	Betas []string `json:"betas,omitempty"`

	// Gateway-specific extensions (optional; ignored by Anthropic SDK but
	// useful when calling Hecate directly).
	Provider string `json:"provider,omitempty"`
}

type AnthropicInboundMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

type AnthropicInboundTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// AnthropicInboundMessage accepts content as either a plain string or an
// array of content blocks. The raw payload is kept and decoded in the
// normalizer.
type AnthropicInboundMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicInboundContentBlock covers the block variants we convert.
type AnthropicInboundContentBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	// prompt caching
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
	// extended thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking opaque data
}

// AnthropicMessagesResponse is the outbound /v1/messages shape.
type AnthropicMessagesResponse struct {
	ID           string                          `json:"id"`
	Type         string                          `json:"type"`
	Role         string                          `json:"role"`
	Model        string                          `json:"model"`
	Content      []AnthropicOutboundContentBlock `json:"content"`
	StopReason   string                          `json:"stop_reason"`
	StopSequence *string                         `json:"stop_sequence"`
	Usage        AnthropicOutboundUsage          `json:"usage"`
}

type AnthropicOutboundContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// extended thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking
}

type AnthropicOutboundUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
