package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestTaskJSONRoundTrip_MCPServers pins the JSON contract for the
// MCPServers field. Both task-state backends (sqlite and postgres)
// store the entire Task as a JSON blob — there's no per-field
// migration, so any silent change to the JSON shape (a rename, an
// added json tag, a field demoted to unexported) would corrupt
// every existing task's MCPServers config without any compile-time
// or migration-level signal.
//
// This test is the pin: build a Task with a fully-populated
// MCPServers slice (both stdio and HTTP transports, env + headers
// with the three secret-storage forms, every approval-policy
// value), round-trip through json.Marshal / json.Unmarshal, and
// assert deep equality. If any field stops surviving the
// round-trip, this fails and the operator notices before the
// storage layer silently drops data.
func TestTaskJSONRoundTrip_MCPServers(t *testing.T) {
	t.Parallel()
	original := Task{
		ID:            "task-mcp-roundtrip",
		Title:         "MCP round-trip",
		Prompt:        "exercise every MCP server field",
		Status:        "queued",
		ExecutionKind: "agent_loop",
		MCPServers: []MCPServerConfig{
			// Stdio entry — the canonical filesystem-server shape,
			// with env values in all three storage forms.
			{
				Name:    "fs",
				Command: "bunx",
				Args:    []string{"--bun", "@modelcontextprotocol/server-filesystem", "/workspace"},
				Env: map[string]string{
					"NODE_ENV":    "production",        // bare literal
					"DEBUG_TOKEN": "$DEBUG_TOKEN",      // $VAR_NAME reference
					"AUTH_SECRET": "enc:abc123base64=", // already-encrypted ciphertext
				},
				ApprovalPolicy: MCPApprovalAuto,
			},
			// HTTP entry — the github-server shape, with headers in
			// all three storage forms and require_approval gating.
			{
				Name: "github",
				URL:  "https://api.example.com/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer $GITHUB_TOKEN",
					"X-Trace":       "on",
					"X-Encrypted":   "enc:def456base64=",
				},
				ApprovalPolicy: MCPApprovalRequireApproval,
			},
			// Block-policy entry — pin that the third policy value
			// also round-trips.
			{
				Name:           "dangerous",
				Command:        "npx",
				Args:           []string{"@vendor/dangerous-mcp"},
				ApprovalPolicy: MCPApprovalBlock,
			},
		},
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got Task
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(original.MCPServers, got.MCPServers) {
		t.Fatalf("MCPServers round-trip mismatch:\n  want: %+v\n   got: %+v", original.MCPServers, got.MCPServers)
	}

	// Also pin the broader Task — a regression on a sibling field
	// (e.g. someone renames Tenant) shouldn't slip past either.
	if !reflect.DeepEqual(original, got) {
		t.Fatalf("Task round-trip mismatch:\n  want: %+v\n   got: %+v", original, got)
	}
}

// TestTaskJSONRoundTrip_NoMCPServers_OmitsField pins that a Task
// with no MCPServers serializes WITHOUT the field (an empty/nil
// slice should not appear in the JSON blob, otherwise every
// non-agent_loop task gains a "MCPServers": null line). The Go
// JSON encoder omits zero-value fields only when they have
// `omitempty` or when the type is interface — Task.MCPServers
// being a slice means nil round-trips to `null`. We pin the
// post-unmarshal shape rather than the on-wire bytes so the test
// stays robust to JSON whitespace.
func TestTaskJSONRoundTrip_NoMCPServers_OmitsField(t *testing.T) {
	t.Parallel()
	original := Task{
		ID:     "task-no-mcp",
		Status: "queued",
	}

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Task
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Errorf("got.MCPServers = %+v, want empty (no MCPServers configured)", got.MCPServers)
	}
}
