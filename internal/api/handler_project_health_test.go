package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectHealth_ReadOnlyAttention(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:    "proj_health",
		Name:  "Health",
		Roots: []projects.Root{{ID: "root_health", Path: t.TempDir(), Kind: "git", Active: true}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_review",
		ProjectID: "proj_health",
		Title:     "Review server attention",
		Status:    projectwork.WorkItemStatusReview,
		Priority:  "high",
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_review): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_review",
		ProjectID:  "proj_health",
		WorkItemID: "work_review",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusRunning,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_review): %v", err)
	}
	if _, err := handler.projectWork.CreateHandoff(t.Context(), projectwork.Handoff{
		ID:                    "handoff_review",
		ProjectID:             "proj_health",
		WorkItemID:            "work_review",
		SourceAssignmentID:    "asgn_review",
		TargetRoleID:          "reviewer_qa",
		Title:                 "QA follow-up",
		Summary:               "Review needs an operator path.",
		RecommendedNextAction: "Create a follow-up assignment.",
		Status:                projectwork.HandoffStatusPending,
	}); err != nil {
		t.Fatalf("CreateHandoff: %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "art_review",
		ProjectID:              "proj_health",
		WorkItemID:             "work_review",
		AssignmentID:           "asgn_review",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "QA review",
		Body:                   "Needs a follow-up path.",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:             projectwork.ReviewRiskMedium,
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:                  "memcand_health",
		ProjectID:           "proj_health",
		Title:               "Remember the review boundary",
		Body:                "Attention derivation stays read-only.",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		Status:              memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	beforeAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_health"})
	if err != nil {
		t.Fatalf("ListAssignments before: %v", err)
	}
	beforeCandidates, err := handler.memoryCandidates.ListCandidates(t.Context(), memory.CandidateFilter{ProjectID: "proj_health"})
	if err != nil {
		t.Fatalf("ListCandidates before: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_health/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHealthEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.Object != "project_health" || response.Data.ProjectID != "proj_health" {
		t.Fatalf("health envelope = %+v, want project_health for project", response)
	}
	if response.Data.ReadBackend != "hecate" {
		t.Fatalf("health read_backend = %q, want hecate", response.Data.ReadBackend)
	}
	assertProjectHealthItemsHaveActions(t, response.Data.Attention, "proj_health")
	if response.Data.Summary.AttentionLimit != projectHealthAttentionLimit || response.Data.Summary.AttentionCount != 5 || response.Data.Summary.OmittedAttentionCount != 0 {
		t.Fatalf("health summary = %+v, want bounded attention counts", response.Data.Summary)
	}
	if response.Data.Summary.PendingMemoryCandidateCount != 1 || response.Data.Summary.PendingHandoffCount != 1 || response.Data.Summary.ReviewFollowUpCount != 1 {
		t.Fatalf("health summary = %+v, want candidate/handoff/review counts", response.Data.Summary)
	}
	defaults := findProjectHealthAttentionForTest(t, response.Data.Attention, "Provider/model defaults missing")
	assertProjectHealthActionForTest(t, defaults, projectActionOpenProjectSettings, "proj_health")
	if defaults.Status != "awaiting_approval" {
		t.Fatalf("defaults attention = %+v, want awaiting approval status", defaults)
	}
	handoff := findProjectHealthAttentionForTest(t, response.Data.Attention, "Pending handoff: Review server attention")
	if handoff.WorkItemID != "work_review" || handoff.Bucket != "recent" || handoff.ActionLabel != "View recent" {
		t.Fatalf("handoff attention = %+v, want recent work target", handoff)
	}
	assertProjectHealthActionForTest(t, handoff, projectActionOpenWorkItem, "proj_health")
	if handoff.Action.WorkItemID != "work_review" || handoff.Action.AssignmentID != "asgn_review" || handoff.Action.ActivityBucket != "recent" {
		t.Fatalf("handoff action = %+v, want recent work item target", handoff.Action)
	}
	review := findProjectHealthAttentionForTest(t, response.Data.Attention, "Review follow-up: Review server attention")
	if review.WorkItemID != "work_review" || review.ActionLabel != "Open review" {
		t.Fatalf("review attention = %+v, want review work target", review)
	}
	assertProjectHealthActionForTest(t, review, projectActionOpenWorkItem, "proj_health")
	if review.Action.WorkItemID != "work_review" {
		t.Fatalf("review action = %+v, want work item target", review.Action)
	}
	context := findProjectHealthAttentionForTest(t, response.Data.Attention, "No project memory or context sources enabled")
	assertProjectHealthActionForTest(t, context, projectActionOpenMemoryReview, "proj_health")
	candidate := findProjectHealthAttentionForTest(t, response.Data.Attention, "Memory candidate pending review")
	assertProjectHealthActionForTest(t, candidate, projectActionReviewMemoryCandidate, "proj_health")
	if candidate.CandidateID != "memcand_health" || candidate.Action.CandidateID != "memcand_health" {
		t.Fatalf("candidate attention = %+v, want memory candidate target", candidate)
	}

	afterAssignments, err := handler.projectWork.ListAssignments(t.Context(), projectwork.AssignmentFilter{ProjectID: "proj_health"})
	if err != nil {
		t.Fatalf("ListAssignments after: %v", err)
	}
	afterCandidates, err := handler.memoryCandidates.ListCandidates(t.Context(), memory.CandidateFilter{ProjectID: "proj_health"})
	if err != nil {
		t.Fatalf("ListCandidates after: %v", err)
	}
	if len(afterAssignments) != len(beforeAssignments) || len(afterCandidates) != len(beforeCandidates) {
		t.Fatalf("health mutated project state: assignments %d->%d candidates %d->%d", len(beforeAssignments), len(afterAssignments), len(beforeCandidates), len(afterCandidates))
	}
}

func TestProjectHealth_CairnlineConfiguredUsesReadModel(t *testing.T) {
	t.Parallel()
	handler := NewHandler(config.Config{
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	server := NewServer(quietLogger(), handler)
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:    "proj_health_cairnline",
		Name:  "Health Cairnline",
		Roots: []projects.Root{{ID: "root_health", Path: t.TempDir(), Kind: "git", Active: true}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_review",
		ProjectID: "proj_health_cairnline",
		Title:     "Review server attention",
		Status:    projectwork.WorkItemStatusReview,
		Priority:  "high",
	}); err != nil {
		t.Fatalf("CreateWorkItem(work_review): %v", err)
	}
	if _, err := handler.projectWork.CreateAssignment(t.Context(), projectwork.Assignment{
		ID:         "asgn_review",
		ProjectID:  "proj_health_cairnline",
		WorkItemID: "work_review",
		RoleID:     "software_developer",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusRunning,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateAssignment(asgn_review): %v", err)
	}
	if _, err := handler.projectWork.CreateHandoff(t.Context(), projectwork.Handoff{
		ID:                    "handoff_review",
		ProjectID:             "proj_health_cairnline",
		WorkItemID:            "work_review",
		SourceAssignmentID:    "asgn_review",
		TargetRoleID:          "reviewer_qa",
		Title:                 "QA follow-up",
		Summary:               "Review needs an operator path.",
		RecommendedNextAction: "Create a follow-up assignment.",
		Status:                projectwork.HandoffStatusPending,
	}); err != nil {
		t.Fatalf("CreateHandoff: %v", err)
	}
	if _, err := handler.projectWork.CreateArtifact(t.Context(), projectwork.CollaborationArtifact{
		ID:                     "art_review",
		ProjectID:              "proj_health_cairnline",
		WorkItemID:             "work_review",
		AssignmentID:           "asgn_review",
		Kind:                   projectwork.ArtifactKindReview,
		Title:                  "QA review",
		Body:                   "Needs a follow-up path.",
		ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:             projectwork.ReviewRiskMedium,
		ReviewFollowUpRequired: true,
	}); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:                  "memcand_health",
		ProjectID:           "proj_health_cairnline",
		Title:               "Remember the review boundary",
		Body:                "Attention derivation stays read-only.",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		Status:              memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_health_cairnline/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHealthEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.Data.ReadBackend != "cairnline" {
		t.Fatalf("health read_backend = %q, want cairnline", response.Data.ReadBackend)
	}
	if response.Data.Summary.PendingMemoryCandidateCount != 1 || response.Data.Summary.PendingHandoffCount != 1 || response.Data.Summary.ReviewFollowUpCount != 1 || response.Data.Summary.ChangesRequestedReviewCount != 1 {
		t.Fatalf("health summary = %+v, want Cairnline-backed candidate/handoff/review counts", response.Data.Summary)
	}
	assertProjectHealthItemsHaveActions(t, response.Data.Attention, "proj_health_cairnline")
	handoff := findProjectHealthAttentionForTest(t, response.Data.Attention, "Pending handoff: Review server attention")
	if handoff.Action.Type != projectActionOpenWorkItem || handoff.WorkItemID != "work_review" {
		t.Fatalf("handoff attention = %+v, want Cairnline-backed work item action", handoff)
	}
	review := findProjectHealthAttentionForTest(t, response.Data.Attention, "Review follow-up: Review server attention")
	if review.Action.Type != projectActionOpenWorkItem || review.WorkItemID != "work_review" || review.Status != "awaiting_approval" {
		t.Fatalf("review attention = %+v, want Cairnline-backed review follow-up", review)
	}
	candidate := findProjectHealthAttentionForTest(t, response.Data.Attention, "Memory candidate pending review")
	if candidate.CandidateID != "memcand_health" || candidate.Action.CandidateID != "memcand_health" {
		t.Fatalf("candidate attention = %+v, want Cairnline-backed memory candidate target", candidate)
	}
}

func TestProjectHealth_ProfileAndSkillReferences(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:                  "proj_refs",
		Name:                "References",
		Roots:               []projects.Root{{ID: "root_refs", Path: t.TempDir(), Kind: "git", Active: true}},
		DefaultProvider:     "openai",
		DefaultModel:        "gpt-5",
		DefaultAgentProfile: "missing_profile",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_refs",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:                  "role_refs",
		ProjectID:           "proj_refs",
		Name:                "Reference owner",
		DefaultAgentProfile: "implementation",
		SkillIDs:            []string{"backend", "review"},
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), "proj_refs", []projectskills.Skill{{
		ID:        "backend",
		ProjectID: "proj_refs",
		Path:      "backend/SKILL.md",
		Enabled:   false,
		Status:    projectskills.StatusAvailable,
	}}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_refs/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHealthEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	assertProjectHealthItemsHaveActions(t, response.Data.Attention, "proj_refs")
	profiles := findProjectHealthAttentionForTest(t, response.Data.Attention, "Agent profile reference missing")
	assertProjectHealthActionForTest(t, profiles, projectActionOpenProfiles, "proj_refs")
	if !strings.Contains(profiles.Detail, "missing_profile") {
		t.Fatalf("profile attention = %+v, want missing profile detail", profiles)
	}
	skills := findProjectHealthAttentionForTest(t, response.Data.Attention, "Project skills need review")
	assertProjectHealthActionForTest(t, skills, projectActionOpenSkills, "proj_refs")
	if !strings.Contains(skills.Detail, "unresolved: review") || !strings.Contains(skills.Detail, "disabled: backend") {
		t.Fatalf("skill attention = %+v, want unresolved and disabled skill detail", skills)
	}
}

func TestProjectHealth_StandalonePendingHandoffAttention(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:              "proj_standalone_handoff",
		Name:            "Standalone Handoff",
		Roots:           []projects.Root{{ID: "root_standalone_handoff", Path: t.TempDir(), Kind: "git", Active: true}},
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_standalone_handoff",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateWorkItem(t.Context(), projectwork.WorkItem{
		ID:        "work_standalone_handoff",
		ProjectID: "proj_standalone_handoff",
		Title:     "Review standalone handoff",
		Status:    projectwork.WorkItemStatusReview,
		Priority:  "normal",
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := handler.projectWork.CreateHandoff(t.Context(), projectwork.Handoff{
		ID:                    "handoff_standalone",
		ProjectID:             "proj_standalone_handoff",
		WorkItemID:            "work_standalone_handoff",
		TargetRoleID:          "reviewer_qa",
		Title:                 "Review follow-up",
		Summary:               "Needs a reviewer decision.",
		RecommendedNextAction: "Open the work item and create the next assignment.",
		Status:                projectwork.HandoffStatusPending,
	}); err != nil {
		t.Fatalf("CreateHandoff: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_standalone_handoff/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHealthEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if response.Data.Summary.PendingHandoffCount != 1 {
		t.Fatalf("PendingHandoffCount = %d, want 1", response.Data.Summary.PendingHandoffCount)
	}
	assertProjectHealthItemsHaveActions(t, response.Data.Attention, "proj_standalone_handoff")
	handoff := findProjectHealthAttentionForTest(t, response.Data.Attention, "Pending handoff: Review standalone handoff")
	if handoff.WorkItemID != "work_standalone_handoff" || handoff.ActionLabel != "Open handoff" || handoff.Bucket != "" {
		t.Fatalf("handoff attention = %+v, want standalone work target", handoff)
	}
	assertProjectHealthActionForTest(t, handoff, projectActionOpenWorkItem, "proj_standalone_handoff")
	if handoff.Action.WorkItemID != "work_standalone_handoff" || handoff.Action.HandoffID != "handoff_standalone" {
		t.Fatalf("handoff action = %+v, want standalone handoff work target", handoff.Action)
	}
}

func TestProjectHealth_AttentionCapReportsOmittedItems(t *testing.T) {
	t.Parallel()
	handler, server := newProjectWorkTestServer()
	if _, err := handler.projects.Create(t.Context(), projects.Project{
		ID:                  "proj_cap_health",
		Name:                "Health Cap",
		DefaultAgentProfile: "missing_profile",
	}); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	if _, err := handler.projectWork.CreateRole(t.Context(), projectwork.AgentRoleProfile{
		ID:        "role_cap",
		ProjectID: "proj_cap_health",
		Name:      "Cap role",
		SkillIDs:  []string{"missing_skill"},
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := handler.memoryCandidates.CreateCandidate(t.Context(), memory.Candidate{
		ID:        "memcand_cap",
		ProjectID: "proj_cap_health",
		Title:     "Remember cap behavior",
		Body:      "The server reports omitted attention items.",
		Status:    memory.CandidateStatusPending,
	}); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hecate/v1/projects/proj_cap_health/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response ProjectHealthEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	assertProjectHealthItemsHaveActions(t, response.Data.Attention, "proj_cap_health")
	if response.Data.Summary.AttentionCount != projectHealthAttentionLimit || response.Data.Summary.AvailableAttentionCount <= projectHealthAttentionLimit || response.Data.Summary.OmittedAttentionCount == 0 {
		t.Fatalf("health summary = %+v, want omitted attention count", response.Data.Summary)
	}
	if len(response.Data.Attention) != projectHealthAttentionLimit {
		t.Fatalf("attention length = %d, want %d", len(response.Data.Attention), projectHealthAttentionLimit)
	}
}

func findProjectHealthAttentionForTest(t *testing.T, items []ProjectHealthAttentionItem, title string) ProjectHealthAttentionItem {
	t.Helper()
	for _, item := range items {
		if item.Title == title {
			return item
		}
	}
	t.Fatalf("project health attention %q not found in %+v", title, items)
	return ProjectHealthAttentionItem{}
}

func assertProjectHealthItemsHaveActions(t *testing.T, items []ProjectHealthAttentionItem, projectID string) {
	t.Helper()
	for _, item := range items {
		if strings.TrimSpace(item.ProjectID) == "" {
			t.Fatalf("attention item %q has empty project_id: %+v", item.ID, item)
		}
		if item.ProjectID != projectID {
			t.Fatalf("attention item %q project_id = %q, want %q", item.ID, item.ProjectID, projectID)
		}
		if strings.TrimSpace(item.Action.Type) == "" {
			t.Fatalf("attention item %q has empty action type: %+v", item.ID, item.Action)
		}
		if strings.TrimSpace(item.Action.ProjectID) == "" {
			t.Fatalf("attention item %q has empty action project_id: %+v", item.ID, item.Action)
		}
		if item.Action.ProjectID != item.ProjectID {
			t.Fatalf("attention item %q action project_id = %q, want item project_id %q", item.ID, item.Action.ProjectID, item.ProjectID)
		}
	}
}

func assertProjectHealthActionForTest(t *testing.T, item ProjectHealthAttentionItem, actionType, projectID string) {
	t.Helper()
	if item.ProjectID != projectID {
		t.Fatalf("attention item project_id = %q, want %q in %+v", item.ProjectID, projectID, item)
	}
	if item.Action.Type != actionType || item.Action.ProjectID != projectID {
		t.Fatalf("attention action = %+v, want type %q for project %q", item.Action, actionType, projectID)
	}
}
