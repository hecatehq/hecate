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
