package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hecatehq/hecate/internal/agentadapters"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectruntime"
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
	startedRef := assignmentExecutionRefForTest(t, started.Data)
	if startedRef.TaskID == "" || startedRef.RunID == "" || startedRef.ContextSnapshotID == "" {
		t.Fatalf("started assignment execution_ref = %+v, want task/run/context links", startedRef)
	}
	task, found, err := handler.taskStore.GetTask(t.Context(), startedRef.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", startedRef.TaskID, found, err)
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
		"source_run_id":           startedRef.RunID,
		"target_role_id":          "reviewer_qa",
		"title":                   "Review handoff",
		"summary":                 "Backend journey implementation is ready for review.",
		"recommended_next_action": "Create a review assignment and inspect the context packet.",
		"context_refs":            []string{startedRef.ContextSnapshotID},
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
	if reviewAssignment.Data.ID != "asgn_review" || reviewAssignment.Data.ExecutionRef != nil {
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

func TestProjectJourneyAPI_CairnlineReplacementModeCreatesWorkAndStartsWithoutNativeProjectIdentity(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	root := t.TempDir()

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Cairnline Replacement Journey",
		"roots": []map[string]any{{
			"id":     "root_replacement",
			"path":   root,
			"kind":   "git",
			"active": true,
		}},
		"default_provider": "anthropic",
		"default_model":    "claude-sonnet-4",
	}))
	projectID := project.Data.ID
	if projectID == "" || project.Data.ReadBackend != "cairnline" || project.Data.DefaultRootID != "root_replacement" {
		t.Fatalf("project = %+v, want created Cairnline replacement identity with default root", project.Data)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after create, want no native project identity row", ok, err)
	}

	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                  "role_replacement",
		"name":                "Replacement implementer",
		"instructions":        "Use the Cairnline replacement graph.",
		"default_driver_kind": "hecate_task",
		"default_provider":    "anthropic",
		"default_model":       "claude-sonnet-4",
	}))
	if role.Data.ID != "role_replacement" || role.Data.ProjectID != projectID || role.Data.ReadBackend != "cairnline" {
		t.Fatalf("role = %+v, want replacement project role", role.Data)
	}
	reviewer := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                  "role_reviewer",
		"name":                "Replacement reviewer",
		"instructions":        "Review the Cairnline replacement journey.",
		"default_driver_kind": "hecate_task",
		"default_provider":    "anthropic",
		"default_model":       "claude-sonnet-4",
	}))
	if reviewer.Data.ID != "role_reviewer" || reviewer.Data.ProjectID != projectID || reviewer.Data.ReadBackend != "cairnline" {
		t.Fatalf("reviewer role = %+v, want replacement reviewer role", reviewer.Data)
	}
	assertNoNativeProjectWorkRoleForJourney(t, handler, projectID, "role_replacement")
	assertNoNativeProjectWorkRoleForJourney(t, handler, projectID, "role_reviewer")

	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":                "work_replacement",
		"title":             "Start replacement assignment",
		"brief":             "Exercise Cairnline-authoritative create, work, assignment, launch, collaboration, and closeout.",
		"status":            projectwork.WorkItemStatusReady,
		"owner_role_id":     "role_replacement",
		"root_id":           "root_replacement",
		"reviewer_role_ids": []string{"role_reviewer"},
	}))
	if work.Data.ID != "work_replacement" || work.Data.ReadBackend != "cairnline" || work.Data.RootID != "root_replacement" {
		t.Fatalf("work = %+v, want Cairnline replacement work item", work.Data)
	}
	assertNoNativeProjectWorkItemForJourney(t, handler, projectID, "work_replacement")

	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_replacement",
		"role_id":     "role_replacement",
		"root_id":     "root_replacement",
		"driver_kind": projectwork.AssignmentDriverHecateTask,
	}))
	if assignment.Data.ID != "asgn_replacement" || assignment.Data.ReadBackend != "cairnline" || assignment.Data.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment = %+v, want queued Cairnline replacement assignment", assignment.Data)
	}
	assertNoNativeProjectWorkAssignmentForJourney(t, handler, projectID, "work_replacement", "asgn_replacement")

	started := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/assignments/asgn_replacement/start", `{}`)
	ref := assignmentExecutionRefForTest(t, started.Data)
	if ref.TaskID == "" || ref.RunID == "" || ref.ContextSnapshotID == "" {
		t.Fatalf("started assignment execution_ref = %+v, want task/run/context links", ref)
	}
	task, found, err := handler.taskStore.GetTask(t.Context(), ref.TaskID)
	if err != nil || !found {
		t.Fatalf("GetTask(%q) found=%v err=%v, want task", ref.TaskID, found, err)
	}
	if task.ProjectID != projectID || task.WorkItemID != "work_replacement" || task.AssignmentID != "asgn_replacement" {
		t.Fatalf("task linkage = project %q work %q assignment %q, want replacement refs", task.ProjectID, task.WorkItemID, task.AssignmentID)
	}
	if task.RequestedProvider != "anthropic" || task.RequestedModel != "claude-sonnet-4" || task.WorkingDirectory != root {
		t.Fatalf("task provider/model/workspace = %q/%q/%q, want anthropic/claude-sonnet-4/%q", task.RequestedProvider, task.RequestedModel, task.WorkingDirectory, root)
	}
	if !strings.Contains(task.Prompt, "Start replacement assignment") || !strings.Contains(task.SystemPrompt, "Use the Cairnline replacement graph.") {
		t.Fatalf("task prompt/system = %q / %q, want replacement work and role context", task.Prompt, task.SystemPrompt)
	}

	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after start, want no native project identity row", ok, err)
	}
	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_replacement")
	if mirrored.ExecutionRef != ref.RunID || mirrored.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("mirrored Cairnline assignment = ref %q context %q, want %q/%q", mirrored.ExecutionRef, mirrored.ContextSnapshotID, ref.RunID, ref.ContextSnapshotID)
	}
	runtime, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_replacement")
	if err != nil || !ok {
		t.Fatalf("Hecate runtime overlay ok=%v err=%v after replacement start, want runtime refs", ok, err)
	}
	if runtime.ExecutionRef.RunID != ref.RunID || runtime.ExecutionRef.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("Hecate runtime overlay = %+v, want run/context %q/%q", runtime.ExecutionRef, ref.RunID, ref.ContextSnapshotID)
	}
	assertNoNativeProjectWorkAssignmentForJourney(t, handler, projectID, "work_replacement", "asgn_replacement")

	completed := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/assignments/asgn_replacement", projectJourneyJSON(t, map[string]any{
		"status": projectwork.AssignmentStatusCompleted,
	}))
	if completed.Data.Status != projectwork.AssignmentStatusCompleted || completed.Data.ReadBackend != "cairnline" {
		t.Fatalf("completed assignment = %+v, want Cairnline-backed completed assignment", completed.Data)
	}

	evidence := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                   "artifact_replacement_evidence",
		"assignment_id":        "asgn_replacement",
		"kind":                 projectwork.ArtifactKindEvidenceLink,
		"title":                "Replacement evidence",
		"body":                 "Task and context links were created from a Cairnline-only project graph.",
		"author_role_id":       "role_replacement",
		"evidence_url":         "https://example.invalid/hecate/cairnline-replacement",
		"evidence_provider":    "operator",
		"evidence_trust_label": projectwork.EvidenceTrustOperatorProvided,
	}))
	if evidence.Data.Kind != projectwork.ArtifactKindEvidenceLink || evidence.Data.AssignmentID != "asgn_replacement" || evidence.Data.EvidenceURL != "https://example.invalid/hecate/cairnline-replacement" {
		t.Fatalf("evidence = %+v, want assignment evidence response", evidence.Data)
	}
	mirroredEvidence := getMirroredCairnlineEvidenceForTest(t, handler, projectID, "work_replacement", "artifact_replacement_evidence")
	if mirroredEvidence.Locator != "https://example.invalid/hecate/cairnline-replacement" || mirroredEvidence.Provider != "operator" {
		t.Fatalf("mirrored evidence = %+v, want Cairnline-backed assignment evidence", mirroredEvidence)
	}

	review := mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                        "artifact_replacement_review",
		"assignment_id":             "asgn_replacement",
		"reviewed_assignment_id":    "asgn_replacement",
		"kind":                      projectwork.ArtifactKindReview,
		"title":                     "Replacement review",
		"body":                      "Approved replacement journey evidence.",
		"author_role_id":            "role_reviewer",
		"review_verdict":            projectwork.ReviewVerdictApproved,
		"review_risk":               projectwork.ReviewRiskLow,
		"review_follow_up_required": false,
	}))
	if review.Data.ReviewVerdict != projectwork.ReviewVerdictApproved || review.Data.ReviewFollowUpRequired {
		t.Fatalf("review = %+v, want approved review response without follow-up", review.Data)
	}
	mirroredReview := getMirroredCairnlineReviewForTest(t, handler, projectID, "work_replacement", "artifact_replacement_review")
	if mirroredReview.Verdict != projectwork.ReviewVerdictApproved || mirroredReview.Risk != projectwork.ReviewRiskLow {
		t.Fatalf("mirrored review = %+v, want approved Cairnline-backed review without follow-up", mirroredReview)
	}

	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_replacement_review",
		"source_assignment_id":    "asgn_replacement",
		"source_run_id":           ref.RunID,
		"target_role_id":          "role_reviewer",
		"title":                   "Replacement review handoff",
		"summary":                 "Cairnline replacement journey evidence is ready.",
		"recommended_next_action": "Review the recorded evidence and close the work item.",
		"context_refs":            []string{ref.ContextSnapshotID},
		"linked_artifact_ids":     []string{"artifact_replacement_evidence", "artifact_replacement_review"},
		"created_by_role_id":      "role_replacement",
		"provenance_kind":         "agent_draft",
		"trust_label":             "operator_reviewed",
	}))
	if handoff.Data.Status != projectwork.HandoffStatusPending || handoff.Data.TargetRoleID != "role_reviewer" {
		t.Fatalf("handoff = %+v, want pending review handoff response", handoff.Data)
	}
	mirroredHandoff := getMirroredCairnlineHandoffForTest(t, handler, projectID, "work_replacement", "handoff_replacement_review")
	if mirroredHandoff.ToRoleID != "role_reviewer" || mirroredHandoff.Status != "open" {
		t.Fatalf("mirrored handoff = %+v, want pending Cairnline-backed review handoff", mirroredHandoff)
	}
	accepted := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/handoffs/handoff_replacement_review/status", `{"status":"accepted"}`)
	if accepted.Data.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("accepted handoff = %+v, want accepted handoff response", accepted.Data)
	}
	mirroredHandoff = getMirroredCairnlineHandoffForTest(t, handler, projectID, "work_replacement", "handoff_replacement_review")
	if mirroredHandoff.Status != projectwork.HandoffStatusAccepted {
		t.Fatalf("mirrored accepted handoff = %+v, want accepted Cairnline-backed handoff", mirroredHandoff)
	}

	readiness := mustRequestJSONStatus[ProjectWorkItemReadinessEnvelope](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement/readiness", "")
	if !readiness.Data.Ready || readiness.Data.Status != "ready" || readiness.Data.ReadBackend != "cairnline" || readiness.Data.CompletedAssignments != 1 {
		t.Fatalf("readiness = %+v, want Cairnline-backed ready closeout", readiness.Data)
	}
	closed := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement", projectJourneyJSON(t, map[string]any{
		"status": projectwork.WorkItemStatusDone,
	}))
	if closed.Data.Status != projectwork.WorkItemStatusDone || closed.Data.ReadBackend != "cairnline" {
		t.Fatalf("closed work item = %+v, want Cairnline-backed done status", closed.Data)
	}
	if _, ok, err := handler.projects.Get(t.Context(), projectID); err != nil || ok {
		t.Fatalf("Hecate project store ok=%v err=%v after closeout, want no native project identity row", ok, err)
	}
	if mirroredWork := getMirroredCairnlineWorkItemForTest(t, handler, projectID, "work_replacement"); mirroredWork.Status != projectwork.WorkItemStatusDone {
		t.Fatalf("mirrored Cairnline work = %+v, want done", mirroredWork)
	}
}

