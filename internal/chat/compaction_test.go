package chat

import (
	"strings"
	"testing"
	"time"
)

func TestCompactTranscriptSummary_SummarizesOlderVisibleMessages(t *testing.T) {
	session := Session{Messages: []Message{
		{ID: "m1", Role: "user", Content: "first request"},
		{ID: "m2", Role: "assistant", Status: "completed", Content: "first answer"},
		{ID: "m3", Role: "assistant", Status: "running", Content: "still running"},
		{ID: "m4", Role: "user", Content: "second request"},
		{ID: "m5", Role: "assistant", Status: "completed", Content: "second answer"},
		{ID: "m6", Role: "user", Content: "latest request"},
	}}
	now := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)

	result := CompactTranscriptSummary(session, CompactTranscriptOptions{
		Now:            now,
		RetainMessages: 2,
		MinMessages:    3,
	})

	if !result.Compacted {
		t.Fatal("Compacted = false, want true")
	}
	if result.Summary.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", result.Summary.MessageCount)
	}
	if result.Summary.ThroughMessageID != "m4" {
		t.Fatalf("ThroughMessageID = %q, want m4", result.Summary.ThroughMessageID)
	}
	if !result.Summary.CompactedAt.Equal(now) {
		t.Fatalf("CompactedAt = %s, want %s", result.Summary.CompactedAt, now)
	}
	if result.Summary.Strategy != ContextSummaryStrategyDeterministic {
		t.Fatalf("Strategy = %q, want %q", result.Summary.Strategy, ContextSummaryStrategyDeterministic)
	}
	if len(result.Messages) != 3 || result.Messages[0].ID != "m1" || result.Messages[2].ID != "m4" {
		t.Fatalf("Messages = %+v, want compacted transcript selection m1..m4 without running assistant", result.Messages)
	}
	if strings.Contains(result.Summary.Content, "still running") {
		t.Fatalf("summary includes running assistant message: %q", result.Summary.Content)
	}
	for _, want := range []string{"- User: first request", "- Assistant: first answer", "- User: second request"} {
		if !strings.Contains(result.Summary.Content, want) {
			t.Fatalf("summary missing %q: %q", want, result.Summary.Content)
		}
	}
}

func TestCompactTranscriptSummary_ExtendsExistingSummaryOnce(t *testing.T) {
	session := Session{
		ContextSummary: ContextSummary{
			Content:          "- User: first request",
			MessageCount:     1,
			ThroughMessageID: "m1",
			CompactedAt:      time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC),
		},
		Messages: []Message{
			{ID: "m1", Role: "user", Content: "first request"},
			{ID: "m2", Role: "assistant", Status: "completed", Content: "first answer"},
			{ID: "m3", Role: "user", Content: "second request"},
			{ID: "m4", Role: "assistant", Status: "completed", Content: "second answer"},
		},
	}

	result := CompactTranscriptSummary(session, CompactTranscriptOptions{
		RetainMessages: 1,
		MinMessages:    2,
	})

	if !result.Compacted {
		t.Fatal("Compacted = false, want true")
	}
	if result.Summary.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", result.Summary.MessageCount)
	}
	if got := strings.Count(result.Summary.Content, "first request"); got != 1 {
		t.Fatalf("first request appears %d times, want once: %q", got, result.Summary.Content)
	}
	if !strings.Contains(result.Summary.Content, "- Assistant: first answer") ||
		!strings.Contains(result.Summary.Content, "- User: second request") {
		t.Fatalf("summary did not append new compacted messages: %q", result.Summary.Content)
	}
}

func TestTranscriptSummaryPrompt(t *testing.T) {
	prompt := TranscriptSummaryPrompt(ContextSummary{
		Content:          "- User: first request",
		MessageCount:     1,
		ThroughMessageID: "m1",
	})

	if !strings.Contains(prompt, "Earlier chat transcript compacted by Hecate") ||
		!strings.Contains(prompt, "Use this as background") {
		t.Fatalf("prompt missing guidance: %q", prompt)
	}
}
