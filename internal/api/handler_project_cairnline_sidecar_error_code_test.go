package api

import (
	"encoding/json"
	"testing"

	"github.com/hecatehq/hecate/internal/mcp"
	"github.com/hecatehq/hecate/internal/orchestrator"
)

// sidecarToolErrorResult builds a failed CachedMCPToolCallResult with the given
// prose text and raw structuredContent, mirroring what the sidecar client sees
// from a Cairnline tool-level error.
func sidecarToolErrorResult(text string, structured json.RawMessage) *orchestrator.CachedMCPToolCallResult {
	return &orchestrator.CachedMCPToolCallResult{
		Text:    text,
		IsError: true,
		Result: mcp.CallToolResult{
			Content:           mcp.TextContent(text),
			StructuredContent: structured,
			IsError:           true,
		},
	}
}

func TestProjectCairnlineSidecarToolErrorCode(t *testing.T) {
	tests := []struct {
		name   string
		result *orchestrator.CachedMCPToolCallResult
		want   string
	}{
		{
			name:   "nil result",
			result: nil,
			want:   "",
		},
		{
			name:   "empty structured content",
			result: sidecarToolErrorResult("boom", nil),
			want:   "",
		},
		{
			name:   "json null structured content",
			result: sidecarToolErrorResult("boom", json.RawMessage("null")),
			want:   "",
		},
		{
			name:   "valid not_found code",
			result: sidecarToolErrorResult(`root "x" not found`, json.RawMessage(`{"error":{"code":"not_found","message":"root \"x\" not found"}}`)),
			want:   "not_found",
		},
		{
			name:   "valid conflict code",
			result: sidecarToolErrorResult("state conflict", json.RawMessage(`{"error":{"code":"conflict","message":"state conflict"}}`)),
			want:   "conflict",
		},
		{
			name:   "malformed json",
			result: sidecarToolErrorResult("boom", json.RawMessage(`{"error":`)),
			want:   "",
		},
		{
			name:   "error object without code",
			result: sidecarToolErrorResult("boom", json.RawMessage(`{"error":{"message":"boom"}}`)),
			want:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectCairnlineSidecarToolErrorCode(tc.result); got != tc.want {
				t.Fatalf("projectCairnlineSidecarToolErrorCode = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestProjectCairnlineSidecarToolErrorIsNotFound covers the classification the
// four sidecar read sites rely on: a structured not_found code (and the legacy
// prose fallback for pre-contract sidecars) resolves to the not-found path
// (ok=false -> HTTP 404), while any other code or prose resolves to the
// read-failure path (HTTP 502).
func TestProjectCairnlineSidecarToolErrorIsNotFound(t *testing.T) {
	tests := []struct {
		name   string
		result *orchestrator.CachedMCPToolCallResult
		want   bool
	}{
		{
			name:   "structured not_found maps to not-found",
			result: sidecarToolErrorResult("unrelated prose", json.RawMessage(`{"error":{"code":"not_found","message":"gone"}}`)),
			want:   true,
		},
		{
			name:   "structured conflict is not not-found",
			result: sidecarToolErrorResult("gone not found", json.RawMessage(`{"error":{"code":"conflict","message":"state conflict"}}`)),
			want:   false,
		},
		{
			name:   "structured internal is not not-found",
			result: sidecarToolErrorResult("gone not found", json.RawMessage(`{"error":{"code":"internal","message":"boom"}}`)),
			want:   false,
		},
		{
			name:   "empty structured falls back to prose not found",
			result: sidecarToolErrorResult("projects.get: root not found", nil),
			want:   true,
		},
		{
			name:   "empty structured with unrelated prose is read-failure",
			result: sidecarToolErrorResult("projects.get: internal error", nil),
			want:   false,
		},
		{
			name:   "nil result is not not-found",
			result: nil,
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectCairnlineSidecarToolErrorIsNotFound(tc.result); got != tc.want {
				t.Fatalf("projectCairnlineSidecarToolErrorIsNotFound = %v, want %v", got, tc.want)
			}
		})
	}
}
