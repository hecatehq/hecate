package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/controlplane"
	"github.com/hecatehq/hecate/internal/providers"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestWorkspaceSkills_ChatFreezesPrivateBodyAndExposesOnlyMetadata(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, ".agents", "skills", "review", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	const private = "private-skill-sentinel: Always inspect tests first."
	if err := os.WriteFile(filename, []byte(private), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{name: "openai", response: &types.ChatResponse{Model: "gpt-4o-mini", CreatedAt: time.Now().UTC(), Choices: []types.ChatChoice{{Message: types.Message{Role: "assistant", Content: "Reviewed."}, FinishReason: "stop"}}}}
	h := newTestAPIHandlerWithSettings(quietLogger(), []providers.Provider{provider}, config.Config{}, controlplane.NewMemoryStore())
	client := newTaskTestClient(t, NewServer(quietLogger(), h))
	payload, _ := json.Marshal(map[string]string{"workspace": root})
	catalog := mustRequestJSON[struct {
		Data []types.WorkspaceSkillInfo `json:"data"`
	}](client, http.MethodPost, "/hecate/v1/workspace-skills", string(payload))
	if len(catalog.Data) != 1 {
		t.Fatalf("catalog: %#v", catalog)
	}
	taskPayload, _ := json.Marshal(CreateTaskRequest{Prompt: "review", WorkingDirectory: root, WorkspaceSkills: []types.WorkspaceSkillSelection{{Path: catalog.Data[0].Path, SHA256: catalog.Data[0].SHA256}}})
	task := mustRequestJSON[TaskResponse](client, http.MethodPost, "/hecate/v1/tasks", string(taskPayload))
	if task.Data.ExecutionKind != "agent_loop" || len(task.Data.WorkspaceSkills) != 1 {
		t.Fatal("omitted execution kind did not normalize to native agent loop")
	}
	payload, _ = json.Marshal(CreateChatSessionRequest{Workspace: root, Provider: "openai", Model: "gpt-4o-mini", WorkspaceSkills: []types.WorkspaceSkillSelection{{Path: catalog.Data[0].Path, SHA256: catalog.Data[0].SHA256}}})
	created := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions", string(payload))
	if len(created.Data.WorkspaceSkills) != 1 {
		t.Fatal("missing frozen metadata")
	}
	if err := os.WriteFile(filename, []byte("changed skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	client.mustRequestStatus(http.StatusBadRequest, http.MethodPost, "/hecate/v1/chat/sessions", string(payload))
	turned := mustRequestJSON[ChatSessionResponse](client, http.MethodPost, "/hecate/v1/chat/sessions/"+created.Data.ID+"/messages", `{"content":"review","tools_enabled":false}`)
	raw, _ := json.Marshal(turned)
	if strings.Contains(string(raw), private) || strings.Contains(string(raw), "changed skill") {
		t.Fatal("private body leaked to API/context")
	}
	request := provider.LastRequest()
	if len(request.Messages) < 2 || len(request.Messages[0].ContentBlocks) != 1 || !strings.Contains(request.Messages[0].ContentBlocks[0].Text, private) {
		t.Fatal("provider did not receive frozen instructions")
	}
	if strings.Contains(request.Messages[0].ContentBlocks[0].Text, "changed skill") {
		t.Fatal("provider saw a later version")
	}
	stored, _, err := h.agentChat.Get(t.Context(), created.Data.ID)
	if err != nil || stored.WorkspaceSkills[0].Body != private {
		t.Fatalf("runtime snapshot missing: %v", err)
	}
	metadata := chat.ContextPacket{}
	appendWorkspaceSkillContext(&metadata, stored.WorkspaceSkills)
	if len(metadata.Items) != 1 || metadata.Items[0].Body != "" {
		t.Fatal("context metadata contains body")
	}
}

func TestWorkspaceSkills_TaskRendererDoesNotExposeBody(t *testing.T) {
	task := types.Task{WorkspaceSkills: []types.WorkspaceSkillSnapshot{{WorkspaceSkillInfo: types.WorkspaceSkillInfo{Path: ".agents/skills/review/SKILL.md", SHA256: "digest"}, Body: "private-task-skill"}}}
	raw, err := json.Marshal(renderTaskItem(task))
	if err != nil || strings.Contains(string(raw), "private-task-skill") || !strings.Contains(string(raw), "workspace_skills") {
		t.Fatalf("rendered task=%s err=%v", raw, err)
	}
}
