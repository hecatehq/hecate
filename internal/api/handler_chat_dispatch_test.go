package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

// boolPtr is a tiny helper for the tools_enabled tri-state on
// CreateChatMessageRequest. A `*bool` field uses nil to mean "the
// client did not send this", which is what
// `normalizeChatExecutionMode` falls back on; non-nil values pin the
// dispatch.
func boolPtr(b bool) *bool { return &b }

func TestNormalizeChatExecutionMode_ExplicitModeWinsOverEverything(t *testing.T) {
	// When the client sends `execution_mode` explicitly, the dispatcher
	// uses it verbatim regardless of session shape or tools_enabled.
	// This preserves back-compat with older clients that send the
	// literal directly.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode(chat.ExecutionModeHecateTask, session, boolPtr(false)); got != chat.ExecutionModeHecateTask {
		t.Errorf("explicit hecate_task + tools_enabled=false: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode(chat.ExecutionModeDirectModel, session, boolPtr(true)); got != chat.ExecutionModeDirectModel {
		t.Errorf("explicit direct_model + tools_enabled=true: got %q, want direct_model", got)
	}
	if got := normalizeChatExecutionMode(chat.ExecutionModeExternalAgent, session, boolPtr(true)); got != chat.ExecutionModeExternalAgent {
		t.Errorf("explicit external_agent: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_ExternalSessionPinsExternalAgent(t *testing.T) {
	// When the session itself is external (agent_id != "hecate"), the
	// dispatcher pins external_agent no matter what tools_enabled
	// signal the client may have sent. Agent identity is the source
	// of truth here.
	session := chat.Session{AgentID: "claude_code"}
	if got := normalizeChatExecutionMode("", session, nil); got != chat.ExecutionModeExternalAgent {
		t.Errorf("external session + no signals: got %q, want external_agent", got)
	}
	if got := normalizeChatExecutionMode("", session, boolPtr(true)); got != chat.ExecutionModeExternalAgent {
		t.Errorf("external session + tools_enabled=true: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_HecateSessionDerivesFromToolsEnabled(t *testing.T) {
	// The new wire shape: Hecate-side turns send `tools_enabled` and
	// omit `execution_mode`. The dispatcher routes hecate_task vs
	// direct_model on the boolean.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode("", session, boolPtr(true)); got != chat.ExecutionModeHecateTask {
		t.Errorf("hecate session + tools_enabled=true: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode("", session, boolPtr(false)); got != chat.ExecutionModeDirectModel {
		t.Errorf("hecate session + tools_enabled=false: got %q, want direct_model", got)
	}
}

func TestNormalizeChatExecutionMode_HecateSessionDefaultsToDirectModelWhenSignalsAbsent(t *testing.T) {
	// Pre-tools_enabled clients sent neither field on the model-chat
	// path and relied on the dispatcher's default. Keep that
	// contract: a Hecate session with no execution_mode and no
	// tools_enabled still routes to direct_model.
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode("", session, nil); got != chat.ExecutionModeDirectModel {
		t.Errorf("hecate session + no signals: got %q, want direct_model", got)
	}
}
