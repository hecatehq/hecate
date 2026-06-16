package chat

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultCompactRetainMessages = 8
	DefaultCompactMinMessages    = 18

	compactSummaryMaxRunes = 8000
	compactLineMaxRunes    = 600
)

type CompactTranscriptOptions struct {
	Now            time.Time
	RetainMessages int
	MinMessages    int
}

type CompactTranscriptResult struct {
	Summary   ContextSummary
	Compacted bool
}

func CompactTranscriptSummary(session Session, opts CompactTranscriptOptions) CompactTranscriptResult {
	retain := opts.RetainMessages
	if retain <= 0 {
		retain = DefaultCompactRetainMessages
	}
	minMessages := opts.MinMessages
	if minMessages <= 0 {
		minMessages = retain + 1
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	visible := compactableTranscriptMessages(session.Messages)
	current := cloneContextSummary(session.ContextSummary)
	if len(visible) < minMessages || len(visible) <= retain {
		return CompactTranscriptResult{Summary: current}
	}

	start := 0
	if current.ThroughMessageID != "" {
		found := false
		for i, message := range visible {
			if message.ID != current.ThroughMessageID {
				continue
			}
			start = i + 1
			found = true
			break
		}
		if !found {
			return CompactTranscriptResult{Summary: current}
		}
	}

	end := len(visible) - retain
	if end <= start {
		return CompactTranscriptResult{Summary: current}
	}

	nextMessages := visible[start:end]
	nextSummary := ContextSummary{
		Content:          buildCompactTranscriptSummary(current.Content, nextMessages),
		MessageCount:     current.MessageCount + len(nextMessages),
		ThroughMessageID: nextMessages[len(nextMessages)-1].ID,
		CompactedAt:      now.UTC(),
	}
	return CompactTranscriptResult{Summary: nextSummary, Compacted: true}
}

func TranscriptSummaryPrompt(summary ContextSummary) string {
	content := strings.TrimSpace(summary.Content)
	if content == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("Earlier chat transcript compacted by Hecate")
	if summary.MessageCount > 0 {
		b.WriteString(fmt.Sprintf(" (%d messages", summary.MessageCount))
		if summary.ThroughMessageID != "" {
			b.WriteString(" through ")
			b.WriteString(summary.ThroughMessageID)
		}
		b.WriteString(")")
	}
	b.WriteString(":\n")
	b.WriteString(content)
	b.WriteString("\n\nUse this as background. Newer transcript messages follow in full.")
	return b.String()
}

func compactableTranscriptMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Role == "assistant" && !terminalTranscriptStatus(message.Status) {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		out = append(out, message)
	}
	return out
}

func terminalTranscriptStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func buildCompactTranscriptSummary(previous string, messages []Message) string {
	var lines []string
	if previous = strings.TrimSpace(previous); previous != "" {
		lines = append(lines, previous)
	}
	for _, message := range messages {
		lines = append(lines, compactTranscriptLine(message))
	}
	return trimRunesFromStart(strings.Join(lines, "\n"), compactSummaryMaxRunes)
}

func compactTranscriptLine(message Message) string {
	role := "User"
	if message.Role == "assistant" {
		role = "Assistant"
	}
	text := compactSingleLine(message.Content)
	if message.Role == "assistant" && message.Status != "" && message.Status != "completed" {
		return fmt.Sprintf("- %s (%s): %s", role, message.Status, text)
	}
	return fmt.Sprintf("- %s: %s", role, text)
}

func compactSingleLine(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return trimRunesFromEnd(value, compactLineMaxRunes)
}

func trimRunesFromEnd(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func trimRunesFromStart(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes <= 3 {
		return string(runes[len(runes)-maxRunes:])
	}
	return "..." + string(runes[len(runes)-maxRunes+3:])
}
