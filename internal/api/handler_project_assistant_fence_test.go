package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/orchestrator"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
)

func TestProjectAssistantMutationProjectIDsPreservesMultiplePortableProjects(t *testing.T) {
	t.Parallel()
	handler := &Handler{agentChat: chat.NewMemoryStore()}
	proposal := projectassistant.Proposal{Actions: []projectassistant.Action{
		{
			Kind:   projectassistant.ActionCreateWorkItem,
			Target: map[string]string{"project_id": "project_a"},
			Patch:  []byte(`{"project_id":"project_a","title":"A"}`),
		},
		{
			Kind:   projectassistant.ActionCreateMemoryCandidate,
			Target: map[string]string{"project_id": "project_b"},
			Patch:  []byte(`{"project_id":"project_b","title":"B","body":"body"}`),
		},
	}}

	projectIDs, err := handler.projectAssistantMutationProjectIDs(t.Context(), proposal)
	if err != nil {
		t.Fatalf("projectAssistantMutationProjectIDs() error: %v", err)
	}
	if want := []string{"project_a", "project_b"}; !slices.Equal(projectIDs, want) {
		t.Fatalf("projectAssistantMutationProjectIDs() = %v, want %v", projectIDs, want)
	}
}

func TestDraftProjectProposalAgentToolUsesProjectFence(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	_, release, err := handler.projectMutationGate.beginDestructive(t.Context(), "project_tool")
	if err != nil {
		t.Fatalf("close project fence: %v", err)
	}
	defer release()
	_, err = handler.DraftProjectProposal(t.Context(), orchestrator.ProjectAssistantDraftInput{
		ProjectID: "project_tool",
		Request:   "Draft work",
	})
	if !errors.Is(err, errProjectMutationClosed) {
		t.Fatalf("DraftProjectProposal() error = %v, want errProjectMutationClosed", err)
	}
}

func TestProjectAssistantGeneratedCreateProjectIDUsesDeleteFenceAndStableRetry(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	const proposalID = "pa_generated_project_fence"
	originalPatch := json.RawMessage(`{"name":"Generated fenced project"}`)
	proposeBody, err := json.Marshal(map[string]any{
		"id": proposalID,
		"actions": []projectassistant.Action{{
			Kind:  projectassistant.ActionCreateProject,
			Patch: originalPatch,
		}},
	})
	if err != nil {
		t.Fatalf("marshal propose request: %v", err)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/propose", bytes.NewReader(proposeBody)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("propose status = %d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var proposed projectAssistantProposalResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &proposed); err != nil {
		t.Fatalf("decode propose response: %v", err)
	}
	var canonicalPatch struct {
		ID string `json:"id"`
	}
	if len(proposed.Data.Actions) != 1 || json.Unmarshal(proposed.Data.Actions[0].Patch, &canonicalPatch) != nil || canonicalPatch.ID == "" {
		t.Fatalf("canonical proposal actions = %+v, want generated project id", proposed.Data.Actions)
	}

	// Exercise direct Apply canonicalization with the original omitted-id
	// action. A destructive project closure must match the generated id before
	// the first durable apply write, and the exact retry must derive the same id.
	direct := proposed.Data
	direct.Actions = []projectassistant.Action{{Kind: projectassistant.ActionCreateProject, Patch: originalPatch}}
	applyBody, err := json.Marshal(map[string]any{"proposal": direct, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply request: %v", err)
	}
	_, releaseDelete, err := handler.projectMutationGate.beginDestructive(t.Context(), canonicalPatch.ID)
	if err != nil {
		t.Fatalf("begin generated project delete fence: %v", err)
	}
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if recorder.Code != http.StatusConflict {
		releaseDelete()
		t.Fatalf("fenced apply status = %d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
	if _, ok, err := handler.projects.Get(t.Context(), canonicalPatch.ID); err != nil || ok {
		releaseDelete()
		t.Fatalf("project existed during delete fence ok=%t err=%v", ok, err)
	}
	releaseDelete()

	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(applyBody)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("retry apply status = %d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	var applied projectAssistantApplyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode retry apply response: %v", err)
	}
	if len(applied.Data.Actions) != 1 || applied.Data.Actions[0].ID != canonicalPatch.ID {
		t.Fatalf("retry apply result = %+v, want stable project id %q", applied.Data, canonicalPatch.ID)
	}
}

func TestProjectAssistantApplyMovesChatAcrossProjectsWithAtomicFence(t *testing.T) {
	t.Parallel()
	handler, server := newProjectAssistantTestHandler()
	for _, project := range []projects.Project{
		{ID: "project_move_source", Name: "Source"},
		{ID: "project_move_target", Name: "Target"},
	} {
		if _, err := handler.projects.Create(t.Context(), project); err != nil {
			t.Fatalf("create project %s: %v", project.ID, err)
		}
	}
	if _, err := handler.agentChat.Create(t.Context(), chat.Session{ID: "chat_cross_project", ProjectID: "project_move_source"}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	proposal := projectassistant.Proposal{
		ID:                   "pa_cross_project_move",
		Title:                "Move chat",
		RequiresConfirmation: true,
		Actions: []projectassistant.Action{{
			Kind:   projectassistant.ActionMoveChatSession,
			Target: map[string]string{"chat_session_id": "chat_cross_project"},
			Patch:  []byte(`{"project_id":"project_move_target"}`),
		}},
	}
	body, err := json.Marshal(map[string]any{"proposal": proposal, "confirm": true})
	if err != nil {
		t.Fatalf("marshal apply request: %v", err)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/hecate/v1/project-assistant/apply", bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply status = %d body=%s, want 200", recorder.Code, recorder.Body.String())
	}
	session, ok, err := handler.agentChat.Get(t.Context(), "chat_cross_project")
	if err != nil || !ok {
		t.Fatalf("get moved chat ok=%t err=%v", ok, err)
	}
	if session.ProjectID != "project_move_target" {
		t.Fatalf("moved chat project_id = %q, want project_move_target", session.ProjectID)
	}
}

func TestProjectAssistantMutationProjectIDIncludesMoveChatSource(t *testing.T) {
	t.Parallel()
	store := chat.NewMemoryStore()
	if _, err := store.Create(t.Context(), chat.Session{ID: "chat_move", ProjectID: "project_a"}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	handler := &Handler{agentChat: store}
	proposal := projectassistant.Proposal{Actions: []projectassistant.Action{{
		Kind:   projectassistant.ActionMoveChatSession,
		Target: map[string]string{"chat_session_id": "chat_move"},
		Patch:  []byte(`{"project_id":"project_b"}`),
	}}}

	projectIDs, err := handler.projectAssistantMutationProjectIDs(t.Context(), proposal)
	if err != nil {
		t.Fatalf("projectAssistantMutationProjectIDs(move across projects) error: %v", err)
	}
	if want := []string{"project_b", "project_a"}; !slices.Equal(projectIDs, want) {
		t.Fatalf("projectAssistantMutationProjectIDs(move across projects) = %v, want %v", projectIDs, want)
	}
	proposal.Actions[0].Patch = []byte(`{"project_id":"project_a"}`)
	projectIDs, err = handler.projectAssistantMutationProjectIDs(t.Context(), proposal)
	if err != nil || !slices.Equal(projectIDs, []string{"project_a"}) {
		t.Fatalf("projectAssistantMutationProjectIDs(move within project) = %v, %v, want [project_a]", projectIDs, err)
	}
}
