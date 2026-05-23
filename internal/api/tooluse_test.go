package api

// End-to-end tool-use verification through the HTTP gateway layer.
//
// Four directions are exercised:
//   1. POST /v1/chat/completions — OpenAI request shape, tool_call response
//   2. POST /v1/chat/completions — tool result follow-up → final text answer
//   3. POST /v1/messages — Anthropic request shape, tool_use response (stop_reason=tool_use)
//   4. POST /v1/messages — tool_result follow-up → final text answer
//   5. POST /v1/messages — fakeProvider returns ToolCalls; verify Anthropic rendering
//   6. POST /v1/chat/completions — parallel tool calls in one response

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/pkg/types"
)

// ─── /v1/chat/completions ────────────────────────────────────────────────────

func TestChatCompletionsToolCallResponse(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:        "chatcmpl-tool-1",
			Model:     "gpt-4o-mini",
			CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
			Choices: []types.ChatChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: types.Message{
					Role: "assistant",
					ToolCalls: []types.ToolCall{{
						ID:   "call_abc",
						Type: "function",
						Function: types.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"Paris","unit":"celsius"}`,
						},
					}},
				},
			}},
			Usage: types.Usage{PromptTokens: 25, CompletionTokens: 15, TotalTokens: 40},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{
		"model": "gpt-4o-mini",
		"messages": [{"role":"user","content":"Weather in Paris?"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get current weather",
				"parameters": {"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string"}},"required":["location"]}
			}
		}],
		"tool_choice": "auto"
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp OpenAIChatCompletionResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", choice.FinishReason)
	}
	// content must be null when tool_calls are present
	if !choice.Message.Content.Null {
		t.Fatalf("content = %q (Null=%v), want JSON null when tool_calls present", choice.Message.Content.AsString(), choice.Message.Content.Null)
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Fatalf("tool_call.id = %q, want call_abc", tc.ID)
	}
	if tc.Type != "function" {
		t.Fatalf("tool_call.type = %q, want function", tc.Type)
	}
	if tc.Function.Name != "get_weather" {
		t.Fatalf("tool_call.function.name = %q, want get_weather", tc.Function.Name)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("tool_call.function.arguments invalid JSON: %v", err)
	}
	if args["location"] != "Paris" {
		t.Fatalf("arguments.location = %v, want Paris", args["location"])
	}
}

func TestChatCompletionsToolResultFollowUp(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var captured types.ChatRequest
	provider := &recordingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:        "chatcmpl-final",
				Model:     "gpt-4o-mini",
				CreatedAt: time.Unix(1_700_000_001, 0).UTC(),
				Choices: []types.ChatChoice{{
					Index:        0,
					FinishReason: "stop",
					Message:      types.Message{Role: "assistant", Content: "The weather in Paris is 22°C."},
				}},
				Usage: types.Usage{PromptTokens: 50, CompletionTokens: 8, TotalTokens: 58},
			},
		},
		captured: &captured,
	}

	handler := newTestHTTPHandler(logger, provider)
	// Multi-turn: user → assistant(tool_call) → tool(result) → final
	body := `{
		"model": "gpt-4o-mini",
		"messages": [
			{"role":"user","content":"Weather in Paris?"},
			{"role":"assistant","tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Paris\"}"}}]},
			{"role":"tool","content":"{\"temp\":22,\"unit\":\"celsius\"}","tool_call_id":"call_abc"}
		]
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp OpenAIChatCompletionResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop (final answer)", resp.Choices[0].FinishReason)
	}
	if got := resp.Choices[0].Message.Content.AsString(); !strings.Contains(got, "22°C") {
		t.Fatalf("content = %q, want final answer containing temperature", got)
	}

	// Verify all three messages were forwarded to the provider.
	if len(captured.Messages) != 3 {
		t.Fatalf("forwarded messages = %d, want 3", len(captured.Messages))
	}
	if captured.Messages[2].Role != "tool" {
		t.Fatalf("messages[2].role = %q, want tool", captured.Messages[2].Role)
	}
	if captured.Messages[2].ToolCallID != "call_abc" {
		t.Fatalf("messages[2].tool_call_id = %q, want call_abc", captured.Messages[2].ToolCallID)
	}
	// Assistant message must carry the tool call.
	if len(captured.Messages[1].ToolCalls) != 1 {
		t.Fatalf("messages[1].tool_calls = %d, want 1", len(captured.Messages[1].ToolCalls))
	}
}

