package api

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/projects"
)

func TestChatContextPacketsUseVisibleTranscriptCount(t *testing.T) {
	session := chat.Session{
		ID:        "chat_1",
		Workspace: "/tmp/hecate",
		Messages: []chat.Message{
			{ID: "sys", Role: "system", Content: "hidden"},
			{ID: "u1", Role: "user", Content: "first"},
			{ID: "a1", Role: "assistant", Content: "done", Status: "completed"},
			{ID: "a2", Role: "assistant", Content: "still running", Status: "running"},
			{ID: "empty", Role: "user", Content: "   "},
		},
	}

	packets := []struct {
		name   string
		packet chat.ContextPacket
	}{
		{
			name:   "direct model",
			packet: (&Handler{}).directModelContextPacket(context.Background(), session, "ollama", "llama3.1:8b", "system"),
		},
		{
			name:   "hecate task",
			packet: (&Handler{}).hecateTaskContextPacket(context.Background(), session, "ollama", "llama3.1:8b", "system", false),
		},
		{
			name:   "external agent",
			packet: (&Handler{}).externalAgentContextPacket(context.Background(), session, "Cursor Agent"),
		},
	}

	for _, tc := range packets {
		t.Run(tc.name, func(t *testing.T) {
			if tc.packet.MessageCount != 3 {
				t.Fatalf("MessageCount = %d, want 3 visible terminal messages including current user turn", tc.packet.MessageCount)
			}
		})
	}
}

func TestChatContextPacketsIncludeEnabledProjectSources(t *testing.T) {
	ctx := context.Background()
	projectStore := newContextPacketProjectStore(t, ctx)
	handler := &Handler{projects: projectStore}
	session := chat.Session{
		ID:        "chat_1",
		ProjectID: "proj_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}

	packets := []struct {
		name   string
		packet chat.ContextPacket
	}{
		{
			name:   "direct model",
			packet: handler.directModelContextPacket(ctx, session, "ollama", "llama3.1:8b", ""),
		},
		{
			name:   "hecate task",
			packet: handler.hecateTaskContextPacket(ctx, session, "ollama", "llama3.1:8b", "", false),
		},
		{
			name:   "external agent",
			packet: handler.externalAgentContextPacket(ctx, session, "Cursor Agent"),
		},
	}

	for _, tc := range packets {
		t.Run(tc.name, func(t *testing.T) {
			assertContextSource(t, tc.packet, chat.ContextSource{
				Kind:   "workspace_doc",
				Label:  "README",
				Detail: "README.md",
				Trust:  "project",
			})
			assertContextSource(t, tc.packet, chat.ContextSource{
				Kind:   "project_notes",
				Label:  "docs/notes.md",
				Detail: "docs/notes.md",
				Trust:  "project",
			})
			for _, source := range tc.packet.Sources {
				if source.Detail == "private.md" {
					t.Fatalf("disabled project context source was included: %+v", source)
				}
			}
		})
	}
}

func TestChatContextPacketSourceOrdering(t *testing.T) {
	ctx := context.Background()
	handler := &Handler{projects: newContextPacketProjectStore(t, ctx)}
	session := chat.Session{
		ID:        "chat_1",
		ProjectID: "proj_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		{
			name: "direct model",
			got:  sourceKinds(handler.directModelContextPacket(ctx, session, "ollama", "llama3.1:8b", "system")),
			want: []string{"system_prompt", "workspace_doc", "project_notes", "transcript"},
		},
		{
			name: "hecate task",
			got:  sourceKinds(handler.hecateTaskContextPacket(ctx, session, "ollama", "llama3.1:8b", "system", false)),
			want: []string{"system_prompt", "workspace", "workspace_doc", "project_notes", "transcript", "task_runtime"},
		},
		{
			name: "external agent",
			got:  sourceKinds(handler.externalAgentContextPacket(ctx, session, "Cursor Agent")),
			want: []string{"workspace", "workspace_doc", "project_notes", "transcript", "adapter_session"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(tc.got, tc.want) {
				t.Fatalf("source kinds = %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func newContextPacketProjectStore(t *testing.T, ctx context.Context) projects.Store {
	t.Helper()
	projectStore := projects.NewMemoryStore()
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	if _, err := projectStore.Create(ctx, projects.Project{
		ID:        "proj_1",
		Name:      "Hecate",
		CreatedAt: now,
		UpdatedAt: now,
		ContextSources: []projects.ContextSource{
			{
				ID:        "ctxsrc_readme",
				Kind:      "doc",
				Title:     "README",
				Path:      "README.md",
				Enabled:   true,
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "ctxsrc_notes",
				Kind:      "notes",
				Path:      "docs/notes.md",
				Enabled:   true,
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				ID:        "ctxsrc_disabled",
				Kind:      "doc",
				Title:     "Disabled",
				Path:      "private.md",
				Enabled:   false,
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	return projectStore
}

func assertContextSource(t *testing.T, packet chat.ContextPacket, want chat.ContextSource) {
	t.Helper()
	for _, got := range packet.Sources {
		if got == want {
			return
		}
	}
	t.Fatalf("context source %+v not found in packet sources: %+v", want, packet.Sources)
}

func sourceKinds(packet chat.ContextPacket) []string {
	kinds := make([]string, 0, len(packet.Sources))
	for _, source := range packet.Sources {
		kinds = append(kinds, source.Kind)
	}
	return kinds
}
