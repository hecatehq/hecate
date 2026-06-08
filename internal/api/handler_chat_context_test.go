package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/storage"
	"github.com/hecatehq/hecate/internal/taskstate"
	"github.com/hecatehq/hecate/pkg/types"
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
			assertContextItem(t, tc.packet, "transcript", contextTrustRuntimeState, "chat.transcript")
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
			assertContextItem(t, tc.packet, "workspace_doc", contextTrustWorkspaceGuidance, "README.md")
			assertContextSource(t, tc.packet, chat.ContextSource{
				Kind:   "project_notes",
				Label:  "docs/notes.md",
				Detail: "docs/notes.md",
				Trust:  "project",
			})
			assertContextItem(t, tc.packet, "project_notes", contextTrustWorkspaceGuidance, "docs/notes.md")
			for _, source := range tc.packet.Sources {
				if source.Detail == "private.md" {
					t.Fatalf("disabled project context source was included: %+v", source)
				}
			}
			for _, item := range tc.packet.Items {
				if item.Origin == "private.md" {
					t.Fatalf("disabled project context item was included: %+v", item)
				}
			}
		})
	}
}

func TestChatContextPacketsIncludeEnabledProjectMemory(t *testing.T) {
	ctx := context.Background()
	memoryStore := memory.NewMemoryStore()
	if _, err := memoryStore.Create(ctx, memory.Entry{
		ID:         "mem_operator",
		ProjectID:  "proj_1",
		Title:      "Commit style",
		Body:       "Use conventional commits.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create operator memory: %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{
		ID:         "mem_generated",
		ProjectID:  "proj_1",
		Title:      "Handoff summary",
		Body:       "Generated summary content.",
		TrustLabel: "generated_summary",
		SourceKind: "handoff",
		SourceID:   "art_1",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create generated memory: %v", err)
	}
	if _, err := memoryStore.Create(ctx, memory.Entry{
		ID:        "mem_disabled",
		ProjectID: "proj_1",
		Title:     "Disabled",
		Body:      "Do not include.",
		Enabled:   false,
	}); err != nil {
		t.Fatalf("Create disabled memory: %v", err)
	}
	handler := &Handler{memory: memoryStore}
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
			operator := findContextItemByOrigin(tc.packet, "mem_operator")
			if operator == nil {
				t.Fatalf("operator memory item not found: %+v", tc.packet.Items)
			}
			if operator.TrustLevel != contextTrustOperatorMemory || operator.Body != "Use conventional commits." {
				t.Fatalf("operator memory item = %+v, want operator_memory body snapshot", *operator)
			}
			generated := findContextItemByOrigin(tc.packet, "mem_generated")
			if generated == nil {
				t.Fatalf("generated memory item not found: %+v", tc.packet.Items)
			}
			if generated.TrustLevel != "generated_summary" || generated.Body != "Generated summary content." {
				t.Fatalf("generated memory item = %+v, want generated_summary body snapshot", *generated)
			}
			if findContextItemByOrigin(tc.packet, "mem_disabled") != nil {
				t.Fatalf("disabled memory was included: %+v", tc.packet.Items)
			}
		})
	}
}

