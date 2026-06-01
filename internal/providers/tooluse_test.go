package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/pkg/types"
)

// ─── OpenAI provider ────────────────────────────────────────────────────────

func TestOpenAIProviderSendsToolsInRequest(t *testing.T) {
	t.Parallel()

	var capturedBody openAIChatCompletionRequest
	provider := newOpenAITestProvider(t, func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		return openAIToolCallResponse(t, "call_abc", "get_weather", `{"location":"Paris"}`), nil
	})

	tools := calcWeatherTool()
	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:      "gpt-4o-mini",
		MaxTokens:  32,
		Messages:   []types.Message{{Role: "user", Content: "Weather in Paris?"}},
		Tools:      tools,
		ToolChoice: json.RawMessage(`"auto"`),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if len(capturedBody.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(capturedBody.Tools))
	}
	got := capturedBody.Tools[0]
	if got.Type != "function" {
		t.Fatalf("tool.type = %q, want function", got.Type)
	}
	if got.Function.Name != "get_weather" {
		t.Fatalf("tool.function.name = %q, want get_weather", got.Function.Name)
	}
	if got.Function.Description != "Get current weather" {
		t.Fatalf("tool.function.description = %q, want Get current weather", got.Function.Description)
	}
	// Parameters must be the JSON schema we sent.
	if !strings.Contains(string(got.Function.Parameters), "location") {
		t.Fatalf("tool.function.parameters = %s, want location schema", got.Function.Parameters)
	}
	// tool_choice must be forwarded.
	var tc string
	if err := json.Unmarshal(capturedBody.ToolChoice, &tc); err != nil || tc != "auto" {
		t.Fatalf("tool_choice = %s, want \"auto\"", capturedBody.ToolChoice)
	}
}