func TestProjectJourneyAPI_CairnlineReplacementModeStartsExternalAgentWithoutAdvancingNativeShadow(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	runner := &fakeAgentChatRunner{nativeSessionID: "native_replacement_external"}
	handler.SetAgentChatRunner(runner)
	client := newAPITestClient(t, server)
	root := t.TempDir()

	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                "prof_replacement_external",
		Name:              "Replacement external agent",
		Surface:           agentprofiles.SurfaceExternalAgent,
		ExecutionProfile:  "external_implementation",
		ExternalAgentKind: "codex",
	}); err != nil {
		t.Fatalf("Create external profile: %v", err)
	}

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Cairnline Replacement External Journey",
		"roots": []map[string]any{{
			"id":     "root_replacement_external",
			"path":   root,
			"kind":   "git",
			"active": true,
		}},
	}))
	projectID := project.Data.ID
	if projectID == "" || project.Data.ReadBackend != "cairnline" {
		t.Fatalf("project = %+v, want Cairnline replacement identity", project.Data)
	}

	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "role_replacement_external",
		"name":                  "Replacement external implementer",
		"instructions":          "Prepare the external adapter session from Cairnline state.",
		"default_driver_kind":   projectwork.AssignmentDriverExternalAgent,
		"default_agent_profile": "prof_replacement_external",
	}))
	if role.Data.ID != "role_replacement_external" || role.Data.ReadBackend != "cairnline" {
		t.Fatalf("role = %+v, want Cairnline-backed external role", role.Data)
	}

	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_replacement_external",
		"title":         "Prepare replacement external assignment",
		"brief":         "Exercise replacement-mode External Agent launch with the compatibility store present.",
		"status":        projectwork.WorkItemStatusReady,
		"owner_role_id": "role_replacement_external",
		"root_id":       "root_replacement_external",
	}))
	if work.Data.ID != "work_replacement_external" || work.Data.ReadBackend != "cairnline" {
		t.Fatalf("work = %+v, want Cairnline-backed external work item", work.Data)
	}

	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement_external/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_replacement_external",
		"role_id":     "role_replacement_external",
		"root_id":     "root_replacement_external",
		"driver_kind": projectwork.AssignmentDriverExternalAgent,
	}))
	if assignment.Data.ID != "asgn_replacement_external" || assignment.Data.ReadBackend != "cairnline" || assignment.Data.Status != projectwork.AssignmentStatusQueued {
		t.Fatalf("assignment = %+v, want queued Cairnline-backed external assignment", assignment.Data)
	}

	started := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusOK, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_replacement_external/assignments/asgn_replacement_external/start", `{"driver_kind":"external_agent"}`)
	ref := assignmentExecutionRefForTest(t, started.Data)
	if ref.ChatSessionID == "" || ref.ContextSnapshotID == "" || ref.TaskID != "" || ref.RunID != "" {
		t.Fatalf("started assignment execution_ref = %+v, want chat/context links only", ref)
	}
	if started.Data.Status != projectwork.AssignmentStatusRunning || started.Data.ReadBackend != "cairnline" {
		t.Fatalf("started assignment = %+v, want running Cairnline-backed external assignment", started.Data)
	}
	resolvedWorkspace, err := agentadapters.ValidateWorkspace(root)
	if err != nil {
		t.Fatalf("ValidateWorkspace: %v", err)
	}
	if len(runner.prepareRequests) != 1 || runner.prepareRequests[0].AdapterID != "codex" || runner.prepareRequests[0].SessionID != ref.ChatSessionID || runner.prepareRequests[0].Workspace != resolvedWorkspace {
		t.Fatalf("prepare requests = %+v, want one codex session in workspace %q", runner.prepareRequests, resolvedWorkspace)
	}
	if len(runner.runRequests) != 0 {
		t.Fatalf("run requests = %+v, want no automatic external-agent turn", runner.runRequests)
	}

	mirrored := getMirroredCairnlineAssignmentForTest(t, handler, projectID, "asgn_replacement_external")
	if mirrored.ExecutionRef != ref.ChatSessionID || mirrored.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("mirrored Cairnline assignment = ref %q context %q, want %q/%q", mirrored.ExecutionRef, mirrored.ContextSnapshotID, ref.ChatSessionID, ref.ContextSnapshotID)
	}
	runtime, ok, err := handler.projectRuntime.Get(t.Context(), projectID, "asgn_replacement_external")
	if err != nil || !ok {
		t.Fatalf("Hecate runtime overlay ok=%v err=%v after replacement external start, want runtime refs", ok, err)
	}
	if runtime.ExecutionRef.ChatSessionID != ref.ChatSessionID || runtime.ExecutionRef.ContextSnapshotID != ref.ContextSnapshotID {
		t.Fatalf("Hecate runtime overlay = %+v, want chat/context %q/%q", runtime.ExecutionRef, ref.ChatSessionID, ref.ContextSnapshotID)
	}
	assertNoNativeProjectWorkAssignmentForJourney(t, handler, projectID, "work_replacement_external", "asgn_replacement_external")
}