func TestChatCompletionsParallelToolCalls(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "chatcmpl-parallel",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: types.Message{
					Role: "assistant",
					ToolCalls: []types.ToolCall{
						{ID: "call_1", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Paris"}`}},
						{ID: "call_2", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Berlin"}`}},
					},
				},
			}},
			Usage: types.Usage{PromptTokens: 35, CompletionTokens: 20, TotalTokens: 55},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Weather in Paris and Berlin?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{}}}]}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp OpenAIChatCompletionResponse
	json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp) //nolint:errcheck
	if len(resp.Choices[0].Message.ToolCalls) != 2 {
		t.Fatalf("tool_calls count = %d, want 2", len(resp.Choices[0].Message.ToolCalls))
	}
	if resp.Choices[0].Message.ToolCalls[0].ID != "call_1" ||
		resp.Choices[0].Message.ToolCalls[1].ID != "call_2" {
		t.Fatalf("tool_call IDs = %v, want call_1, call_2",
			[]string{resp.Choices[0].Message.ToolCalls[0].ID, resp.Choices[0].Message.ToolCalls[1].ID})
	}
}

// ─── /v1/messages ────────────────────────────────────────────────────────────

func TestMessagesEndpointToolUseResponse(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Provider returns tool_calls in internal format; handler renders as Anthropic tool_use.
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "msg-tool-1",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: types.Message{
					Role: "assistant",
					ToolCalls: []types.ToolCall{{
						ID:   "toolu_01",
						Type: "function",
						Function: types.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"London","unit":"celsius"}`,
						},
					}},
				},
			}},
			Usage: types.Usage{PromptTokens: 20, CompletionTokens: 12, TotalTokens: 32},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{
		"model": "gpt-4o-mini",
		"max_tokens": 128,
		"messages": [{"role":"user","content":"Weather in London?"}],
		"tools": [{
			"name": "get_weather",
			"description": "Get current weather",
			"input_schema": {"type":"object","properties":{"location":{"type":"string"},"unit":{"type":"string"}},"required":["location"]}
		}],
		"tool_choice": {"type":"auto"}
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp AnthropicMessagesResponse
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if resp.Type != "message" {
		t.Fatalf("type = %q, want message", resp.Type)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	// Find the tool_use block.
	var toolBlock *AnthropicOutboundContentBlock
	for i := range resp.Content {
		if resp.Content[i].Type == "tool_use" {
			toolBlock = &resp.Content[i]
			break
		}
	}
	if toolBlock == nil {
		t.Fatalf("no tool_use block in content: %+v", resp.Content)
	}
	if toolBlock.ID != "toolu_01" {
		t.Fatalf("tool_use.id = %q, want toolu_01", toolBlock.ID)
	}
	if toolBlock.Name != "get_weather" {
		t.Fatalf("tool_use.name = %q, want get_weather", toolBlock.Name)
	}
	var input map[string]any
	if err := json.Unmarshal(toolBlock.Input, &input); err != nil {
		t.Fatalf("tool_use.input invalid JSON: %v", err)
	}
	if input["location"] != "London" {
		t.Fatalf("tool_use.input.location = %v, want London", input["location"])
	}
	// Usage must be mapped correctly.
	if resp.Usage.InputTokens != 20 || resp.Usage.OutputTokens != 12 {
		t.Fatalf("usage = %+v, want input=20 output=12", resp.Usage)
	}
}

func TestMessagesEndpointToolResultFollowUp(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var captured types.ChatRequest
	provider := &recordingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "msg-final",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Index:        0,
					FinishReason: "stop",
					Message:      types.Message{Role: "assistant", Content: "The weather in London is 18°C."},
				}},
				Usage: types.Usage{PromptTokens: 60, CompletionTokens: 10, TotalTokens: 70},
			},
		},
		captured: &captured,
	}

	handler := newTestHTTPHandler(logger, provider)
	// Full Anthropic multi-turn: user → assistant(tool_use) → user(tool_result) → final
	body := `{
		"model":      "gpt-4o-mini",
		"max_tokens": 128,
		"messages": [
			{"role":"user","content":"Weather in London?"},
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"location":"London"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_01","content":[{"type":"text","text":"{\"temp\":18,\"unit\":\"celsius\"}"}]}
			]}
		]
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp AnthropicMessagesResponse
	json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp) //nolint:errcheck
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", resp.StopReason)
	}
	if len(resp.Content) == 0 || resp.Content[0].Type != "text" {
		t.Fatalf("content = %+v, want single text block", resp.Content)
	}
	if !strings.Contains(resp.Content[0].Text, "18°C") {
		t.Fatalf("content text = %q, want final answer containing temperature", resp.Content[0].Text)
	}

	// Verify message structure forwarded to the provider:
	// system(none) + user + assistant(tool_call) + tool(result) = 3 messages
	if len(captured.Messages) != 3 {
		t.Fatalf("forwarded messages = %d, want 3, got: %+v", len(captured.Messages), captured.Messages)
	}
	// Second message: assistant with tool_call
	assistMsg := captured.Messages[1]
	if assistMsg.Role != "assistant" {
		t.Fatalf("messages[1].role = %q, want assistant", assistMsg.Role)
	}
	if len(assistMsg.ToolCalls) != 1 || assistMsg.ToolCalls[0].ID != "toolu_01" {
		t.Fatalf("messages[1].tool_calls = %+v, want single call toolu_01", assistMsg.ToolCalls)
	}
	// Third message: tool result
	toolMsg := captured.Messages[2]
	if toolMsg.Role != "tool" {
		t.Fatalf("messages[2].role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "toolu_01" {
		t.Fatalf("messages[2].tool_call_id = %q, want toolu_01", toolMsg.ToolCallID)
	}
	if !strings.Contains(toolMsg.Content, "celsius") {
		t.Fatalf("messages[2].content = %q, want tool result containing celsius", toolMsg.Content)
	}
}