func TestOpenAIProviderMapsToolCallResponse(t *testing.T) {
	t.Parallel()

	provider := newOpenAITestProvider(t, func(r *http.Request) (*http.Response, error) {
		return openAIToolCallResponse(t, "call_xyz", "get_weather", `{"location":"Tokyo","unit":"celsius"}`), nil
	})

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:     "gpt-4o-mini",
		MaxTokens: 64,
		Messages:  []types.Message{{Role: "user", Content: "Weather in Tokyo?"}},
		Tools:     calcWeatherTool(),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "call_xyz" {
		t.Fatalf("tool_call.id = %q, want call_xyz", tc.ID)
	}
	if tc.Type != "function" {
		t.Fatalf("tool_call.type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call.function.name = %q, want get_weather", tc.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments are not valid JSON: %v", err)
	}
	if args["location"] != "Tokyo" {
		t.Fatalf("arguments.location = %#v, want Tokyo", args["location"])
	}
}

func TestOpenAIProviderSendsToolResultAsToolRoleMessage(t *testing.T) {
	t.Parallel()

	var capturedBody openAIChatCompletionRequest
	provider := newOpenAITestProvider(t, func(r *http.Request) (*http.Response, error) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		return openAITextResponse(t, "The weather in Tokyo is 22°C."), nil
	})

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "gpt-4o-mini",
		Messages: []types.Message{
			{Role: "user", Content: "Weather in Tokyo?"},
			{
				Role: "assistant",
				ToolCalls: []types.ToolCall{{
					ID:   "call_xyz",
					Type: "function",
					Function: types.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"location":"Tokyo"}`,
					},
				}},
			},
			{Role: "tool", Content: `{"temp":22,"unit":"celsius"}`, ToolCallID: "call_xyz"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if len(capturedBody.Messages) != 3 {
		t.Fatalf("messages count = %d, want 3", len(capturedBody.Messages))
	}
	// tool message must be sent with role=tool and tool_call_id set.
	toolMsg := capturedBody.Messages[2]
	if toolMsg.Role != "tool" {
		t.Fatalf("messages[2].role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_xyz" {
		t.Fatalf("messages[2].tool_call_id = %q, want call_xyz", toolMsg.ToolCallID)
	}
	if got := toolMsg.Content.AsString(); !strings.Contains(got, "celsius") {
		t.Fatalf("messages[2].content = %q, want tool result JSON containing celsius", got)
	}
	// assistant message must have tool_calls, JSON-null content.
	assistMsg := capturedBody.Messages[1]
	if len(assistMsg.ToolCalls) != 1 {
		t.Fatalf("messages[1].tool_calls count = %d, want 1", len(assistMsg.ToolCalls))
	}
	if !assistMsg.Content.Null {
		t.Fatalf("messages[1].content = %q (Null=%v), want JSON null for tool_calls message", assistMsg.Content.AsString(), assistMsg.Content.Null)
	}
}

func TestOpenAIProviderMultipleParallelToolCalls(t *testing.T) {
	t.Parallel()

	var capturedBody openAIChatCompletionRequest
	provider := newOpenAITestProvider(t, func(r *http.Request) (*http.Response, error) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		// Return two tool_calls in one response.
		resp := openAIChatCompletionResponse{
			ID:    "chatcmpl-parallel",
			Model: "gpt-4o-mini",
			Choices: []openAIChatCompletionChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: openAIChatMessage{
					Role: "assistant",
					ToolCalls: []openAIToolCall{
						{ID: "call_1", Type: "function", Function: openAIToolCallFunction{Name: "get_weather", Arguments: `{"location":"Paris"}`}},
						{ID: "call_2", Type: "function", Function: openAIToolCallFunction{Name: "get_weather", Arguments: `{"location":"Berlin"}`}},
					},
				},
			}},
			Usage: openAIUsage{PromptTokens: 30, CompletionTokens: 20, TotalTokens: 50},
		}
		b, _ := json.Marshal(resp)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(b)),
		}, nil
	})

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:     "gpt-4o-mini",
		MaxTokens: 128,
		Messages:  []types.Message{{Role: "user", Content: "Weather in Paris and Berlin?"}},
		Tools:     calcWeatherTool(),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Fatalf("tool_calls count = %d, want 2", len(resp.Choices[0].Message.ToolCalls))
	}
	if resp.Choices[0].Message.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool_calls[0].id = %q, want call_1", resp.Choices[0].Message.ToolCalls[0].ID)
	}
	if resp.Choices[0].Message.ToolCalls[1].ID != "call_2" {
		t.Fatalf("tool_calls[1].id = %q, want call_2", resp.Choices[0].Message.ToolCalls[1].ID)
	}
}

// ─── Anthropic provider ─────────────────────────────────────────────────────

func TestAnthropicProviderSendsToolsWithInputSchema(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		return anthropicToolUseResponse(t, "toolu_01", "get_weather", `{"location":"London"}`), nil
	})

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:      "claude-opus-4-5",
		MaxTokens:  64,
		Messages:   []types.Message{{Role: "user", Content: "Weather in London?"}},
		Tools:      calcWeatherTool(),
		ToolChoice: json.RawMessage(`"auto"`),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	toolsRaw, ok := capturedBody["tools"]
	if !ok {
		t.Fatal("upstream body missing tools field")
	}
	toolsBytes, _ := json.Marshal(toolsRaw)
	var tools []map[string]any
	if err := json.Unmarshal(toolsBytes, &tools); err != nil {
		t.Fatalf("tools is not a JSON array: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	tool := tools[0]
	if tool["name"] != "get_weather" {
		t.Fatalf("tool.name = %q, want get_weather", tool["name"])
	}
	// Anthropic format uses input_schema, not function.parameters.
	if _, ok := tool["input_schema"]; !ok {
		t.Fatalf("tool missing input_schema field (got: %v)", tool)
	}
	// No "type: function" wrapper.
	if _, ok := tool["function"]; ok {
		t.Fatal("Anthropic tool must not have a 'function' wrapper")
	}
	// tool_choice must be translated to Anthropic format.
	tcRaw, _ := json.Marshal(capturedBody["tool_choice"])
	if !strings.Contains(string(tcRaw), `"auto"`) {
		t.Fatalf("tool_choice = %s, want {type:auto}", tcRaw)
	}
}

func TestAnthropicProviderMapsToolUseResponse(t *testing.T) {
	t.Parallel()

	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		return anthropicToolUseResponse(t, "toolu_xyz", "get_weather", `{"location":"Sydney","unit":"celsius"}`), nil
	})

	resp, err := provider.Chat(context.Background(), types.ChatRequest{
		Model:     "claude-opus-4-5",
		MaxTokens: 64,
		Messages:  []types.Message{{Role: "user", Content: "Weather in Sydney?"}},
		Tools:     calcWeatherTool(),
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls (translated from tool_use)", choice.FinishReason)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "toolu_xyz" {
		t.Fatalf("tool_call.id = %q, want toolu_xyz", tc.ID)
	}
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call.function.name = %q, want get_weather", tc.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("arguments are not valid JSON: %v", err)
	}
	if args["location"] != "Sydney" {
		t.Fatalf("arguments.location = %q, want Sydney", args["location"])
	}
}

func TestAnthropicProviderBatchesToolResultsIntoUserMessage(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		return anthropicTextResponse(t, "The weather in London is 18°C."), nil
	})

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{
			{Role: "user", Content: "Weather in London?"},
			{Role: "assistant", ToolCalls: []types.ToolCall{{
				ID:       "toolu_01",
				Type:     "function",
				Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"London"}`},
			}}},
			// Two consecutive tool results should be batched into one user message.
			{Role: "tool", Content: `{"temp":18,"unit":"celsius"}`, ToolCallID: "toolu_01"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	msgs, _ := capturedBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("upstream messages count = %d, want 3 (user, assistant, user[tool_result])", len(msgs))
	}
	lastMsg := msgs[2].(map[string]any)
	if lastMsg["role"] != "user" {
		t.Fatalf("last message role = %q, want user (tool_results wrapped in user)", lastMsg["role"])
	}
	lastContentRaw, _ := json.Marshal(lastMsg["content"])
	if !strings.Contains(string(lastContentRaw), "tool_result") {
		t.Fatalf("last message content = %s, want tool_result block", lastContentRaw)
	}
	if !strings.Contains(string(lastContentRaw), "toolu_01") {
		t.Fatalf("last message content = %s, missing tool_use_id", lastContentRaw)
	}
}

