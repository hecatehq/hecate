package chat

const (
	TurnKindDirectModel   = "direct_model"
	TurnKindHecateTask    = "hecate_task"
	TurnKindExternalAgent = "external_agent"
)

func MessageTurnKind(session Session, message Message) string {
	executionMode := firstNonEmpty(message.ExecutionMode, defaultExecutionMode(session))
	switch executionMode {
	case ExecutionModeExternalAgent:
		return TurnKindExternalAgent
	case ExecutionModeHecateTask:
		if !message.ToolsEnabled {
			return TurnKindDirectModel
		}
		return TurnKindHecateTask
	default:
		return ""
	}
}

func defaultExecutionMode(session Session) string {
	if session.AgentID != "" && session.AgentID != DefaultAgentID {
		return ExecutionModeExternalAgent
	}
	return ExecutionModeHecateTask
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