func TestMessagesEndpointParallelToolUseBlocks(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "msg-parallel",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: types.Message{
					Role: "assistant",
					ToolCalls: []types.ToolCall{
						{ID: "toolu_01", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Paris"}`}},
						{ID: "toolu_02", Type: "function", Function: types.ToolCallFunction{Name: "get_weather", Arguments: `{"location":"Berlin"}`}},
					},
				},
			}},
			Usage: types.Usage{PromptTokens: 30, CompletionTokens: 18, TotalTokens: 48},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{
		"model": "gpt-4o-mini", "max_tokens": 128,
		"messages": [{"role":"user","content":"Weather in Paris and Berlin?"}],
		"tools": [{"name":"get_weather","input_schema":{"type":"object"}}]
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp AnthropicMessagesResponse
	json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp) //nolint:errcheck
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	var toolBlocks []AnthropicOutboundContentBlock
	for _, b := range resp.Content {
		if b.Type == "tool_use" {
			toolBlocks = append(toolBlocks, b)
		}
	}
	if len(toolBlocks) != 2 {
		t.Fatalf("tool_use blocks = %d, want 2", len(toolBlocks))
	}
	ids := []string{toolBlocks[0].ID, toolBlocks[1].ID}
	if ids[0] != "toolu_01" || ids[1] != "toolu_02" {
		t.Fatalf("tool_use IDs = %v, want toolu_01, toolu_02", ids)
	}
}

