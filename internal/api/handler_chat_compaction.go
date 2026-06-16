package api

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatapp"
	"github.com/hecatehq/hecate/pkg/types"
)

const (
	agentChatSemanticCompactMaxOutputTokens    = 2048
	agentChatSemanticCompactTranscriptMaxRunes = 24000
	agentChatSemanticCompactMessageMaxRunes    = 2400
	agentChatSemanticCompactSummaryMaxRunes    = 8000
)

type compactChatSessionOptions struct {
	RetainMessages   int
	MinMessages      int
	RequireCompacted bool
	Provider         string
	Model            string
	Now              time.Time
}

func (h *Handler) compactChatSession(ctx context.Context, id string, opts compactChatSessionOptions) (chat.Session, error) {
	app := h.chatApplication()
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := app.CompactSessionWithSummary(ctx, chatapp.CompactSessionCommand{
		ID:               id,
		RetainMessages:   opts.RetainMessages,
		MinMessages:      opts.MinMessages,
		HecateOnly:       true,
		RequireCompacted: opts.RequireCompacted,
		Now:              now,
	}, func(ctx context.Context, session chat.Session, selection chat.CompactTranscriptResult) (chat.ContextSummary, error) {
		summary, err := h.semanticCompactTranscript(ctx, session, selection, opts)
		if err == nil {
			return summary, nil
		}
		if h != nil && h.logger != nil {
			h.logger.DebugContext(ctx, "chat.compaction.semantic_fallback", "session_id", session.ID, "error", err)
		}
		return selection.Summary, nil
	})
	if err != nil {
		return chat.Session{}, err
	}
	return result.Session, nil
}

func (h *Handler) semanticCompactTranscript(ctx context.Context, session chat.Session, selection chat.CompactTranscriptResult, opts compactChatSessionOptions) (chat.ContextSummary, error) {
	provider := strings.TrimSpace(opts.Provider)
	if provider == "" {
		provider = strings.TrimSpace(session.Provider)
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = strings.TrimSpace(session.Model)
	}
	if h == nil || h.service == nil {
		return chat.ContextSummary{}, fmt.Errorf("chat service is not configured")
	}
	if model == "" {
		return chat.ContextSummary{}, fmt.Errorf("chat model is not configured")
	}
	prompt := semanticCompactTranscriptPrompt(session.ContextSummary, selection.Messages)
	resp, err := (gatewayAgentLLMClient{service: h.service}).Chat(ctx, types.ChatRequest{
		RequestID:   newChatID("compact"),
		Model:       model,
		MaxTokens:   agentChatSemanticCompactMaxOutputTokens,
		Temperature: 0,
		Scope:       types.RequestScope{ProviderHint: provider},
		Messages: []types.Message{
			{
				Role:    "system",
				Content: "You compact Hecate Chat transcripts for future turns. Return only the requested Markdown summary.",
			},
			{
				Role:    "user",
				Content: prompt,
			},
		},
	})
	if err != nil {
		return chat.ContextSummary{}, err
	}
	content := strings.TrimSpace(chatResponseText(resp))
	if content == "" {
		return chat.ContextSummary{}, fmt.Errorf("semantic compaction returned an empty summary")
	}
	summary := selection.Summary
	summary.Content = trimRunesFromEnd(content, agentChatSemanticCompactSummaryMaxRunes)
	summary.Strategy = chat.ContextSummaryStrategySemantic
	return summary, nil
}

func semanticCompactTranscriptPrompt(previous chat.ContextSummary, messages []chat.Message) string {
	transcript := trimRunesFromStart(serializeSemanticCompactTranscript(messages), agentChatSemanticCompactTranscriptMaxRunes)
	var b strings.Builder
	if content := strings.TrimSpace(previous.Content); content != "" {
		b.WriteString("Update the previous summary with the new transcript. Preserve still-true details, remove stale details, and merge new facts.\n\n")
		b.WriteString("<previous-summary>\n")
		b.WriteString(content)
		b.WriteString("\n</previous-summary>\n\n")
	} else {
		b.WriteString("Create a compact anchored summary from this transcript.\n\n")
	}
	b.WriteString("Return exactly these Markdown sections, in this order:\n")
	b.WriteString("## Goal\n- [single-sentence task summary]\n\n")
	b.WriteString("## Constraints & Preferences\n- [user constraints, preferences, specs, or \"(none)\"]\n\n")
	b.WriteString("## Progress\n### Done\n- [completed work or \"(none)\"]\n\n")
	b.WriteString("### In Progress\n- [current work or \"(none)\"]\n\n")
	b.WriteString("### Blocked\n- [blockers or \"(none)\"]\n\n")
	b.WriteString("## Key Decisions\n- [decision and why, or \"(none)\"]\n\n")
	b.WriteString("## Next Steps\n- [ordered next actions or \"(none)\"]\n\n")
	b.WriteString("## Critical Context\n- [important technical facts, exact errors, open questions, commands, ids, or \"(none)\"]\n\n")
	b.WriteString("## Relevant Files\n- [file or directory path: why it matters, or \"(none)\"]\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Keep every section, even when empty.\n")
	b.WriteString("- Use terse bullets, not prose paragraphs.\n")
	b.WriteString("- Preserve exact file paths, commands, error strings, ids, providers, models, and decisions when known.\n")
	b.WriteString("- Do not mention that a summary or compaction happened.\n\n")
	b.WriteString("<transcript>\n")
	b.WriteString(transcript)
	b.WriteString("\n</transcript>")
	return b.String()
}

func serializeSemanticCompactTranscript(messages []chat.Message) string {
	var lines []string
	for _, message := range messages {
		role := "User"
		if message.Role == "assistant" {
			role = "Assistant"
		}
		status := strings.TrimSpace(message.Status)
		if status != "" && status != "completed" {
			role += " (" + status + ")"
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s]\n%s", role, trimRunesFromEnd(content, agentChatSemanticCompactMessageMaxRunes)))
	}
	return strings.Join(lines, "\n\n")
}

func chatResponseText(resp *types.ChatResponse) string {
	if resp == nil {
		return ""
	}
	for _, choice := range resp.Choices {
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			return text
		}
	}
	return ""
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