func TestAnthropicProviderBatchesMultipleConsecutiveToolResults(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]any
	provider := newAnthropicTestProvider(t, func(r *http.Request) (*http.Response, error) {
		json.NewDecoder(r.Body).Decode(&capturedBody) //nolint:errcheck
		return anthropicTextResponse(t, "Paris is 20°C, Berlin is 15°C."), nil
	})

	_, err := provider.Chat(context.Background(), types.ChatRequest{
		Model: "claude-opus-4-5",
		Messages: []types.Message{
			{Role: "user", Content: "Weather in Paris and Berlin?"},
			{Role: "assistant", ToolCalls: []types.ToolCall{
				{ID: "toolu_01", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Paris"}`}},
				{ID: "toolu_02", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Berlin"}`}},
			}},
			{Role: "tool", Content: `{"temp":20}`, ToolCallID: "toolu_01"},
			{Role: "tool", Content: `{"temp":15}`, ToolCallID: "toolu_02"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	msgs, _ := capturedBody["messages"].([]any)
	// user → assistant → user[tool_result×2] = 3 messages
	if len(msgs) != 3 {
		t.Fatalf("upstream messages count = %d, want 3 (parallel results batched into one user msg)", len(msgs))
	}
	lastContentRaw, _ := json.Marshal(msgs[2].(map[string]any)["content"])
	// Both tool_use_ids must appear.
	if !strings.Contains(string(lastContentRaw), "toolu_01") ||
		!strings.Contains(string(lastContentRaw), "toolu_02") {
		t.Fatalf("batched tool_results missing one of the IDs: %s", lastContentRaw)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func newOpenAITestProvider(t *testing.T, transport func(*http.Request) (*http.Response, error)) *OpenAICompatibleProvider {
	t.Helper()
	p := NewOpenAIProvider(config.OpenAICompatibleProviderConfig{
		Name:         "openai",
		Kind:         "cloud",
		BaseURL:      "https://api.openai.test",
		APIKey:       "test-key",
		Timeout:      5 * time.Second,
		DefaultModel: "gpt-4o-mini",
	}, nil)
	p.cachedCaps = Capabilities{
		Name:         "openai",
		Kind:         KindCloud,
		DefaultModel: "gpt-4o-mini",
		Models:       []string{"gpt-4o-mini"},
	}
	p.capsExpiry = time.Now().Add(time.Minute)
	p.httpClient = &http.Client{Transport: testRoundTripperFunc(transport)}
	return p
}

func newAnthropicTestProvider(t *testing.T, transport func(*http.Request) (*http.Response, error)) *AnthropicProvider {
	t.Helper()
	p := NewAnthropicProvider(config.OpenAICompatibleProviderConfig{
		Name:         "anthropic",
		Kind:         "cloud",
		Protocol:     "anthropic",
		BaseURL:      "https://api.anthropic.test",
		APIKey:       "test-key",
		APIVersion:   "2023-06-01",
		Timeout:      5 * time.Second,
		DefaultModel: "claude-opus-4-5",
	}, nil)
	p.cachedCaps = Capabilities{
		Name:         "anthropic",
		Kind:         KindCloud,
		DefaultModel: "claude-opus-4-5",
		Models:       []string{"claude-opus-4-5"},
	}
	p.capsExpiry = time.Now().Add(time.Minute)
	p.httpClient = &http.Client{Transport: testRoundTripperFunc(transport)}
	return p
}

func calcWeatherTool() []types.Tool {
	return []types.Tool{{
		Type: "function",
		Function: types.ToolFunction{
			Name:        "get_weather",
			Description: "Get current weather",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]}`),
		},
	}}
}

func openAIToolCallResponse(t *testing.T, callID, fnName, args string) *http.Response {
	t.Helper()
	resp := openAIChatCompletionResponse{
		ID:    "chatcmpl-tool",
		Model: "gpt-4o-mini",
		Choices: []openAIChatCompletionChoice{{
			Index:        0,
			FinishReason: "tool_calls",
			Message: openAIChatMessage{
				Role: "assistant",
				ToolCalls: []openAIToolCall{{
					ID:   callID,
					Type: "function",
					Function: openAIToolCallFunction{
						Name:      fnName,
						Arguments: args,
					},
				}},
			},
		}},
		Usage: openAIUsage{PromptTokens: 20, CompletionTokens: 15, TotalTokens: 35},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func openAITextResponse(t *testing.T, text string) *http.Response {
	t.Helper()
	resp := openAIChatCompletionResponse{
		ID:    "chatcmpl-text",
		Model: "gpt-4o-mini",
		Choices: []openAIChatCompletionChoice{{
			Index:        0,
			FinishReason: "stop",
			Message:      openAIChatMessage{Role: "assistant", Content: openAIMessageContent{Text: text}},
		}},
		Usage: openAIUsage{PromptTokens: 40, CompletionTokens: 10, TotalTokens: 50},
	}
	b, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func anthropicToolUseResponse(t *testing.T, toolID, fnName, inputJSON string) *http.Response {
	t.Helper()
	var inputRaw json.RawMessage
	if err := json.Unmarshal([]byte(inputJSON), &inputRaw); err != nil {
		t.Fatalf("invalid inputJSON: %v", err)
	}
	resp := anthropicMessagesResponse{
		ID:         "msg_tool",
		Model:      "claude-opus-4-5",
		Role:       "assistant",
		StopReason: "tool_use",
		Content: []anthropicContentBlock{
			{Type: "tool_use", ID: toolID, Name: fnName, Input: inputRaw},
		},
		Usage: anthropicUsage{InputTokens: 20, OutputTokens: 12},
	}
	b, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func anthropicTextResponse(t *testing.T, text string) *http.Response {
	t.Helper()
	resp := anthropicMessagesResponse{
		ID:         "msg_text",
		Model:      "claude-opus-4-5",
		Role:       "assistant",
		StopReason: "end_turn",
		Content:    []anthropicContentBlock{{Type: "text", Text: text}},
		Usage:      anthropicUsage{InputTokens: 50, OutputTokens: 8},
	}
	b, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}