func TestProjectJourneyAPI_CairnlineReplacementCloseoutReadinessUsesRuntimeOverlay(t *testing.T) {
	t.Parallel()
	handler, server := newProjectsCairnlineReplacementIdentityAuthorityTestServer(t)
	client := newAPITestClient(t, server)
	root := t.TempDir()

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Cairnline Runtime Overlay Closeout",
		"roots": []map[string]any{{
			"id":     "root_overlay",
			"path":   root,
			"kind":   "git",
			"active": true,
		}},
	}))
	projectID := project.Data.ID
	mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                  "role_overlay",
		"name":                "Runtime overlay implementer",
		"default_driver_kind": projectwork.AssignmentDriverHecateTask,
	}))
	mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_overlay",
		"title":         "Close with runtime overlay",
		"status":        projectwork.WorkItemStatusReady,
		"owner_role_id": "role_overlay",
		"root_id":       "root_overlay",
	}))
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_overlay/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_overlay",
		"role_id":     "role_overlay",
		"root_id":     "root_overlay",
		"driver_kind": projectwork.AssignmentDriverHecateTask,
	}))
	if assignment.Data.Status != projectwork.AssignmentStatusQueued || assignment.Data.ReadBackend != "cairnline" {
		t.Fatalf("assignment = %+v, want queued Cairnline-backed assignment", assignment.Data)
	}
	if _, err := handler.projectRuntime.Upsert(t.Context(), projectruntime.AssignmentRuntime{
		ProjectID:    projectID,
		AssignmentID: "asgn_overlay",
		ExecutionRef: projectwork.AssignmentExecutionRef{
			Kind:              "task_run",
			TaskID:            "task_overlay",
			RunID:             "run_overlay",
			ContextSnapshotID: "ctx_overlay",
			Status:            projectwork.AssignmentStatusCompleted,
		},
	}); err != nil {
		t.Fatalf("Upsert runtime overlay: %v", err)
	}
	mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/work_overlay/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                   "artifact_overlay_evidence",
		"assignment_id":        "asgn_overlay",
		"kind":                 projectwork.ArtifactKindEvidenceLink,
		"title":                "Runtime overlay evidence",
		"body":                 "The Hecate runtime overlay completed while Cairnline still held the portable queued assignment.",
		"author_role_id":       "role_overlay",
		"evidence_url":         "hecate://tasks/task_overlay/runs/run_overlay",
		"evidence_provider":    "hecate",
		"evidence_trust_label": projectwork.EvidenceTrustOperatorProvided,
	}))

	readiness := mustRequestJSONStatus[ProjectWorkItemReadinessEnvelope](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/"+projectID+"/work-items/work_overlay/readiness", "")
	if !readiness.Data.Ready || readiness.Data.Status != "ready" || readiness.Data.ReadBackend != "cairnline" || readiness.Data.CompletedAssignments != 1 {
		t.Fatalf("readiness = %+v, want runtime-overlay completed Cairnline closeout", readiness.Data)
	}
	closed := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusOK, http.MethodPatch, "/hecate/v1/projects/"+projectID+"/work-items/work_overlay", projectJourneyJSON(t, map[string]any{
		"status": projectwork.WorkItemStatusDone,
	}))
	if closed.Data.Status != projectwork.WorkItemStatusDone || closed.Data.ReadBackend != "cairnline" {
		t.Fatalf("closed work item = %+v, want Cairnline-backed closeout", closed.Data)
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

func assertNoNativeProjectWorkRoleForJourney(t *testing.T, handler *Handler, projectID, roleID string) {
	t.Helper()
	roles, err := handler.projectWork.ListRoles(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListRoles(%q): %v", projectID, err)
	}
	for _, role := range roles {
		if role.ID == roleID && !role.BuiltIn {
			t.Fatalf("native project-work role %q exists in replacement mode: %+v", roleID, role)
		}
	}
}

func assertNoNativeProjectWorkItemForJourney(t *testing.T, handler *Handler, projectID, workItemID string) {
	t.Helper()
	item, ok, err := handler.projectWork.GetWorkItem(t.Context(), projectID, workItemID)
	if err != nil {
		t.Fatalf("GetWorkItem(%q, %q): %v", projectID, workItemID, err)
	}
	if ok {
		t.Fatalf("native project-work item %q exists in replacement mode: %+v", workItemID, item)
	}
}

func assertNoNativeProjectWorkAssignmentForJourney(t *testing.T, handler *Handler, projectID, workItemID, assignmentID string) {
	t.Helper()
	assignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{
		ProjectID:  projectID,
		WorkItemID: workItemID,
	})
	if err != nil {
		t.Fatalf("ListAssignments(%q, %q): %v", projectID, workItemID, err)
	}
	for _, assignment := range assignments {
		if assignment.ID == assignmentID {
			t.Fatalf("native project-work assignment %q exists in replacement mode: %+v", assignmentID, assignment)
		}
	}
}

func assertJourneyContextItem(t *testing.T, packet ChatContextPacketItem, origin, section string, included bool) {
	t.Helper()
	item := findRenderedContextItemByOrigin(packet, origin)
	if item == nil || item.Section != section || item.Included != included {
		t.Fatalf("context item origin=%q = %+v, want section=%q included=%v", origin, item, section, included)
	}
}
