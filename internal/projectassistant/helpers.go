package projectassistant

import (
	"encoding/json"
	"strings"

	"github.com/hecatehq/hecate/internal/projectwork"
)

func targetValue(action Action, key string) string {
	if action.Target == nil {
		return ""
	}
	return strings.TrimSpace(action.Target[key])
}

type draftRequest struct {
	title string
	brief string
}

func draftRequestParts(request string) draftRequest {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(request, "\r\n", "\n"), "\r", "\n"), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	if len(parts) == 0 {
		return draftRequest{}
	}
	return draftRequest{title: parts[0], brief: strings.Join(parts[1:], "\n\n")}
}

func validDraftDriverKind(kind string) bool {
	switch kind {
	case projectwork.AssignmentDriverHecateTask, projectwork.AssignmentDriverExternalAgent, projectwork.AssignmentDriverManual:
		return true
	default:
		return false
	}
}

func mustRawJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
