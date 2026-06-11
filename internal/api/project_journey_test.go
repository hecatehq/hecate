package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

func TestProjectJourneyAPI_DiscoverStartInspectAndHandoff(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	handler.SetMemoryStore(memory.NewMemoryStore())
	client := newAPITestClient(t, server)
	root := t.TempDir()
	writeProjectJourneyFile(t, root, "AGENTS.md", "# Project guidance\n\nUse small changes.\nSkill: `.hecate/skills/backend/SKILL.md`.\n")
	writeProjectJourneyFile(t, root, ".hecate/skills/backend/SKILL.md", "---\nname: Backend\n---\n# Backend\n")

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":                   "Journey Project",
		"workspace_path":         root,
		"workspace_kind":         "git",
		"default_provider":       "ollama",
		"default_model":          "qwen2.5-coder",
		"default_workspace_mode": "in_place",
		"default_agent_profile":  "prof_backend",
	}))
	projectID := project.Data.ID
	if projectID == "" || len(project.Data.Roots) != 1 {
		t.Fatalf("project = %+v, want generated id and one root", project.Data)
	}

	discoveredSources := mustRequestJSON[ProjectResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/context-sources/discover", `{}`)
	if !projectJourneyHasContextSource(discoveredSources.Data.ContextSources, "AGENTS.md", "workspace_instruction", "agents_md") {
		t.Fatalf("context sources = %+v, want discovered AGENTS.md source", discoveredSources.Data.ContextSources)
	}
	discoveredSkills := mustRequestJSON[ProjectSkillsResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/skills/discover", "")
	if len(discoveredSkills.Data) != 1 || discoveredSkills.Data[0].ID != "backend" || discoveredSkills.Data[0].Status != "available" {
		t.Fatalf("skills = %+v, want available backend skill", discoveredSkills.Data)
	}

	memoryEntry := mustRequestJSONStatus[ProjectMemoryResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory", projectJourneyJSON(t, map[string]any{
		"title":       "Runtime preference",
		"body":        "Prefer focused backend tests before handoff.",
		"source_kind": "operator",
	}))
	if memoryEntry.Data.ID == "" || !memoryEntry.Data.Enabled {
		t.Fatalf("memory = %+v, want enabled project memory entry", memoryEntry.Data)
	}

	profile := mustRequestJSONStatus[AgentProfileResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/agent-profiles", projectJourneyJSON(t, map[string]any{
		"id":                    "prof_backend",
		"name":                  "Backend implementer",
		"surface":               "hecate_task",
		"execution_profile":     "implementation",
		"project_memory_policy": "include",
		"context_source_policy": "include_enabled",
		"skill_ids":             []string{"backend"},
	}))
	if profile.Data.ID != "prof_backend" {
		t.Fatalf("profile = %+v, want prof_backend", profile.Data)
	}

	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "role_backend",
		"name":                  "Backend engineer",
		"instructions":          "Use the backend project skill and write a handoff.",
		"default_driver_kind":   "hecate_task",
		"default_agent_profile": "prof_backend",
		"skill_ids":             []string{"backend"},
	}))
	if role.Data.ID != "role_backend" || len(role.Data.SkillIDs) != 1 || role.Data.SkillIDs[0] != "backend" {
		t.Fatalf("role = %+v, want backend role with skill id", role.Data)
	}

	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_backend",
		"title":         "Build project journey",
		"brief":         "Exercise project setup, context, assignment start, and handoff.",
		"priority":      "high",
		"owner_role_id": "role_backend",
	}))
	if work.Data.ID != "work_backend" {
		t.Fatalf("work = %+v, want work_backend", work.Data)
	}

	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_backend",
		"role_id":     "role_backend",
		"driver_kind": "hecate_task",
	}))
	if assignment.Data.ID != "asgn_backend" || assignment.Data.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment = %+v, want queued backend assignment", assignment.Data)
	}

	started := mustRequestJSON[ProjectWorkAssignmentEnvelope](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/assignments/asgn_backend/start", `{}`)
	if started.Data.TaskID == "" || started.Data.RunID == "" || started.Data.ContextSnapshotID == "" {
		t.Fatalf("started assignment = %+v, want task/run/context links", started.Data)
	}
	task, found, err := handler.taskStore.GetTask(t.Context(), started.Data.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", started.Data.TaskID, found, err)
	}
	if task.ProjectID != projectID || task.WorkspaceSystemPromptPolicy != types.WorkspaceSystemPromptExclude {
		t.Fatalf("task project/prompt policy = %q/%q, want project id and excluded workspace prompt layer", task.ProjectID, task.WorkspaceSystemPromptPolicy)
	}
	for _, want := range []string{"Project memory: Runtime preference", "Prefer focused backend tests before handoff.", "Workspace instruction: AGENTS.md", "Use small changes."} {
		if !strings.Contains(task.SystemPrompt, want) {
			t.Fatalf("task system_prompt = %q, want %q", task.SystemPrompt, want)
		}
	}

	packetResp := mustRequestJSON[ChatContextPacketResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/assignments/asgn_backend/context", "")
	if packetResp.Data.Refs == nil || packetResp.Data.Refs.ProjectID != projectID || packetResp.Data.Refs.AssignmentID != "asgn_backend" {
		t.Fatalf("context refs = %+v, want project assignment refs", packetResp.Data.Refs)
	}
	assertJourneyContextItem(t, packetResp.Data, "prof_backend", contextSectionProfile, true)
	assertJourneyContextItem(t, packetResp.Data, "project_skills", contextSectionSkills, true)
	assertJourneyContextItem(t, packetResp.Data, memoryEntry.Data.ID, contextSectionMemory, true)
	assertJourneyContextItem(t, packetResp.Data, "AGENTS.md", contextSectionSources, true)
	promptItem := findRenderedContextItemByKind(packetResp.Data, "prompt_context")
	if promptItem == nil || !promptItem.Included || !strings.Contains(promptItem.Body, "Included project memory entries: 1") || !strings.Contains(promptItem.Body, "Included workspace instruction sources: 1") {
		t.Fatalf("prompt context item = %+v, want included memory/source summary", promptItem)
	}

	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_review",
		"source_assignment_id":    "asgn_backend",
		"source_run_id":           started.Data.RunID,
		"target_role_id":          "reviewer_qa",
		"title":                   "Review handoff",
		"summary":                 "Backend journey implementation is ready for review.",
		"recommended_next_action": "Create a review assignment and inspect the context packet.",
		"context_refs":            []string{started.Data.ContextSnapshotID},
		"created_by_role_id":      "role_backend",
		"provenance_kind":         "agent_draft",
		"trust_label":             "operator_reviewed",
		"linked_memory_ids":       []string{memoryEntry.Data.ID},
	}))
	if handoff.Data.Status != projectwork.HandoffStatusPending || handoff.Data.SourceAssignmentID != "asgn_backend" || handoff.Data.TargetRoleID != "reviewer_qa" {
		t.Fatalf("handoff = %+v, want pending handoff from backend to reviewer", handoff.Data)
	}

	reviewAssignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_review",
		"role_id":     "reviewer_qa",
		"driver_kind": "hecate_task",
	}))
	if reviewAssignment.Data.ID != "asgn_review" || reviewAssignment.Data.TaskID != "" {
		t.Fatalf("review assignment = %+v, want queued unstarted follow-up", reviewAssignment.Data)
	}
	patchedHandoff := mustRequestJSON[ProjectHandoffEnvelope](client, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/handoffs/handoff_review", projectJourneyJSON(t, map[string]any{
		"target_assignment_id": "asgn_review",
	}))
	if patchedHandoff.Data.TargetAssignmentID != "asgn_review" {
		t.Fatalf("patched handoff = %+v, want target assignment link", patchedHandoff.Data)
	}
	acceptedHandoff := mustRequestJSON[ProjectHandoffEnvelope](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_backend/handoffs/handoff_review/status", `{"status":"accepted"}`)
	if acceptedHandoff.Data.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("accepted handoff = %+v, want accepted", acceptedHandoff.Data)
	}
}

func projectJourneyJSON(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(raw)
}

func writeProjectJourneyFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func projectJourneyHasContextSource(items []ProjectContextSourceResponseItem, path, kind, format string) bool {
	for _, item := range items {
		if item.Path == path && item.Kind == kind && item.Format == format && item.Enabled {
			return true
		}
	}
	return false
}

func assertJourneyContextItem(t *testing.T, packet ChatContextPacketItem, origin, section string, included bool) {
	t.Helper()
	item := findRenderedContextItemByOrigin(packet, origin)
	if item == nil || item.Section != section || item.Included != included {
		t.Fatalf("context item origin=%q = %+v, want section=%q included=%v", origin, item, section, included)
	}
}
