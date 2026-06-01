package api

import (
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
)

func TestNormalizeChatExecutionMode_ExplicitModeWins(t *testing.T) {
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode(chat.ExecutionModeHecateTask, session); got != chat.ExecutionModeHecateTask {
		t.Errorf("explicit hecate_task: got %q, want hecate_task", got)
	}
	if got := normalizeChatExecutionMode(chat.ExecutionModeExternalAgent, session); got != chat.ExecutionModeExternalAgent {
		t.Errorf("explicit external_agent: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_ExternalSessionPinsExternalAgent(t *testing.T) {
	session := chat.Session{AgentID: "claude_code"}
	if got := normalizeChatExecutionMode("", session); got != chat.ExecutionModeExternalAgent {
		t.Errorf("external session + no signals: got %q, want external_agent", got)
	}
}

func TestNormalizeChatExecutionMode_HecateSessionDefaultsToHecateTask(t *testing.T) {
	session := chat.Session{AgentID: chat.DefaultAgentID}
	if got := normalizeChatExecutionMode("", session); got != chat.ExecutionModeHecateTask {
		t.Errorf("hecate session + no signals: got %q, want hecate_task", got)
	}
}