func TestMessagesEndpointToolUseWithTextPreamble(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Provider returns both text and a tool call in the same message.
	provider := &fakeProvider{
		name: "openai",
		response: &types.ChatResponse{
			ID:    "msg-text-tool",
			Model: "gpt-4o-mini",
			Choices: []types.ChatChoice{{
				Index:        0,
				FinishReason: "tool_calls",
				Message: types.Message{
					Role:    "assistant",
					Content: "Let me check that for you.",
					ToolCalls: []types.ToolCall{{
						ID:   "toolu_03",
						Type: "function",
						Function: types.ToolCallFunction{
							Name:      "get_weather",
							Arguments: `{"location":"Rome"}`,
						},
					}},
				},
			}},
			Usage: types.Usage{PromptTokens: 22, CompletionTokens: 14, TotalTokens: 36},
		},
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{"model":"gpt-4o-mini","max_tokens":128,"messages":[{"role":"user","content":"Weather in Rome?"}],"tools":[{"name":"get_weather","input_schema":{}}]}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp AnthropicMessagesResponse
	json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp) //nolint:errcheck
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use", resp.StopReason)
	}
	// Should have both a text block and a tool_use block.
	hasText, hasTool := false, false
	for _, b := range resp.Content {
		if b.Type == "text" && strings.Contains(b.Text, "check") {
			hasText = true
		}
		if b.Type == "tool_use" && b.Name == "get_weather" {
			hasTool = true
		}
	}
	if !hasText {
		t.Fatalf("missing text preamble block in content: %+v", resp.Content)
	}
	if !hasTool {
		t.Fatalf("missing tool_use block in content: %+v", resp.Content)
	}
}

// TestChatCompletionsToolInputForwardedToAnthropicFormat verifies that tools
// submitted via /v1/chat/completions are forwarded to the provider correctly
// (via the normalizer) and that a tool_call response is rendered back in
// OpenAI format — even when the underlying provider is Anthropic-shaped.
func TestChatCompletionsNormalizesToolsAndRendersToolCalls(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	var captured types.ChatRequest
	provider := &recordingProvider{
		fakeProvider: fakeProvider{
			name: "openai",
			response: &types.ChatResponse{
				ID:    "chatcmpl-norm",
				Model: "gpt-4o-mini",
				Choices: []types.ChatChoice{{
					Index:        0,
					FinishReason: "tool_calls",
					Message: types.Message{
						Role: "assistant",
						ToolCalls: []types.ToolCall{{
							ID:       "call_n",
							Type:     "function",
							Function: types.ToolCallFunction{Name: "lookup", Arguments: `{"q":"foo"}`},
						}},
					},
				}},
				Usage: types.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		},
		captured: &captured,
	}

	handler := newTestHTTPHandler(logger, provider)
	body := `{
		"model": "gpt-4o-mini",
		"messages": [{"role":"user","content":"Look up foo"}],
		"tools": [{
			"type": "function",
			"function": {
				"name": "lookup",
				"description": "Search the database",
				"parameters": {"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}
			}
		}],
		"tool_choice": {"type":"function","function":{"name":"lookup"}}
	}`

	rec := performRequest(t, handler, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Verify tool was normalised into the internal request.
	if len(captured.Tools) != 1 {
		t.Fatalf("captured tools = %d, want 1", len(captured.Tools))
	}
	if captured.Tools[0].Function.Name != "lookup" {
		t.Fatalf("captured tool name = %q, want lookup", captured.Tools[0].Function.Name)
	}
	// Verify tool_choice was forwarded.
	if len(captured.ToolChoice) == 0 {
		t.Fatal("captured tool_choice is empty, want forwarded value")
	}
	if !strings.Contains(string(captured.ToolChoice), "lookup") {
		t.Fatalf("captured tool_choice = %s, want to contain 'lookup'", captured.ToolChoice)
	}

	// Verify response rendering.
	var resp OpenAIChatCompletionResponse
	json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&resp) //nolint:errcheck
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
	if resp.Choices[0].Message.ToolCalls[0].ID != "call_n" {
		t.Fatalf("tool_call.id = %q, want call_n", resp.Choices[0].Message.ToolCalls[0].ID)
	}
}
