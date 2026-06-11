package chat

import "testing"

func TestMessageTurnKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session Session
		message Message
		want    string
	}{
		{
			name:    "hecate tools off is direct model",
			session: Session{AgentID: DefaultAgentID},
			message: Message{ExecutionMode: ExecutionModeHecateTask, ToolsEnabled: false},
			want:    TurnKindDirectModel,
		},
		{
			name:    "hecate tools on is task",
			session: Session{AgentID: DefaultAgentID},
			message: Message{ExecutionMode: ExecutionModeHecateTask, ToolsEnabled: true},
			want:    TurnKindHecateTask,
		},
		{
			name:    "external execution mode",
			session: Session{AgentID: "codex"},
			message: Message{ExecutionMode: ExecutionModeExternalAgent},
			want:    TurnKindExternalAgent,
		},
		{
			name:    "external session default",
			session: Session{AgentID: "codex"},
			message: Message{},
			want:    TurnKindExternalAgent,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := MessageTurnKind(tt.session, tt.message); got != tt.want {
				t.Fatalf("MessageTurnKind() = %q, want %q", got, tt.want)
			}
		})
	}
}
