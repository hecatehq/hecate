package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

// boolPtr is a tiny helper for the tools_enabled tri-state on
// CreateChatMessageRequest. A `*bool` field uses nil to mean "the
// client did not send this", which is what `chatRequestToolsEnabled`
// falls back on; non-nil values pin the tools state.
func boolPtr(b bool) *bool { return &b }

func TestNormalizeChatExecutionMode_ExplicitModeWinsOverSessionShape(t *testing.T) {
	// When the client sends `execution_mode` explicitly, the dispatcher
	// uses it verbatim (except for the legacy direct_model literal
	// which normalizes to hecate_task — covered in its own test
	// below). The tools_enabled argument is unused by the function
	// today; the dispatcher reads tools_enabled separately via
	// chatRequestToolsEnabled.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode(chat.ExecutionModeHecateTask, session, boolPtr(false)); got != chat.ExecutionModeHecateTask {
		t.Errorf("explicit hecate_task: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode(chat.ExecutionModeExternalAgent, session, boolPtr(true)); got != chat.ExecutionModeExternalAgent {
		t.Errorf("explicit external_agent: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_LegacyDirectModelNormalizesToHecateTask(t *testing.T) {
	// Older clients still send execution_mode="direct_model" for what
	// is now a Hecate-task turn with tools off. The dispatcher's
	// switch only knows hecate_task and external_agent, so the
	// normalize step folds the legacy literal forward. The tools-off
	// intent is recovered separately by chatRequestToolsEnabled.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode(chat.LegacyExecutionModeDirectModel, session, nil); got != chat.ExecutionModeHecateTask {
		t.Errorf("legacy direct_model: got %q, want hecate_task", got)
	}
}

func TestNormalizeChatExecutionMode_ExternalSessionPinsExternalAgent(t *testing.T) {
	// When the session itself is external (agent_id != "hecate"), the
	// dispatcher pins external_agent. Agent identity is the source
	// of truth here.
	session := chat.Session{AgentID: "claude_code"}
	if got := normalizeChatExecutionMode("", session, nil); got != chat.ExecutionModeExternalAgent {
		t.Errorf("external session + no signals: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_HecateSessionDefaultsToHecateTask(t *testing.T) {
	// Every Hecate-side turn — tools-on or tools-off — now normalizes
	// to hecate_task. The tools-on/off axis lives on the per-message
	// ToolsEnabled boolean, not on execution_mode.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode("", session, nil); got != chat.ExecutionModeHecateTask {
		t.Errorf("hecate session + no signals: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode("", session, boolPtr(true)); got != chat.ExecutionModeHecateTask {
		t.Errorf("hecate session + tools_enabled=true: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode("", session, boolPtr(false)); got != chat.ExecutionModeHecateTask {
		t.Errorf("hecate session + tools_enabled=false: got %q, want hecate_task", got)
	}
}

func TestChatRequestToolsEnabled_PreservesLegacyDirectModelAsToolsOff(t *testing.T) {
	// The legacy execution_mode="direct_model" literal still encodes
	// tools-off intent for older clients. chatRequestToolsEnabled
	// (the helper the dispatcher uses to derive the per-turn tools
	// state) must recognize it so the resulting Message.ToolsEnabled
	// matches what the client meant.
	cases := []struct {
		name string
		req  CreateChatMessageRequest
		want bool
	}{
		{"explicit tools_enabled=true wins", CreateChatMessageRequest{ToolsEnabled: boolPtr(true), ExecutionMode: chat.LegacyExecutionModeDirectModel}, true},
		{"explicit tools_enabled=false wins", CreateChatMessageRequest{ToolsEnabled: boolPtr(false), ExecutionMode: chat.ExecutionModeHecateTask}, false},
		{"legacy direct_model → tools off", CreateChatMessageRequest{ExecutionMode: chat.LegacyExecutionModeDirectModel}, false},
		{"hecate_task → tools on", CreateChatMessageRequest{ExecutionMode: chat.ExecutionModeHecateTask}, true},
		{"unset → tools off (preserves pre-tools_enabled default)", CreateChatMessageRequest{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatRequestToolsEnabled(tc.req); got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
