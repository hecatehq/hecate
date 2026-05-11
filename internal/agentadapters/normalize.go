package agentadapters

import (
	"encoding/json"
	"strings"
)

func normalizeOutput(adapterID, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	switch adapterID {
	case "codex":
		if normalized := normalizeJSONLines(raw); normalized != "" {
			return normalized
		}
	}
	return raw
}

func NormalizeOutput(adapterID, raw string) string {
	return normalizeOutput(adapterID, raw)
}

func NormalizeError(adapterName string, err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	if raw == "" {
		return ""
	}
	if adapterName == "Claude Code" && isAuthErrorText(raw) {
		return claudeCodeAuthErrorMessage()
	}
	parsed, ok := parseJSONRPCError(raw)
	if !ok {
		return raw
	}
	message := strings.TrimSpace(strings.TrimPrefix(parsed.Message, "Internal error:"))
	if message == "" {
		message = strings.TrimSpace(parsed.Message)
	}
	if message == "" {
		message = raw
	}
	if adapterName == "" {
		adapterName = "Agent adapter"
	}
	if adapterName == "Claude Code" && isAuthErrorText(message) {
		return claudeCodeAuthErrorMessage()
	}
	if kind := parsed.Data.ErrorKind; kind != "" {
		return adapterName + " error (" + kind + "): " + message
	}
	return adapterName + " error: " + message
}

func isAuthErrorText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "authentication required") ||
		strings.Contains(lower, "not logged in") ||
		strings.Contains(lower, "please log in") ||
		strings.Contains(lower, "please login") ||
		strings.Contains(lower, "unauthenticated")
}

type jsonRPCErrorPayload struct {
	Message string `json:"message"`
	Data    struct {
		ErrorKind string `json:"errorKind"`
	} `json:"data"`
}

func parseJSONRPCError(raw string) (jsonRPCErrorPayload, bool) {
	var payload jsonRPCErrorPayload
	if err := json.Unmarshal([]byte(raw), &payload); err == nil && payload.Message != "" {
		return payload, true
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return payload, false
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil || payload.Message == "" {
		return payload, false
	}
	return payload, true
}

func normalizeJSONLines(raw string) string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}
		out = append(out, extractAgentText(value)...)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func extractAgentText(value any) []string {
	switch v := value.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return nil
		}
		return []string{text}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, extractAgentText(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"text", "content", "message", "output", "summary"} {
			if extracted := extractAgentText(v[key]); len(extracted) > 0 {
				return extracted
			}
		}
		for _, key := range []string{"delta", "item", "result", "response"} {
			if extracted := extractAgentText(v[key]); len(extracted) > 0 {
				return extracted
			}
		}
	}
	return nil
}