func TestChatContextPacketsItemizeVisibleRuntimeMetadata(t *testing.T) {
	session := chat.Session{
		ID:        "chat_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}
	packet := (&Handler{}).hecateTaskContextPacket(context.Background(), session, "ollama", "llama3.1:8b", "system", true)

	assertContextItem(t, packet, "system_prompt", contextTrustSystemInstruction, "task.system_prompt")
	assertContextItem(t, packet, "workspace", contextTrustWorkspaceGuidance, "/tmp/hecate")
	assertContextItem(t, packet, "task_runtime", contextTrustRuntimeState, "hecate.task_runtime")
	for _, item := range packet.Items {
		if !item.Included {
			t.Fatalf("context item %q Included = false, want true", item.Kind)
		}
	}
}

func TestExternalAgentContextPacketNotesPrivatePromptBoundary(t *testing.T) {
	session := chat.Session{
		ID:        "chat_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}
	packet := (&Handler{}).externalAgentContextPacket(context.Background(), session, "Cursor Agent")
	item := findContextItem(packet, "external_agent_session")
	if item == nil {
		t.Fatalf("external_agent_session context item not found: %+v", packet.Items)
	}
	if item.TrustLevel != contextTrustRuntimeState {
		t.Fatalf("external agent item trust level = %q, want %q", item.TrustLevel, contextTrustRuntimeState)
	}
	if !strings.Contains(item.Body, "cannot inspect the external agent's private prompt") {
		t.Fatalf("external agent item body = %q, want private prompt boundary note", item.Body)
	}
}

func TestChatMessageContextEndpointReturnsPacket(t *testing.T) {
	ctx := context.Background()
	chatStore := chat.NewMemoryStore()
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_1", Title: "Context test"}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	packet := chat.ContextPacket{
		Version:       chatContextPacketVersion,
		ExecutionMode: chat.ExecutionModeHecateTask,
		MessageCount:  1,
		Items: []chat.ContextItem{{
			Kind:            "transcript",
			TrustLevel:      contextTrustRuntimeState,
			Origin:          "chat.transcript",
			Title:           "Chat transcript",
			Included:        true,
			InclusionReason: "test",
		}},
	}
	if _, err := chatStore.AppendMessage(ctx, "chat_1", chat.Message{
		ID:      "msg_1",
		Role:    "assistant",
		Content: "done",
		Context: packet,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	handler := &Handler{agentChat: chatStore}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/chat_1/messages/msg_1/context", "")

	if resp.Object != "context_packet" {
		t.Fatalf("Object = %q, want context_packet", resp.Object)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].Kind != "transcript" {
		t.Fatalf("context packet items = %+v, want transcript item", resp.Data.Items)
	}
}

func TestChatMessageContextEndpointReturnsNotFoundWithoutStoredPacket(t *testing.T) {
	ctx := context.Background()
	chatStore := chat.NewMemoryStore()
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_1", Title: "Context test"}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if _, err := chatStore.AppendMessage(ctx, "chat_1", chat.Message{
		ID:      "msg_user",
		Role:    "user",
		Content: "hello",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	handler := &Handler{agentChat: chatStore}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/chat/sessions/chat_1/messages/msg_user/context", "")
}

func TestChatMessageContextEndpointReturnsHistoricalSnapshotAfterProjectSourcesChange(t *testing.T) {
	ctx := context.Background()
	projectStore := newContextPacketProjectStore(t, ctx)
	handler := &Handler{projects: projectStore, agentChat: chat.NewMemoryStore()}
	session := chat.Session{
		ID:        "chat_1",
		ProjectID: "proj_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}
	if _, err := handler.agentChat.Create(ctx, session); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	packet := handler.directModelContextPacket(ctx, session, "ollama", "llama3.1:8b", "")
	if _, err := handler.agentChat.AppendMessage(ctx, session.ID, chat.Message{
		ID:      "msg_1",
		Role:    "assistant",
		Content: "done",
		Context: packet,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := projectStore.Update(ctx, "proj_1", func(project *projects.Project) {
		project.ContextSources[0].Title = "README changed later"
		project.ContextSources[0].Enabled = false
	}); err != nil {
		t.Fatalf("Update project context sources: %v", err)
	}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/chat_1/messages/msg_1/context", "")

	assertRenderedContextItem(t, resp.Data, "workspace_doc", contextTrustWorkspaceGuidance, "README.md")
	for _, item := range resp.Data.Items {
		if item.Title == "README changed later" {
			t.Fatalf("historical context packet was rewritten after project change: %+v", resp.Data.Items)
		}
	}
}

func TestChatMessageContextEndpointReturnsHistoricalSnapshotAfterProjectMemoryChange(t *testing.T) {
	ctx := context.Background()
	memoryStore := memory.NewMemoryStore()
	if _, err := memoryStore.Create(ctx, memory.Entry{
		ID:         "mem_1",
		ProjectID:  "proj_1",
		Title:      "Commit style",
		Body:       "Use conventional commits.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create project memory: %v", err)
	}
	handler := &Handler{memory: memoryStore, agentChat: chat.NewMemoryStore()}
	session := chat.Session{
		ID:        "chat_1",
		ProjectID: "proj_1",
		Workspace: "/tmp/hecate",
		Messages:  []chat.Message{{ID: "u1", Role: "user", Content: "first"}},
	}
	if _, err := handler.agentChat.Create(ctx, session); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	packet := handler.directModelContextPacket(ctx, session, "ollama", "llama3.1:8b", "")
	if _, err := handler.agentChat.AppendMessage(ctx, session.ID, chat.Message{
		ID:      "msg_1",
		Role:    "assistant",
		Content: "done",
		Context: packet,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := memoryStore.Update(ctx, "proj_1", "mem_1", func(entry *memory.Entry) {
		entry.Title = "Changed later"
		entry.Body = "Different text."
		entry.Enabled = false
	}); err != nil {
		t.Fatalf("Update project memory: %v", err)
	}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/chat/sessions/chat_1/messages/msg_1/context", "")

	item := findRenderedContextItemByOrigin(resp.Data, "mem_1")
	if item == nil {
		t.Fatalf("memory context item not found: %+v", resp.Data.Items)
	}
	if item.Title != "Commit style" || item.Body != "Use conventional commits." || item.TrustLevel != contextTrustOperatorMemory {
		t.Fatalf("historical memory item = %+v, want original body snapshot", *item)
	}
}

func TestTaskRunContextEndpointReturnsLinkedChatMessagePacket(t *testing.T) {
	ctx := context.Background()
	chatStore := chat.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", OriginKind: "chat", OriginID: "chat_1", Status: "running"}
	run := types.TaskRun{ID: "run_1", TaskID: "task_1", Number: 1, Status: "running"}
	if _, err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_1", TaskID: "task_1", LatestRunID: "run_1"}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if _, err := chatStore.AppendMessage(ctx, "chat_1", chat.Message{
		ID:      "msg_1",
		Role:    "assistant",
		TaskID:  "task_1",
		RunID:   "run_1",
		Content: "done",
		Context: chat.ContextPacket{
			Version:       chatContextPacketVersion,
			ExecutionMode: chat.ExecutionModeHecateTask,
			MessageCount:  1,
			Items: []chat.ContextItem{{
				Kind:       "task_runtime",
				TrustLevel: contextTrustRuntimeState,
				Origin:     "hecate.task_runtime",
				Title:      "Hecate task runtime",
				Included:   true,
			}},
		},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	handler := &Handler{agentChat: chatStore, taskStore: taskStore}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	resp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/tasks/task_1/runs/run_1/context", "")

	if resp.Object != "context_packet" {
		t.Fatalf("Object = %q, want context_packet", resp.Object)
	}
	if len(resp.Data.Items) != 1 || resp.Data.Items[0].Kind != "task_runtime" {
		t.Fatalf("context packet items = %+v, want task_runtime item", resp.Data.Items)
	}
}

func TestTaskRunContextEndpointFallbackHydratesSQLiteChatMessages(t *testing.T) {
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "hecate.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	chatStore, err := chat.NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore(chat): %v", err)
	}
	taskStore := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", Status: "running"}
	run := types.TaskRun{ID: "run_1", TaskID: "task_1", Number: 1, Status: "running"}
	if _, err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := chatStore.Create(ctx, chat.Session{ID: "chat_1"}); err != nil {
		t.Fatalf("Create chat session: %v", err)
	}
	if _, err := chatStore.AppendMessage(ctx, "chat_1", chat.Message{
		ID:      "msg_1",
		Role:    "assistant",
		TaskID:  "task_1",
		RunID:   "run_1",
		Content: "done",
		Context: chat.ContextPacket{
			Version:       chatContextPacketVersion,
			ExecutionMode: chat.ExecutionModeHecateTask,
			MessageCount:  1,
			Items: []chat.ContextItem{{
				Kind:       "task_runtime",
				TrustLevel: contextTrustRuntimeState,
				Origin:     "hecate.task_runtime",
				Title:      "Hecate task runtime",
				Included:   true,
			}},
		},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	handler := &Handler{agentChat: chatStore, taskStore: taskStore}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	clientHTTP := newAPITestClient(t, server)

	resp := mustRequestJSON[ChatContextPacketResponse](clientHTTP, http.MethodGet, "/hecate/v1/tasks/task_1/runs/run_1/context", "")

	if len(resp.Data.Items) != 1 || resp.Data.Items[0].Kind != "task_runtime" {
		t.Fatalf("context packet items = %+v, want task_runtime item", resp.Data.Items)
	}
}

func TestTaskRunContextEndpointReturnsNotFoundForStandaloneRun(t *testing.T) {
	ctx := context.Background()
	chatStore := chat.NewMemoryStore()
	taskStore := taskstate.NewMemoryStore()
	task := types.Task{ID: "task_1", Status: "running"}
	run := types.TaskRun{ID: "run_1", TaskID: "task_1", Number: 1, Status: "running"}
	if _, err := taskStore.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	handler := &Handler{agentChat: chatStore, taskStore: taskStore}
	server := NewServer(slog.New(slog.NewJSONHandler(io.Discard, nil)), handler)
	client := newAPITestClient(t, server)

	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/tasks/task_1/runs/run_1/context", "")
}

func TestRenderChatContextPacketIncludesItems(t *testing.T) {
	packet := chat.ContextPacket{
		ID:            "ctx_1",
		Version:       chatContextPacketVersion,
		ExecutionMode: chat.ExecutionModeExternalAgent,
		Workspace:     "/tmp/hecate",
		MessageCount:  2,
		Refs: &chat.ContextRefs{
			SessionID: "chat_1",
			MessageID: "msg_1",
			RunID:     "run_1",
		},
		Items: []chat.ContextItem{{
			Section:         contextSectionRuntime,
			Kind:            "external_agent_session",
			TrustLevel:      contextTrustRuntimeState,
			Origin:          "adapter:Cursor Agent",
			Title:           "Cursor Agent ACP session",
			Body:            "Hecate cannot inspect private prompt packing.",
			BodyRef:         "adapter_session",
			Included:        true,
			InclusionReason: "Visible external-agent metadata",
		}},
	}

	rendered := renderChatContextPacket(packet)

	if rendered == nil {
		t.Fatal("renderChatContextPacket returned nil, want packet")
	}
	if rendered.ID != "ctx_1" || rendered.Refs == nil || rendered.Refs.SessionID != "chat_1" || rendered.Refs.RunID != "run_1" {
		t.Fatalf("rendered packet ids/refs = %+v, want ctx_1 with refs", rendered)
	}
	assertRenderedContextItem(t, *rendered, "external_agent_session", contextTrustRuntimeState, "adapter:Cursor Agent")
	if rendered.Items[0].Section != contextSectionRuntime || rendered.Items[0].BodyRef != "adapter_session" || rendered.Items[0].InclusionReason != "Visible external-agent metadata" {
		t.Fatalf("rendered context item missing fields: %+v", rendered.Items[0])
	}
}

func TestNormalizeContextPacketDoesNotMutateInput(t *testing.T) {
	packet := chat.ContextPacket{
		ID: "ctx_1",
		Refs: &chat.ContextRefs{
			SessionID: "chat_1",
		},
		Items: []chat.ContextItem{{
			Kind:       "task_runtime",
			TrustLevel: contextTrustRuntimeState,
			Origin:     "runtime:task",
			Title:      "Task runtime",
			Included:   true,
		}},
	}

	normalized := normalizeContextPacket(packet, chat.ContextRefs{
		SessionID: "chat_1",
		RunID:     "run_1",
	})

	if packet.Refs == nil || packet.Refs.RunID != "" {
		t.Fatalf("input packet refs mutated to %+v, want original run_id to stay empty", packet.Refs)
	}
	if packet.Items[0].Section != "" {
		t.Fatalf("input packet item section mutated to %q, want empty section", packet.Items[0].Section)
	}
	if normalized.Refs == nil || normalized.Refs.RunID != "run_1" {
		t.Fatalf("normalized refs = %+v, want run_1", normalized.Refs)
	}
	if normalized.Items[0].Section != contextSectionRuntime {
		t.Fatalf("normalized item section = %q, want %q", normalized.Items[0].Section, contextSectionRuntime)
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
			want: []string{"project", "system_prompt", "workspace", "workspace_doc", "project_notes", "transcript"},
		},
		{
			name: "hecate task",
			got:  sourceKinds(handler.hecateTaskContextPacket(ctx, session, "ollama", "llama3.1:8b", "system", false)),
			want: []string{"project", "system_prompt", "workspace", "workspace_doc", "project_notes", "transcript", "task_runtime"},
		},
		{
			name: "external agent",
			got:  sourceKinds(handler.externalAgentContextPacket(ctx, session, "Cursor Agent")),
			want: []string{"project", "workspace", "workspace_doc", "project_notes", "transcript", "adapter_session"},
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

func assertContextItem(t *testing.T, packet chat.ContextPacket, kind, trustLevel, origin string) {
	t.Helper()
	item := findContextItem(packet, kind)
	if item == nil {
		t.Fatalf("context item kind %q not found in packet items: %+v", kind, packet.Items)
	}
	if item.TrustLevel != trustLevel {
		t.Fatalf("context item %q trust level = %q, want %q", kind, item.TrustLevel, trustLevel)
	}
	if item.Origin != origin {
		t.Fatalf("context item %q origin = %q, want %q", kind, item.Origin, origin)
	}
}

func assertRenderedContextItem(t *testing.T, packet ChatContextPacketItem, kind, trustLevel, origin string) {
	t.Helper()
	for _, item := range packet.Items {
		if item.Kind == kind {
			if item.TrustLevel != trustLevel {
				t.Fatalf("rendered context item %q trust level = %q, want %q", kind, item.TrustLevel, trustLevel)
			}
			if item.Origin != origin {
				t.Fatalf("rendered context item %q origin = %q, want %q", kind, item.Origin, origin)
			}
			return
		}
	}
	t.Fatalf("rendered context item kind %q not found in packet items: %+v", kind, packet.Items)
}

func findContextItem(packet chat.ContextPacket, kind string) *chat.ContextItem {
	for i := range packet.Items {
		if packet.Items[i].Kind == kind {
			return &packet.Items[i]
		}
	}
	return nil
}

func findContextItemByOrigin(packet chat.ContextPacket, origin string) *chat.ContextItem {
	for idx := range packet.Items {
		if packet.Items[idx].Origin == origin {
			return &packet.Items[idx]
		}
	}
	return nil
}

func findRenderedContextItemByOrigin(packet ChatContextPacketItem, origin string) *ChatContextItem {
	for idx := range packet.Items {
		if packet.Items[idx].Origin == origin {
			return &packet.Items[idx]
		}
	}
	return nil
}

func findRenderedContextItemByKind(packet ChatContextPacketItem, kind string) *ChatContextItem {
	for idx := range packet.Items {
		if packet.Items[idx].Kind == kind {
			return &packet.Items[idx]
		}
	}
	return nil
}

func sourceKinds(packet chat.ContextPacket) []string {
	kinds := make([]string, 0, len(packet.Sources))
	for _, source := range packet.Sources {
		kinds = append(kinds, source.Kind)
	}
	return kinds
}
