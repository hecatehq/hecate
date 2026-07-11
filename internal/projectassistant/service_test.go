package projectassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
	"github.com/hecatehq/hecate/pkg/types"
)

type assistantFixture struct {
	service          *Service
	projects         projects.Store
	chats            chat.Store
	work             projectwork.Store
	projectSkills    projectskills.Store
	memoryEntries    memory.Store
	memoryCandidates memory.CandidateStore
	proposals        ProposalStore
}

type assistantFixtureBuilder struct {
	name  string
	build func(t *testing.T) assistantFixture
}

type failingAssignmentWorkAuthority struct {
	WorkAuthority
	err error
}

func (authority failingAssignmentWorkAuthority) CreateAssignment(context.Context, string, string, WorkAssignmentCommand) (projectwork.Assignment, error) {
	return projectwork.Assignment{}, authority.err
}

type fakeAssistantLLM struct {
	response string
	err      error
	requests []types.ChatRequest
}

type readOnlyProjectAuthority struct {
	ProjectAuthority
	project projects.Project
	err     error
}

func (authority readOnlyProjectAuthority) GetProject(_ context.Context, projectID string) (projects.Project, bool, error) {
	if authority.err != nil {
		return projects.Project{}, false, authority.err
	}
	if strings.TrimSpace(projectID) != authority.project.ID {
		return projects.Project{}, false, nil
	}
	return authority.project, true, nil
}

func (f *fakeAssistantLLM) Chat(_ context.Context, req types.ChatRequest) (*types.ChatResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return &types.ChatResponse{
		Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", Content: f.response},
		}},
	}, nil
}

func TestService_ProposeRejectsUnknownActionKind(t *testing.T) {
	t.Parallel()
	fixture := newMemoryAssistantFixture(t)

	_, err := fixture.service.Propose(context.Background(), ProposalInput{
		Actions: []Action{{Kind: "rewrite_the_world", Patch: rawPatch(t, map[string]string{"name": "bad"})}},
	})
	if !errors.Is(err, ErrUnknownActionKind) {
		t.Fatalf("Propose err = %v, want ErrUnknownActionKind", err)
	}
}

func TestService_ProposeStoresProposalRecordAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)

			proposal, err := fixture.service.Propose(ctx, ProposalInput{
				ProjectID: "proj_store",
				Source:    ProposalSourceAPI,
				Title:     "Create stored work",
				Summary:   "Persist the proposal record.",
				Actions: []Action{{
					Kind:   ActionCreateWorkItem,
					Target: map[string]string{"project_id": "proj_store"},
					Patch:  rawPatch(t, map[string]string{"project_id": "proj_store", "title": "Stored work"}),
				}},
				Warnings: []string{"review before applying"},
				TraceID:  "trace_store",
			})
			if err != nil {
				t.Fatalf("Propose: %v", err)
			}

			record, ok, err := fixture.service.Proposal(ctx, proposal.ID)
			if err != nil || !ok {
				t.Fatalf("Proposal ok=%v err=%v, want stored record", ok, err)
			}
			if record.ID != proposal.ID || record.ProjectID != "proj_store" || record.Source != ProposalSourceAPI || record.Status != ProposalStatusProposed {
				t.Fatalf("record = %+v, want proposed API record scoped to project", record)
			}
			if record.Proposal.TraceID != "trace_store" || len(record.Proposal.Warnings) != 1 || record.Proposal.Warnings[0] != "review before applying" {
				t.Fatalf("stored proposal = %+v, want trace and warnings", record.Proposal)
			}
			if record.Fingerprint == "" {
				t.Fatalf("fingerprint is empty")
			}
		})
	}
}

func TestProposalStore_ListProposalsFiltersOrdersAndHydratesAttempts(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			oldTime := time.Date(2024, 6, 27, 9, 0, 0, 0, time.UTC)
			newTime := oldTime.Add(time.Hour)

			oldRecord := proposalStoreTestRecord(t, "pa_old", "proj_alpha", oldTime)
			newRecord := proposalStoreTestRecord(t, "pa_new", "proj_alpha", newTime)
			otherRecord := proposalStoreTestRecord(t, "pa_other", "proj_beta", newTime.Add(time.Minute))
			for _, record := range []ProposalRecord{oldRecord, newRecord, otherRecord} {
				if _, err := fixture.proposals.UpsertProposal(ctx, record); err != nil {
					t.Fatalf("UpsertProposal(%s): %v", record.ID, err)
				}
			}
			attemptResult := ApplyResult{
				ProposalID:           newRecord.ID,
				Status:               ApplyStatusBlockedBeforeApply,
				Applied:              false,
				TotalActionCount:     1,
				CommittedActionCount: 0,
			}
			if _, err := fixture.proposals.RecordApplyAttempt(ctx, ApplyAttempt{
				ID:           "paatt_new",
				ProposalID:   newRecord.ID,
				Status:       ApplyStatusBlockedBeforeApply,
				Confirmed:    false,
				Result:       attemptResult,
				ErrorMessage: "confirmation required",
				CreatedAt:    newTime.Add(time.Minute),
			}); err != nil {
				t.Fatalf("RecordApplyAttempt: %v", err)
			}

			alpha, err := fixture.proposals.ListProposals(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("ListProposals(project): %v", err)
			}
			if len(alpha) != 2 || alpha[0].ID != "pa_new" || alpha[1].ID != "pa_old" {
				t.Fatalf("project list ids = %+v, want pa_new then pa_old", proposalRecordIDs(alpha))
			}
			if alpha[0].LatestResult == nil || alpha[0].LatestResult.Status != ApplyStatusBlockedBeforeApply || len(alpha[0].ApplyAttempts) != 1 || alpha[0].ApplyAttempts[0].ID != "paatt_new" {
				t.Fatalf("new record = %+v, want hydrated latest result and attempt", alpha[0])
			}
			all, err := fixture.proposals.ListProposals(ctx, "")
			if err != nil {
				t.Fatalf("ListProposals(all): %v", err)
			}
			if len(all) != 3 {
				t.Fatalf("all list len = %d, want 3", len(all))
			}
		})
	}
}

func TestService_DraftCreatesAssignmentProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_plan",
				ProjectID:   project.ID,
				Title:       "Plan next work",
				Brief:       "Pick the next reviewable task.",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: "product_manager",
				RootID:      "root_feature",
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Queue product planning\nPrefer a reviewable handoff.",
				DriverKind: projectwork.AssignmentDriverExternalAgent,
				TraceID:    "trace_draft",
			})
			if err != nil {
				t.Fatalf("Draft assignment: %v", err)
			}
			if proposal.Title != "Queue product planning" || proposal.TraceID != "trace_draft" {
				t.Fatalf("proposal title/trace = %q/%q, want request title and trace", proposal.Title, proposal.TraceID)
			}
			if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateAssignment {
				t.Fatalf("actions = %+v, want one create_assignment", proposal.Actions)
			}
			patch := rawPatchMap(t, proposal.Actions[0].Patch)
			if patch["project_id"] != project.ID || patch["work_item_id"] != workItem.ID || patch["role_id"] != "product_manager" || patch["root_id"] != "root_feature" || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent || patch["status"] != projectwork.AssignmentStatusQueued {
				t.Fatalf("assignment patch = %+v, want project/work/owner-role/root/external queued", patch)
			}
		})
	}
}

func TestService_DraftReviewFollowUpCreatesLinkedProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_review",
				ProjectID:   project.ID,
				Title:       "Review requested changes",
				Brief:       "Follow up on review findings.",
				Status:      projectwork.WorkItemStatusReview,
				OwnerRoleID: "product_manager",
				RootID:      "root_a",
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			if _, err := fixture.work.CreateAssignment(ctx, projectwork.Assignment{
				ID:         "asgn_impl",
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				RoleID:     "software_developer",
				DriverKind: projectwork.AssignmentDriverHecateTask,
				Status:     projectwork.AssignmentStatusCompleted,
			}); err != nil {
				t.Fatalf("CreateAssignment(impl): %v", err)
			}
			if _, err := fixture.work.CreateAssignment(ctx, projectwork.Assignment{
				ID:         "asgn_review",
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				RoleID:     "reviewer_qa",
				DriverKind: projectwork.AssignmentDriverHecateTask,
				Status:     projectwork.AssignmentStatusCompleted,
			}); err != nil {
				t.Fatalf("CreateAssignment(review): %v", err)
			}
			if _, err := fixture.work.CreateArtifact(ctx, projectwork.CollaborationArtifact{
				ID:                     "artifact_review",
				ProjectID:              project.ID,
				WorkItemID:             workItem.ID,
				AssignmentID:           "asgn_review",
				Kind:                   projectwork.ArtifactKindReview,
				Title:                  "QA reviewer review",
				Body:                   "Verdict: Changes requested\n\nFollow-up:\nUpdate empty-state spacing.",
				ReviewedAssignmentID:   "asgn_impl",
				ReviewVerdict:          projectwork.ReviewVerdictChangesRequested,
				ReviewRisk:             projectwork.ReviewRiskMedium,
				ReviewFollowUpRequired: true,
			}); err != nil {
				t.Fatalf("CreateArtifact(review): %v", err)
			}

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID:        project.ID,
				WorkItemID:       workItem.ID,
				DraftMode:        DraftModeReviewFollowUp,
				ReviewArtifactID: "artifact_review",
				TraceID:          "trace_review_followup",
			})
			if err != nil {
				t.Fatalf("Draft review follow-up: %v", err)
			}
			if proposal.TraceID != "trace_review_followup" || len(proposal.Actions) != 3 {
				t.Fatalf("proposal = %+v, want traced three-action proposal", proposal)
			}
			if proposal.Actions[0].Kind != ActionCreateHandoff || proposal.Actions[1].Kind != ActionCreateAssignment || proposal.Actions[2].Kind != ActionUpdateHandoff {
				t.Fatalf("actions = %+v, want create_handoff/create_assignment/update_handoff", proposal.Actions)
			}
			var handoff handoffPatch
			if err := json.Unmarshal(proposal.Actions[0].Patch, &handoff); err != nil {
				t.Fatalf("decode handoff patch: %v", err)
			}
			var assignment assignmentPatch
			if err := json.Unmarshal(proposal.Actions[1].Patch, &assignment); err != nil {
				t.Fatalf("decode assignment patch: %v", err)
			}
			var handoffUpdate updateHandoffPatch
			if err := json.Unmarshal(proposal.Actions[2].Patch, &handoffUpdate); err != nil {
				t.Fatalf("decode handoff update patch: %v", err)
			}
			if handoff.ID == "" || assignment.ID == "" || handoffUpdate.TargetAssignmentID == nil || *handoffUpdate.TargetAssignmentID != assignment.ID {
				t.Fatalf("generated ids handoff=%+v assignment=%+v update=%+v, want linked ids", handoff, assignment, handoffUpdate)
			}
			if handoff.SourceAssignmentID != "asgn_review" || handoff.TargetRoleID != "software_developer" || len(handoff.LinkedArtifactIDs) != 1 || handoff.LinkedArtifactIDs[0] != "artifact_review" {
				t.Fatalf("handoff patch = %+v, want review source and reviewed-assignment role", handoff)
			}
			if assignment.RoleID != "software_developer" || assignment.Status != projectwork.AssignmentStatusQueued || assignment.RootID != "root_a" {
				t.Fatalf("assignment patch = %+v, want queued reviewed-role assignment with root", assignment)
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("Apply review follow-up proposal: %v", err)
			}
			if !result.Applied || len(result.Actions) != 3 {
				t.Fatalf("apply result = %+v, want three applied actions", result)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: workItem.ID})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			var createdAssignment projectwork.Assignment
			for _, item := range assignments {
				if item.ID == assignment.ID {
					createdAssignment = item
					break
				}
			}
			if createdAssignment.ID == "" || createdAssignment.Status != projectwork.AssignmentStatusQueued {
				t.Fatalf("created assignment = %+v, want queued follow-up assignment", createdAssignment)
			}
			handoffs, err := fixture.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: project.ID, WorkItemID: workItem.ID})
			if err != nil {
				t.Fatalf("ListHandoffs: %v", err)
			}
			if len(handoffs) != 1 || handoffs[0].Status != projectwork.HandoffStatusAccepted || handoffs[0].TargetAssignmentID != assignment.ID {
				t.Fatalf("handoffs = %+v, want accepted linked follow-up handoff", handoffs)
			}
		})
	}
}

func TestService_DraftCreatesWorkItemProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID: project.ID,
				Request:   "Write rollout notes\nCapture release caveats.",
				RoleID:    "architect",
			})
			if err != nil {
				t.Fatalf("Draft work item: %v", err)
			}
			if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateWorkItem {
				t.Fatalf("actions = %+v, want one create_work_item", proposal.Actions)
			}
			patch := rawPatchMap(t, proposal.Actions[0].Patch)
			if patch["project_id"] != project.ID || patch["title"] != "Write rollout notes" || patch["brief"] != "Capture release caveats." || patch["owner_role_id"] != "architect" {
				t.Fatalf("work item patch = %+v, want project/title/brief/owner role", patch)
			}
		})
	}
}

func TestService_DraftBootstrapCreatesGuidanceAndSkillRoleProposalAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".hecate", "skills", "research"), 0o755); err != nil {
				t.Fatalf("mkdir skill: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, ".hecate", "skills", "research", "SKILL.md"), []byte("# Research\n"), 0o644); err != nil {
				t.Fatalf("write skill: %v", err)
			}
			project, err := fixture.projects.Create(ctx, projects.Project{
				ID:   "proj_bootstrap",
				Name: "Bootstrap Project",
				Roots: []projects.Root{{
					ID:     "root_bootstrap",
					Path:   root,
					Kind:   "git",
					Active: true,
				}},
				ContextSources: []projects.ContextSource{{
					ID:             "ctx_agents",
					Kind:           "workspace_instruction",
					Title:          "AGENTS.md",
					Path:           "AGENTS.md",
					Enabled:        true,
					Format:         "agents_md",
					Scope:          "workspace",
					TrustLabel:     "workspace_guidance",
					SourceCategory: "workspace_guidance",
					Metadata:       map[string]string{"root_id": "root_bootstrap"},
				}},
			})
			if err != nil {
				t.Fatalf("Create project: %v", err)
			}
			discovered, warnings := projectskills.Discover(ctx, project)
			if len(warnings) != 0 {
				t.Fatalf("Discover project skills warnings = %+v, want none", warnings)
			}
			if _, err := fixture.projectSkills.UpsertDiscovered(ctx, project.ID, discovered); err != nil {
				t.Fatalf("UpsertDiscovered: %v", err)
			}

			proposal, err := fixture.service.Draft(ctx, DraftInput{
				ProjectID: project.ID,
				Request:   "Set up project guidance",
				DraftMode: DraftModeBootstrap,
				TraceID:   "trace_bootstrap",
			})
			if err != nil {
				t.Fatalf("Draft bootstrap: %v", err)
			}
			if proposal.TraceID != "trace_bootstrap" || !proposal.RequiresConfirmation {
				t.Fatalf("proposal trace/confirmation = %q/%v, want trace and confirmation", proposal.TraceID, proposal.RequiresConfirmation)
			}
			if proposal.Title != "Set up Bootstrap Project guidance" {
				t.Fatalf("proposal title = %q, want setup title", proposal.Title)
			}
			if len(proposal.Actions) != 2 {
				t.Fatalf("actions = %+v, want guidance candidate and skill role", proposal.Actions)
			}
			if proposal.Actions[0].Kind != ActionCreateMemoryCandidate || proposal.Actions[1].Kind != ActionCreateRole {
				t.Fatalf("action kinds = %s/%s, want memory candidate then role", proposal.Actions[0].Kind, proposal.Actions[1].Kind)
			}
			var memoryPatch memoryCandidatePatch
			if err := json.Unmarshal(proposal.Actions[0].Patch, &memoryPatch); err != nil {
				t.Fatalf("decode memory patch: %v", err)
			}
			if memoryPatch.SuggestedSourceKind != "context_source" || memoryPatch.SuggestedSourceID != "ctx_agents" || memoryPatch.SuggestedTrustLabel != "workspace_guidance" {
				t.Fatalf("memory patch = %+v, want context-source provenance", memoryPatch)
			}
			if !strings.Contains(memoryPatch.Body, "provenance only") {
				t.Fatalf("memory body = %q, want provenance-only warning", memoryPatch.Body)
			}
			var rolePatch rolePatch
			if err := json.Unmarshal(proposal.Actions[1].Patch, &rolePatch); err != nil {
				t.Fatalf("decode role patch: %v", err)
			}
			if rolePatch.ID != "skill_research" || rolePatch.Name != "Research" || rolePatch.DefaultDriverKind != projectwork.AssignmentDriverHecateTask {
				t.Fatalf("role patch = %+v, want skill-derived role", rolePatch)
			}
			if len(rolePatch.SkillIDs) != 1 || rolePatch.SkillIDs[0] != "research" {
				t.Fatalf("role skill ids = %+v, want research", rolePatch.SkillIDs)
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("Apply bootstrap: %v", err)
			}
			if !result.Applied || len(result.Actions) != 2 {
				t.Fatalf("apply result = %+v, want two applied actions", result)
			}
			candidates, err := fixture.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
				ProjectID: project.ID,
				Status:    memory.CandidateStatusPending,
			})
			if err != nil {
				t.Fatalf("ListCandidates: %v", err)
			}
			if len(candidates) != 1 || candidates[0].SuggestedSourceKind != "context_source" || candidates[0].SuggestedSourceID != "ctx_agents" {
				t.Fatalf("candidates = %+v, want context-source memory candidate", candidates)
			}
			roles, err := fixture.work.ListRoles(ctx, project.ID)
			if err != nil {
				t.Fatalf("ListRoles: %v", err)
			}
			if !contextRoleExists(roleContexts(roles), "skill_research") {
				t.Fatalf("roles = %+v, want skill_research custom role", roles)
			}
			for _, role := range roles {
				if role.ID == "skill_research" && (len(role.SkillIDs) != 1 || role.SkillIDs[0] != "research") {
					t.Fatalf("role skill ids after apply = %+v, want research", role.SkillIDs)
				}
			}
		})
	}
}

func TestService_DraftBootstrapIgnoresRepoSpecificDocsAISkills(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs-ai", "skills", "research"), 0o755); err != nil {
		t.Fatalf("mkdir docs-ai skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs-ai", "skills", "research", "SKILL.md"), []byte("# Research\n"), 0o644); err != nil {
		t.Fatalf("write docs-ai skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Instructions\n\nNo skill registry is declared here.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	project, err := fixture.projects.Create(ctx, projects.Project{
		ID:   "proj_docs_ai_bootstrap",
		Name: "Docs AI Bootstrap",
		Roots: []projects.Root{{
			ID:     "root_docs_ai",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{{
			ID:             "ctx_docs_ai_agents",
			Kind:           "workspace_instruction",
			Title:          "AGENTS.md",
			Path:           "AGENTS.md",
			Enabled:        true,
			Format:         "agents_md",
			Scope:          "workspace",
			TrustLabel:     "workspace_guidance",
			SourceCategory: "workspace_guidance",
		}},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID: project.ID,
		Request:   "Set up project guidance",
		DraftMode: DraftModeBootstrap,
	})
	if err != nil {
		t.Fatalf("Draft bootstrap: %v", err)
	}
	for _, action := range proposal.Actions {
		if action.Kind == ActionCreateRole {
			t.Fatalf("actions = %+v, want docs-ai skills ignored for generic bootstrap", proposal.Actions)
		}
	}
}

func TestService_DraftBootstrapUsesRegisteredSkillDirsFromGuidanceReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs-ai", "skills", "research"), 0o755); err != nil {
		t.Fatalf("mkdir docs-ai skill: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "claude-skills", "review"), 0o755); err != nil {
		t.Fatalf("mkdir review claude skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs-ai", "skills", "research", "SKILL.md"), []byte("# Research\n"), 0o644); err != nil {
		t.Fatalf("write docs-ai skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "claude-skills", "review", "SKILL.md"), []byte("# Review\n"), 0o644); err != nil {
		t.Fatalf("write review claude skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Canonical skill registry: [`docs-ai/skills/README.md`](docs-ai/skills/README.md).\nUse [`docs-ai/skills/research/SKILL.md`](docs-ai/skills/research/SKILL.md).\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("Claude-compatible entrypoint. Use [`claude-skills/review/SKILL.md`](claude-skills/review/SKILL.md).\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	project, err := fixture.projects.Create(ctx, projects.Project{
		ID:   "proj_guidance_skill_refs",
		Name: "Guidance Skill Refs",
		Roots: []projects.Root{{
			ID:     "root_guidance_skill_refs",
			Path:   root,
			Kind:   "git",
			Active: true,
		}},
		ContextSources: []projects.ContextSource{
			{
				ID:             "ctx_guidance_agents",
				Kind:           "workspace_instruction",
				Title:          "AGENTS.md",
				Path:           "AGENTS.md",
				Enabled:        true,
				Format:         "agents_md",
				Scope:          "workspace",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "workspace_guidance",
				Metadata:       map[string]string{"root_id": "root_guidance_skill_refs"},
			},
			{
				ID:             "ctx_guidance_claude",
				Kind:           "host_instruction",
				Title:          "CLAUDE.md",
				Path:           "CLAUDE.md",
				Enabled:        true,
				Format:         "claude_md",
				Scope:          "workspace",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "workspace_guidance",
				Metadata:       map[string]string{"root_id": "root_guidance_skill_refs", "host": "claude"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	discovered, warnings := projectskills.Discover(ctx, project)
	if len(warnings) != 0 {
		t.Fatalf("Discover project skills warnings = %+v, want none", warnings)
	}
	if _, err := fixture.projectSkills.UpsertDiscovered(ctx, project.ID, discovered); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID: project.ID,
		Request:   "Set up project guidance",
		DraftMode: DraftModeBootstrap,
	})
	if err != nil {
		t.Fatalf("Draft bootstrap: %v", err)
	}
	foundClaudeMemory := false
	found := map[string]bool{}
	for _, action := range proposal.Actions {
		if action.Kind == ActionCreateMemoryCandidate {
			var patch memoryCandidatePatch
			if err := json.Unmarshal(action.Patch, &patch); err != nil {
				t.Fatalf("decode memory patch: %v", err)
			}
			if patch.SuggestedSourceID == "ctx_guidance_claude" {
				foundClaudeMemory = true
				if !strings.Contains(patch.Body, "Agent Preset settings") || strings.Contains(patch.Body, "project profile settings") {
					t.Fatalf("host guidance memory body = %q, want Agent Preset wording without legacy project profile wording", patch.Body)
				}
			}
		}
		if action.Kind != ActionCreateRole {
			continue
		}
		var patch rolePatch
		if err := json.Unmarshal(action.Patch, &patch); err != nil {
			t.Fatalf("decode role patch: %v", err)
		}
		switch patch.ID {
		case "skill_research":
			found[patch.ID] = true
			if !strings.Contains(patch.Instructions, "docs-ai/skills/research/SKILL.md") {
				t.Fatalf("role instructions = %q, want guidance-derived skill path", patch.Instructions)
			}
			if len(patch.SkillIDs) != 1 || patch.SkillIDs[0] != "research" {
				t.Fatalf("role skill ids = %+v, want research", patch.SkillIDs)
			}
		case "skill_review":
			found[patch.ID] = true
			if !strings.Contains(patch.Instructions, "claude-skills/review/SKILL.md") {
				t.Fatalf("role instructions = %q, want Claude guidance-derived skill path", patch.Instructions)
			}
			if len(patch.SkillIDs) != 1 || patch.SkillIDs[0] != "review" {
				t.Fatalf("role skill ids = %+v, want review", patch.SkillIDs)
			}
		}
	}
	for _, id := range []string{"skill_research", "skill_review"} {
		if !found[id] {
			t.Fatalf("actions = %+v, want role %s from guidance skill reference", proposal.Actions, id)
		}
	}
	if !foundClaudeMemory {
		t.Fatalf("actions = %+v, want host guidance memory candidate for CLAUDE.md", proposal.Actions)
	}
}

func TestService_DraftBootstrapSkipsDisabledAndConflictingSkills(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	if _, err := fixture.projectSkills.UpsertDiscovered(ctx, project.ID, []projectskills.Skill{
		{
			ID:         "available",
			Title:      "Available",
			Path:       ".hecate/skills/available/SKILL.md",
			Format:     projectskills.FormatSkillMD,
			Enabled:    true,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
		},
		{
			ID:         "disabled",
			Title:      "Disabled",
			Path:       ".hecate/skills/disabled/SKILL.md",
			Format:     projectskills.FormatSkillMD,
			Enabled:    false,
			Status:     projectskills.StatusAvailable,
			TrustLabel: projectskills.TrustWorkspaceSkill,
		},
		{
			ID:         "conflict",
			Title:      "Conflict",
			Path:       ".hecate/skills/conflict/SKILL.md",
			Format:     projectskills.FormatSkillMD,
			Enabled:    true,
			Status:     projectskills.StatusConflict,
			TrustLabel: projectskills.TrustWorkspaceSkill,
		},
	}); err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID: project.ID,
		Request:   "Bootstrap roles from skills",
		DraftMode: DraftModeBootstrap,
	})
	if err != nil {
		t.Fatalf("Draft bootstrap: %v", err)
	}
	roleIDs := map[string]bool{}
	for _, action := range proposal.Actions {
		if action.Kind != ActionCreateRole {
			continue
		}
		var patch rolePatch
		if err := json.Unmarshal(action.Patch, &patch); err != nil {
			t.Fatalf("decode role patch: %v", err)
		}
		roleIDs[patch.ID] = true
	}
	if !roleIDs["skill_available"] || roleIDs["skill_disabled"] || roleIDs["skill_conflict"] {
		t.Fatalf("role ids = %+v, want only available skill role", roleIDs)
	}
	if len(proposal.Warnings) < 2 || !strings.Contains(strings.Join(proposal.Warnings, "\n"), "disabled") || !strings.Contains(strings.Join(proposal.Warnings, "\n"), "conflict") {
		t.Fatalf("warnings = %+v, want disabled and conflict warnings", proposal.Warnings)
	}
}

func TestService_ContextBuildsProjectAssistantPacketAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
				ID:                "planner",
				ProjectID:         project.ID,
				Name:              "Planning Lead",
				DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
				SkillIDs:          []string{"planning"},
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			if _, err := fixture.projectSkills.UpsertDiscovered(ctx, project.ID, []projectskills.Skill{{
				ID:          "planning",
				ProjectID:   project.ID,
				Title:       "Planning",
				Description: "Plan scoped project work.",
				Path:        ".hecate/skills/planning/SKILL.md",
				RootID:      "root_a",
				Format:      projectskills.FormatSkillMD,
				Enabled:     true,
				Status:      projectskills.StatusAvailable,
				TrustLabel:  projectskills.TrustWorkspaceSkill,
			}}); err != nil {
				t.Fatalf("UpsertDiscovered skill: %v", err)
			}
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_context",
				ProjectID:   project.ID,
				Title:       "Shape assistant context",
				Brief:       "Define what the assistant sees.",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: role.ID,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			if _, err := fixture.work.CreateAssignment(ctx, projectwork.Assignment{
				ID:         "asgn_context",
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				RoleID:     role.ID,
				DriverKind: projectwork.AssignmentDriverExternalAgent,
				Status:     projectwork.AssignmentStatusRunning,
			}); err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if _, err := fixture.memoryEntries.Create(ctx, memory.Entry{
				ID:         "mem_context",
				ProjectID:  project.ID,
				Title:      "Assistant context decision",
				Body:       "Drafts inspect context before proposing durable changes.",
				TrustLabel: memory.TrustLabelOperatorMemory,
				SourceKind: memory.SourceKindOperator,
				Enabled:    true,
			}); err != nil {
				t.Fatalf("Create memory: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_context",
				ProjectID:           project.ID,
				Title:               "Candidate context note",
				Body:                "Candidate memory is visible but still reviewable.",
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusPending,
			}); err != nil {
				t.Fatalf("CreateCandidate: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_rejected",
				ProjectID:           project.ID,
				Title:               "Rejected context note",
				Body:                "Rejected suggestions stay out of assistant context.",
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusRejected,
			}); err != nil {
				t.Fatalf("Create rejected candidate: %v", err)
			}

			packet, err := fixture.service.Context(ctx, ContextInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Queue planning",
			})
			if err != nil {
				t.Fatalf("Context: %v", err)
			}
			if packet.Project.ID != project.ID || packet.SelectedWork == nil || packet.SelectedWork.ID != workItem.ID {
				t.Fatalf("context project/work = %+v/%+v, want selected project/work", packet.Project, packet.SelectedWork)
			}
			if packet.Selection.RoleID != role.ID || packet.Selection.RoleSource != "selected_work_owner" || packet.Selection.DriverKind != projectwork.AssignmentDriverExternalAgent || packet.Selection.DriverSource != "role_default" {
				t.Fatalf("selection = %+v, want owner role and role default driver", packet.Selection)
			}
			if !strings.Contains(packet.Selection.Reason, "Selected work item is owned by Planning Lead") || !strings.Contains(packet.Selection.Reason, "Using external_agent") {
				t.Fatalf("selection reason = %q, want owner/default explanation", packet.Selection.Reason)
			}
			if len(packet.Roles) == 0 || !contextRoleExists(packet.Roles, role.ID) {
				t.Fatalf("roles = %+v, want custom role included", packet.Roles)
			}
			var plannerRole *RoleContext
			for idx := range packet.Roles {
				if packet.Roles[idx].ID == role.ID {
					plannerRole = &packet.Roles[idx]
					break
				}
			}
			if plannerRole == nil || len(plannerRole.SkillIDs) != 1 || plannerRole.SkillIDs[0] != "planning" {
				t.Fatalf("role context = %+v, want planning skill id", plannerRole)
			}
			if len(packet.Skills) != 1 || packet.Skills[0].ID != "planning" || packet.Skills[0].Path == "" {
				t.Fatalf("skills = %+v, want planning metadata", packet.Skills)
			}
			if len(packet.Assignments) != 1 || packet.Assignments[0].ID != "asgn_context" {
				t.Fatalf("assignments = %+v, want recent assignment", packet.Assignments)
			}
			if len(packet.Memory) != 1 || packet.Memory[0].ID != "mem_context" {
				t.Fatalf("memory = %+v, want enabled project memory", packet.Memory)
			}
			if len(packet.MemoryCandidates) != 1 || packet.MemoryCandidates[0].ID != "cand_context" {
				t.Fatalf("memory candidates = %+v, want candidate", packet.MemoryCandidates)
			}
			if len(packet.RecentActivity) == 0 {
				t.Fatalf("recent activity is empty, want context timeline entries")
			}
		})
	}
}

func TestService_ContextReadsProjectFromAuthority(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	project := projects.Project{ID: "proj_authority_context", Name: "Authority Context"}
	workStore := projectwork.NewMemoryStore()
	if _, err := workStore.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	}); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if _, err := workStore.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_authority_context",
		ProjectID:   project.ID,
		Title:       "Shape authority-backed context",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: "planner",
	}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	service := NewService(Stores{
		ProjectAuthority: readOnlyProjectAuthority{project: project},
		Work:             workStore,
	}, sequenceIDGenerator())

	packet, err := service.Context(ctx, ContextInput{
		ProjectID:  project.ID,
		WorkItemID: "work_authority_context",
		Request:    "Queue authority-backed context work",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if packet.Project.ID != project.ID || packet.Project.Name != project.Name || packet.SelectedWork == nil || packet.SelectedWork.ID != "work_authority_context" {
		t.Fatalf("context project/work = %+v/%+v, want authority project and selected work", packet.Project, packet.SelectedWork)
	}
	if packet.Selection.RoleID != "planner" || packet.Selection.DriverKind != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("selection = %+v, want owner role selected from authority-backed context", packet.Selection)
	}
}

func TestService_ContextRejectsMissingExplicitRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)

	_, err := fixture.service.Context(ctx, ContextInput{
		ProjectID: project.ID,
		Request:   "Draft with missing role",
		RoleID:    "missing_role",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Context err = %v, want ErrNotFound", err)
	}
}

func TestService_ContextBudgetsMemoryBodiesAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			memoryBody := strings.Repeat("é", contextMemoryBodyMaxBytes)
			candidateBody := strings.Repeat("é", contextCandidateBodyMaxBytes)
			if _, err := fixture.memoryEntries.Create(ctx, memory.Entry{
				ID:         "mem_budget",
				ProjectID:  project.ID,
				Title:      "Large project memory",
				Body:       memoryBody,
				TrustLabel: memory.TrustLabelOperatorMemory,
				SourceKind: memory.SourceKindOperator,
				Enabled:    true,
			}); err != nil {
				t.Fatalf("Create memory: %v", err)
			}
			if _, err := fixture.memoryCandidates.CreateCandidate(ctx, memory.Candidate{
				ID:                  "cand_budget",
				ProjectID:           project.ID,
				Title:               "Large candidate memory",
				Body:                candidateBody,
				SuggestedTrustLabel: memory.TrustLabelGenerated,
				SuggestedSourceKind: memory.SourceKindGenerated,
				Status:              memory.CandidateStatusPending,
			}); err != nil {
				t.Fatalf("CreateCandidate: %v", err)
			}

			packet, err := fixture.service.Context(ctx, ContextInput{
				ProjectID: project.ID,
				Request:   "Inspect budgeted context",
			})
			if err != nil {
				t.Fatalf("Context: %v", err)
			}
			if len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
				t.Fatalf("memory/candidates = %+v/%+v, want one of each", packet.Memory, packet.MemoryCandidates)
			}
			gotMemory := packet.Memory[0]
			if !gotMemory.BodyTruncated || len(gotMemory.Body) > contextMemoryBodyMaxBytes || !strings.HasSuffix(gotMemory.Body, contextTruncatedSuffix) || !utf8.ValidString(gotMemory.Body) {
				t.Fatalf("memory body budget = len:%d truncated:%v suffix:%v valid:%v", len(gotMemory.Body), gotMemory.BodyTruncated, strings.HasSuffix(gotMemory.Body, contextTruncatedSuffix), utf8.ValidString(gotMemory.Body))
			}
			if gotMemory.BodyOriginalBytes != len(memoryBody) || gotMemory.BodyReturnedBytes != len(gotMemory.Body) || gotMemory.BodyTokensEstimate != estimateTokensFromBytes(len(gotMemory.Body)) {
				t.Fatalf("memory budget metadata = %+v, want original/returned bytes and token estimate", gotMemory)
			}
			gotCandidate := packet.MemoryCandidates[0]
			if !gotCandidate.BodyTruncated || len(gotCandidate.Body) > contextCandidateBodyMaxBytes || !strings.HasSuffix(gotCandidate.Body, contextTruncatedSuffix) || !utf8.ValidString(gotCandidate.Body) {
				t.Fatalf("candidate body budget = len:%d truncated:%v suffix:%v valid:%v", len(gotCandidate.Body), gotCandidate.BodyTruncated, strings.HasSuffix(gotCandidate.Body, contextTruncatedSuffix), utf8.ValidString(gotCandidate.Body))
			}
			if gotCandidate.BodyOriginalBytes != len(candidateBody) || gotCandidate.BodyReturnedBytes != len(gotCandidate.Body) || gotCandidate.BodyTokensEstimate != estimateTokensFromBytes(len(gotCandidate.Body)) {
				t.Fatalf("candidate budget metadata = %+v, want original/returned bytes and token estimate", gotCandidate)
			}
			if packet.Budget.MemoryBodyMaxBytes != contextMemoryBodyMaxBytes || packet.Budget.MemoryCandidateBodyMaxBytes != contextCandidateBodyMaxBytes || packet.Budget.BodyTruncatedCount != 2 {
				t.Fatalf("context budget = %+v, want byte limits and two truncated bodies", packet.Budget)
			}
			if packet.Budget.BodyOriginalBytes != gotMemory.BodyOriginalBytes+gotCandidate.BodyOriginalBytes || packet.Budget.BodyReturnedBytes != gotMemory.BodyReturnedBytes+gotCandidate.BodyReturnedBytes || packet.Budget.BodyTokensEstimate != gotMemory.BodyTokensEstimate+gotCandidate.BodyTokensEstimate {
				t.Fatalf("context budget totals = %+v, want memory + candidate totals", packet.Budget)
			}
		})
	}
}

func TestService_DraftUsesContextSelection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_context_draft",
		ProjectID:   project.ID,
		Title:       "Draft from context",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		Request:    "Queue selected owner",
	})
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	patch := rawPatchMap(t, proposal.Actions[0].Patch)
	if patch["role_id"] != role.ID || patch["driver_kind"] != projectwork.AssignmentDriverExternalAgent {
		t.Fatalf("patch = %+v, want context-selected owner role and role default driver", patch)
	}
}

func TestService_DraftModelUsesLLMProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	if _, err := fixture.projects.Update(ctx, project.ID, func(project *projects.Project) {
		project.DefaultProvider = "ollama"
		project.DefaultModel = "llama3.1:8b"
	}); err != nil {
		t.Fatalf("set project defaults: %v", err)
	}
	role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
		ID:                "planner",
		ProjectID:         project.ID,
		Name:              "Planning Lead",
		DefaultDriverKind: projectwork.AssignmentDriverExternalAgent,
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
		ID:          "work_model",
		ProjectID:   project.ID,
		Title:       "Model-backed draft",
		Status:      projectwork.WorkItemStatusReady,
		OwnerRoleID: role.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	llm := &fakeAssistantLLM{
		response: `{
			"title": "Queue planning review",
			"summary": "Queue the selected role for the selected work item.",
			"actions": [{
				"kind": "create_assignment",
				"target": {"project_id": "proj_alpha"},
				"patch": {
					"project_id": "proj_alpha",
					"work_item_id": "work_model",
					"role_id": "planner",
					"driver_kind": "external_agent",
					"status": "queued"
				},
				"reason": "Queue reviewable work without starting execution."
			}]
		}`,
	}
	fixture.service.llm = llm

	proposal, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		Request:    "Queue planning review",
		DraftMode:  DraftModeModel,
		Provider:   "openai",
		Model:      "gpt-test",
		RequestID:  "req_model_draft",
		TraceID:    "trace_model_draft",
	})
	if err != nil {
		t.Fatalf("Draft model: %v", err)
	}
	if proposal.Title != "Queue planning review" || proposal.TraceID != "trace_model_draft" {
		t.Fatalf("proposal title/trace = %q/%q, want model title and trace", proposal.Title, proposal.TraceID)
	}
	if len(proposal.Actions) != 1 || proposal.Actions[0].Kind != ActionCreateAssignment {
		t.Fatalf("actions = %+v, want model create_assignment", proposal.Actions)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("llm requests = %d, want one", len(llm.requests))
	}
	req := llm.requests[0]
	if req.RequestID != "req_model_draft" || req.Model != "gpt-test" || req.Scope.ProviderHint != "openai" {
		t.Fatalf("llm request = %+v, want explicit request/model/provider", req)
	}
	if !strings.Contains(string(req.ResponseFormat), "json_object") || len(req.Messages) != 2 {
		t.Fatalf("llm response format/messages = %s/%d, want json object and system+user", string(req.ResponseFormat), len(req.Messages))
	}
	if !strings.Contains(req.Messages[1].Content, `"selected_work"`) || !strings.Contains(req.Messages[1].Content, `"selection"`) {
		t.Fatalf("llm prompt = %q, want selected work and selection context", req.Messages[1].Content)
	}
}

func TestService_DraftModelRejectsMissingModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)
	llm := &fakeAssistantLLM{response: `{}`}
	fixture.service.llm = llm

	_, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID: project.ID,
		Request:   "Draft with no configured model",
		DraftMode: DraftModeModel,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Draft err = %v, want ErrInvalid", err)
	}
	if len(llm.requests) != 0 {
		t.Fatalf("llm requests = %d, want none when model is missing", len(llm.requests))
	}
}

func TestService_DraftModelRejectsOutOfScopeActions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name     string
		response string
	}{
		{
			name: "project action",
			response: `{
				"title": "Create project",
				"actions": [{
					"kind": "create_project",
					"target": {"project_id": "proj_alpha"},
					"patch": {"name": "Other project"}
				}]
			}`,
		},
		{
			name: "wrong project",
			response: `{
				"title": "Create work elsewhere",
				"actions": [{
					"kind": "create_work_item",
					"target": {"project_id": "proj_other"},
					"patch": {"project_id": "proj_other", "title": "Elsewhere"}
				}]
			}`,
		},
		{
			name: "assignment binds run",
			response: `{
				"title": "Bind existing run",
				"actions": [{
					"kind": "create_assignment",
					"target": {"project_id": "proj_alpha"},
					"patch": {
						"project_id": "proj_alpha",
						"work_item_id": "work_model_guard",
						"role_id": "planner",
						"driver_kind": "hecate_task",
						"status": "queued",
						"run_id": "run_existing"
					}
				}]
			}`,
		},
		{
			name: "memory candidate claims operator provenance",
			response: `{
				"title": "Remember generated note",
				"actions": [{
					"kind": "create_memory_candidate",
					"target": {"project_id": "proj_alpha"},
					"patch": {
						"project_id": "proj_alpha",
						"title": "Generated note",
						"body": "This was generated by a model draft.",
						"suggested_trust_label": "operator_memory",
						"suggested_source_kind": "operator"
					}
				}]
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMemoryAssistantFixture(t)
			project := createTestProject(t, ctx, fixture.projects)
			if _, err := fixture.projects.Update(ctx, project.ID, func(project *projects.Project) {
				project.DefaultModel = "llama3.1:8b"
			}); err != nil {
				t.Fatalf("set project defaults: %v", err)
			}
			role, err := fixture.work.CreateRole(ctx, projectwork.AgentRoleProfile{
				ID:                "planner",
				ProjectID:         project.ID,
				Name:              "Planning Lead",
				DefaultDriverKind: projectwork.AssignmentDriverHecateTask,
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:          "work_model_guard",
				ProjectID:   project.ID,
				Title:       "Guard model draft",
				Status:      projectwork.WorkItemStatusReady,
				OwnerRoleID: role.ID,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			llm := &fakeAssistantLLM{response: tc.response}
			fixture.service.llm = llm

			_, err = fixture.service.Draft(ctx, DraftInput{
				ProjectID:  project.ID,
				WorkItemID: workItem.ID,
				Request:    "Draft guarded action",
				DraftMode:  DraftModeModel,
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Draft err = %v, want ErrInvalid", err)
			}
			if len(llm.requests) != 1 {
				t.Fatalf("llm requests = %d, want one", len(llm.requests))
			}
		})
	}
}

func TestDecodeModelDraftResponseExtractsWrappedJSON(t *testing.T) {
	t.Parallel()
	draft, err := decodeModelDraftResponse(&types.ChatResponse{
		Choices: []types.ChatChoice{{
			Message: types.Message{Role: "assistant", Content: "```json\n{\"proposal\":{\"title\":\"Wrapped\",\"actions\":[{\"kind\":\"create_work_item\",\"target\":{\"project_id\":\"proj_alpha\"},\"patch\":{\"project_id\":\"proj_alpha\",\"title\":\"Wrapped work\"}}]}}\n```\nTrailing prose with {another brace}."},
		}},
	})
	if err != nil {
		t.Fatalf("decodeModelDraftResponse: %v", err)
	}
	if draft.Title != "Wrapped" || len(draft.Actions) != 1 || draft.Actions[0].Kind != ActionCreateWorkItem {
		t.Fatalf("draft = %+v, want wrapped create_work_item", draft)
	}
}

func TestService_DraftRejectsUnsupportedDriverKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newMemoryAssistantFixture(t)
	project := createTestProject(t, ctx, fixture.projects)

	_, err := fixture.service.Draft(ctx, DraftInput{
		ProjectID:  project.ID,
		Request:    "Queue speculative work",
		DriverKind: "spreadsheet_macro",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Draft err = %v, want ErrInvalid", err)
	}
}

func TestService_ApplyRequiresConfirmation(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_confirm",
				Title:                "Create project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateProject,
					Patch: rawPatch(t, map[string]string{"id": "proj_confirm", "name": "Confirm me"}),
				}},
			}

			_, err := fixture.service.Apply(ctx, proposal, false)
			if !errors.Is(err, ErrConfirmationRequired) {
				t.Fatalf("Apply err = %v, want ErrConfirmationRequired", err)
			}
			items, err := fixture.projects.List(ctx)
			if err != nil {
				t.Fatalf("list projects: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("projects = %+v, want none after unconfirmed apply", items)
			}
		})
	}
}

func TestApplyActionSpecsPairPreflightAndApplyHandlers(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, len(applyActionSpecs))
	for _, spec := range applyActionSpecs {
		if strings.TrimSpace(spec.kind) == "" {
			t.Fatal("apply action spec has empty kind")
		}
		if spec.preflight == nil {
			t.Fatalf("apply action spec %q has nil preflight handler", spec.kind)
		}
		if spec.apply == nil {
			t.Fatalf("apply action spec %q has nil apply handler", spec.kind)
		}
		if _, ok := seen[spec.kind]; ok {
			t.Fatalf("apply action spec %q is duplicated", spec.kind)
		}
		seen[spec.kind] = struct{}{}
		lookup, ok := lookupApplyActionSpec(spec.kind)
		if !ok || lookup.kind != spec.kind {
			t.Fatalf("lookupApplyActionSpec(%q) = %+v/%v, want same spec kind", spec.kind, lookup, ok)
		}
	}
}

func TestService_ApplyCreateAndUpdateProjectAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			toolsEnabled := true
			compactToolOutput := true

			proposal := Proposal{
				ID:                   "pa_project",
				Title:                "Project setup",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateProject,
						Patch: rawPatch(t, map[string]any{
							"id":          "proj_alpha",
							"name":        "Alpha",
							"description": "main",
							"roots": []map[string]any{{
								"id":   "root_a",
								"path": filepath.Join(t.TempDir(), "alpha"),
								"kind": "local",
							}},
						}),
					},
					{
						Kind:   ActionAttachProjectRoot,
						Target: map[string]string{"project_id": "proj_alpha"},
						Patch: rawPatch(t, map[string]any{
							"id":   "root_b",
							"path": filepath.Join(t.TempDir(), "beta"),
							"kind": "local",
						}),
					},
					{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": "proj_alpha"},
						Patch: rawPatch(t, map[string]any{
							"default_root_id":             "root_b",
							"default_provider":            "ollama",
							"default_model":               "llama3.1:8b",
							"default_agent_profile":       "reviewer",
							"default_tools_enabled":       toolsEnabled,
							"default_workspace_mode":      "worktree",
							"default_system_prompt":       "Stay crisp.",
							"default_compact_tool_output": compactToolOutput,
						}),
					},
					{
						Kind:   ActionRemoveProjectRoot,
						Target: map[string]string{"project_id": "proj_alpha", "root_id": "root_a"},
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("Apply setup: %v", err)
			}
			if result.Status != ApplyStatusApplied || !result.Applied || len(result.Actions) != len(proposal.Actions) {
				t.Fatalf("result = %+v, want all actions applied", result)
			}
			project, ok, err := fixture.projects.Get(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("get project: %v", err)
			}
			if !ok {
				t.Fatal("project not found after apply")
			}
			if project.Name != "Alpha" || project.Description != "main" {
				t.Fatalf("project metadata = %+v, want created metadata", project)
			}
			if len(project.Roots) != 1 || project.Roots[0].ID != "root_b" {
				t.Fatalf("project roots = %+v, want only root_b", project.Roots)
			}
			if project.DefaultRootID != "root_b" ||
				project.DefaultProvider != "ollama" ||
				project.DefaultModel != "llama3.1:8b" ||
				project.DefaultAgentProfile != "reviewer" ||
				project.DefaultWorkspaceMode != "worktree" ||
				project.DefaultSystemPrompt != "Stay crisp." {
				t.Fatalf("project defaults = %+v, want applied defaults", project)
			}
			if project.DefaultToolsEnabled == nil || !*project.DefaultToolsEnabled {
				t.Fatalf("default tools enabled = %v, want true", project.DefaultToolsEnabled)
			}
			if project.DefaultCompactToolOutput == nil || !*project.DefaultCompactToolOutput {
				t.Fatalf("default compact output = %v, want true", project.DefaultCompactToolOutput)
			}

			name := "Renamed"
			description := "Updated"
			_, err = fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_project_update",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:   ActionUpdateProject,
					Target: map[string]string{"project_id": "proj_alpha"},
					Patch:  rawPatch(t, map[string]any{"name": name, "description": description}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply update: %v", err)
			}
			project, ok, err = fixture.projects.Get(ctx, "proj_alpha")
			if err != nil || !ok {
				t.Fatalf("get updated project ok=%v err=%v", ok, err)
			}
			if project.Name != name || project.Description != description {
				t.Fatalf("updated project = %+v, want renamed/updated", project)
			}
		})
	}
}

func TestService_ApplyCreateProjectWorkspacePathAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			workspacePath := filepath.Join(t.TempDir(), "workspace")

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_workspace_project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateProject,
					Patch: rawPatch(t, map[string]any{
						"id":             "proj_workspace",
						"name":           "Workspace",
						"workspace_path": workspacePath,
						"workspace_kind": "git",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply create workspace project: %v", err)
			}

			project, ok, err := fixture.projects.Get(ctx, "proj_workspace")
			if err != nil || !ok {
				t.Fatalf("get project ok=%v err=%v", ok, err)
			}
			if len(project.Roots) != 1 {
				t.Fatalf("roots = %+v, want one generated workspace root", project.Roots)
			}
			root := project.Roots[0]
			if root.ID == "" || root.Path != workspacePath || root.Kind != "git" || !root.Active {
				t.Fatalf("root = %+v, want active git workspace root at %q", root, workspacePath)
			}
			if project.DefaultRootID != root.ID {
				t.Fatalf("default_root_id = %q, want generated root id %q", project.DefaultRootID, root.ID)
			}
		})
	}
}

func TestService_ApplyCreateProjectRejectsDuplicateIdentityAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			workspacePath := filepath.Join(t.TempDir(), "workspace")
			if _, err := fixture.projects.Create(ctx, projects.Project{
				ID:   "proj_existing",
				Name: "Existing",
				Roots: []projects.Root{{
					ID:     "root_existing",
					Path:   workspacePath,
					Active: true,
				}},
			}); err != nil {
				t.Fatalf("Create existing project: %v", err)
			}

			for _, tc := range []struct {
				name  string
				patch map[string]any
			}{
				{
					name: "duplicate name",
					patch: map[string]any{
						"id":   "proj_duplicate_name",
						"name": " existing ",
					},
				},
				{
					name: "duplicate workspace path",
					patch: map[string]any{
						"id":             "proj_duplicate_workspace",
						"name":           "Duplicate workspace",
						"workspace_path": workspacePath + string(filepath.Separator),
						"workspace_kind": "git",
					},
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := fixture.service.Apply(ctx, Proposal{
						ID:                   "pa_" + strings.ReplaceAll(tc.name, " ", "_"),
						RequiresConfirmation: true,
						Actions: []Action{{
							Kind:  ActionCreateProject,
							Patch: rawPatch(t, tc.patch),
						}},
					}, true)
					if !errors.Is(err, ErrConflict) {
						t.Fatalf("Apply err = %v, want ErrConflict", err)
					}
				})
			}
		})
	}
}

func TestService_ApplyCreateProjectWithoutWorkspaceAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_no_workspace_project",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateProject,
					Patch: rawPatch(t, map[string]string{
						"id":   "proj_no_workspace",
						"name": "No workspace",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply create workspace-less project: %v", err)
			}

			project, ok, err := fixture.projects.Get(ctx, "proj_no_workspace")
			if err != nil || !ok {
				t.Fatalf("get project ok=%v err=%v", ok, err)
			}
			if len(project.Roots) != 0 || project.DefaultRootID != "" {
				t.Fatalf("project = %+v, want workspace-less project", project)
			}
		})
	}
}

func TestService_ApplyCreateProjectRejectsWorkspacePathWithRoots(t *testing.T) {
	t.Parallel()
	fixture := newMemoryAssistantFixture(t)

	_, err := fixture.service.Apply(context.Background(), Proposal{
		ID:                   "pa_workspace_conflict",
		RequiresConfirmation: true,
		Actions: []Action{{
			Kind: ActionCreateProject,
			Patch: rawPatch(t, map[string]any{
				"id":             "proj_workspace_conflict",
				"name":           "Broken",
				"workspace_path": filepath.Join(t.TempDir(), "workspace"),
				"roots": []map[string]string{{
					"path": filepath.Join(t.TempDir(), "other"),
				}},
			}),
		}},
	}, true)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Apply err = %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "workspace_path cannot be combined with roots") {
		t.Fatalf("Apply err = %v, want workspace_path conflict", err)
	}
}

func TestService_ApplyRevalidatesStaleTargets(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			if _, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:        "work_stale_root",
				ProjectID: project.ID,
				Title:     "Stale root check",
				Status:    projectwork.WorkItemStatusReady,
			}); err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}

			cases := []struct {
				name   string
				action Action
			}{
				{
					name: "missing project",
					action: Action{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": "proj_missing"},
						Patch:  rawPatch(t, map[string]string{"default_model": "llama3.1:8b"}),
					},
				},
				{
					name: "missing root",
					action: Action{
						Kind:   ActionRemoveProjectRoot,
						Target: map[string]string{"project_id": project.ID, "root_id": "root_missing"},
					},
				},
				{
					name: "missing default root",
					action: Action{
						Kind:   ActionSetProjectDefaults,
						Target: map[string]string{"project_id": project.ID},
						Patch:  rawPatch(t, map[string]string{"default_root_id": "root_missing"}),
					},
				},
				{
					name: "missing assignment root",
					action: Action{
						Kind: ActionCreateAssignment,
						Patch: rawPatch(t, map[string]any{
							"project_id":   project.ID,
							"work_item_id": "work_stale_root",
							"role_id":      "software_developer",
							"root_id":      "root_missing",
							"driver_kind":  projectwork.AssignmentDriverHecateTask,
						}),
					},
				},
				{
					name: "missing chat",
					action: Action{
						Kind:   ActionMoveChatSession,
						Target: map[string]string{"chat_session_id": "chat_missing"},
						Patch:  rawPatch(t, map[string]string{"project_id": project.ID}),
					},
				},
			}

			for idx, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, err := fixture.service.Apply(ctx, Proposal{
						ID:                   fmt.Sprintf("pa_stale_%d", idx),
						RequiresConfirmation: true,
						Actions:              []Action{tc.action},
					}, true)
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("Apply err = %v, want ErrNotFound", err)
					}
					assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID})
					if err != nil {
						t.Fatalf("ListAssignments: %v", err)
					}
					if len(assignments) != 0 {
						t.Fatalf("assignments = %+v, want none after stale-target rejection", assignments)
					}
				})
			}
		})
	}
}

func TestService_ApplyMoveChatSessionUpdatesOnlyTarget(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			createTestChatSession(t, ctx, fixture.chats, "chat_a")
			createTestChatSession(t, ctx, fixture.chats, "chat_b")

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_move_chat",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:   ActionMoveChatSession,
					Target: map[string]string{"chat_session_id": "chat_a"},
					Patch:  rawPatch(t, map[string]string{"project_id": project.ID}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply move chat: %v", err)
			}

			chatA, ok, err := fixture.chats.Get(ctx, "chat_a")
			if err != nil || !ok {
				t.Fatalf("get chat_a ok=%v err=%v", ok, err)
			}
			chatB, ok, err := fixture.chats.Get(ctx, "chat_b")
			if err != nil || !ok {
				t.Fatalf("get chat_b ok=%v err=%v", ok, err)
			}
			if chatA.ProjectID != project.ID {
				t.Fatalf("chat_a project = %q, want %q", chatA.ProjectID, project.ID)
			}
			if chatB.ProjectID != "" {
				t.Fatalf("chat_b project = %q, want unchanged empty project", chatB.ProjectID)
			}
		})
	}
}

func TestService_ApplyCreatesProjectWorkRecords(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			result, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_project_work",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]any{
							"id":         "work_a",
							"project_id": project.ID,
							"title":      "Build assistant",
							"brief":      "Create typed project assistant actions.",
						}),
					},
					{
						Kind: ActionCreateAssignment,
						Patch: rawPatch(t, map[string]any{
							"id":           "asgn_a",
							"project_id":   project.ID,
							"work_item_id": "work_a",
							"role_id":      "software_developer",
							"root_id":      project.Roots[0].ID,
							"driver_kind":  projectwork.AssignmentDriverHecateTask,
						}),
					},
					{
						Kind: ActionCreateHandoff,
						Patch: rawPatch(t, map[string]any{
							"id":                      "handoff_a",
							"project_id":              project.ID,
							"work_item_id":            "work_a",
							"title":                   "Continue implementation",
							"summary":                 "Project assistant core is ready for UI.",
							"recommended_next_action": "Add project cockpit cards.",
						}),
					},
				},
			}, true)
			if err != nil {
				t.Fatalf("Apply project work: %v", err)
			}
			if !result.Applied || result.TotalActionCount != 3 || result.CommittedActionCount != 3 || result.ResumeActionIndex != 3 || result.FailedActionIndex != nil {
				t.Fatalf("apply result = %+v, want applied progress counts for 3 actions", result)
			}

			item, ok, err := fixture.work.GetWorkItem(ctx, project.ID, "work_a")
			if err != nil || !ok {
				t.Fatalf("get work item ok=%v err=%v", ok, err)
			}
			if item.Title != "Build assistant" || item.Status != projectwork.WorkItemStatusBacklog {
				t.Fatalf("work item = %+v, want backlog item", item)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: "work_a"})
			if err != nil {
				t.Fatalf("list assignments: %v", err)
			}
			if len(assignments) != 1 || assignments[0].ID != "asgn_a" || assignments[0].Status != projectwork.AssignmentStatusQueued || assignments[0].RootID != project.Roots[0].ID {
				t.Fatalf("assignments = %+v, want queued assignment with root", assignments)
			}
			handoffs, err := fixture.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: project.ID, WorkItemID: "work_a"})
			if err != nil {
				t.Fatalf("list handoffs: %v", err)
			}
			if len(handoffs) != 1 || handoffs[0].ID != "handoff_a" || handoffs[0].Status != projectwork.HandoffStatusPending {
				t.Fatalf("handoffs = %+v, want pending handoff", handoffs)
			}
		})
	}
}

func TestService_ApplyLoopFailureReportsCommittedProgressAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			applyErr := fmt.Errorf("%w: injected assignment failure", ErrConflict)
			fixture.service.workAuthority = failingAssignmentWorkAuthority{
				WorkAuthority: storeWorkAuthority{store: fixture.work},
				err:           applyErr,
			}

			proposal := Proposal{
				ID:                   "pa_apply_loop_partial",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]any{
							"id":         "work_loop_partial",
							"project_id": project.ID,
							"title":      "Apply loop partial",
						}),
					},
					{
						Kind: ActionCreateAssignment,
						Patch: rawPatch(t, map[string]any{
							"id":           "asgn_loop_partial",
							"project_id":   project.ID,
							"work_item_id": "work_loop_partial",
							"role_id":      "software_developer",
							"root_id":      project.Roots[0].ID,
							"driver_kind":  projectwork.AssignmentDriverHecateTask,
							"status":       projectwork.AssignmentStatusQueued,
						}),
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Apply err = %v, want ErrConflict", err)
			}
			var partial *ApplyError
			if !errors.As(err, &partial) {
				t.Fatalf("Apply err = %T %v, want ApplyError", err, err)
			}
			if partial.FailedActionIndex != 1 {
				t.Fatalf("failed_action_index = %d, want 1", partial.FailedActionIndex)
			}
			if result.Status != ApplyStatusPartialDueToRuntimeFailure || result.Applied || result.TotalActionCount != 2 || result.CommittedActionCount != 1 || result.ResumeActionIndex != 1 || result.FailedActionIndex == nil || *result.FailedActionIndex != 1 {
				t.Fatalf("partial result progress = %+v, want one committed action and failed action 1", result)
			}
			if len(result.Actions) != 1 || result.Actions[0].Kind != ActionCreateWorkItem || result.Actions[0].ID != "work_loop_partial" {
				t.Fatalf("partial actions = %+v, want committed work item action", result.Actions)
			}
			if partial.Result.CommittedActionCount != result.CommittedActionCount || len(partial.Result.Actions) != len(result.Actions) {
				t.Fatalf("apply error result = %+v, want returned partial result %+v", partial.Result, result)
			}
			if _, ok, err := fixture.work.GetWorkItem(ctx, project.ID, "work_loop_partial"); err != nil || !ok {
				t.Fatalf("GetWorkItem ok=%v err=%v, want committed work item", ok, err)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: "work_loop_partial"})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			if len(assignments) != 0 {
				t.Fatalf("assignments = %+v, want no assignment after injected apply failure", assignments)
			}
			record, ok, err := fixture.service.Proposal(ctx, proposal.ID)
			if err != nil || !ok {
				t.Fatalf("Proposal ok=%v err=%v, want partial record", ok, err)
			}
			if record.Status != ApplyStatusPartialDueToRuntimeFailure || record.LatestResult == nil || record.LatestResult.CommittedActionCount != 1 || len(record.ApplyAttempts) != 1 {
				t.Fatalf("record = %+v, want partial latest result and one attempt", record)
			}

			resumed := NewService(Stores{
				Projects:         fixture.projects,
				Chats:            fixture.chats,
				Work:             fixture.work,
				ProjectSkills:    fixture.projectSkills,
				Memory:           fixture.memoryEntries,
				MemoryCandidates: fixture.memoryCandidates,
				Proposals:        fixture.proposals,
			}, func(prefix string) string { return prefix + "_resume" })
			result, err = resumed.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("resumed Apply: %v", err)
			}
			if !result.Applied || result.CommittedActionCount != 2 || len(result.Actions) != 2 {
				t.Fatalf("resumed result = %+v, want second action applied from ledger progress", result)
			}
			assignments, err = fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID, WorkItemID: "work_loop_partial"})
			if err != nil {
				t.Fatalf("ListAssignments after resume: %v", err)
			}
			if len(assignments) != 1 || assignments[0].ID != "asgn_loop_partial" {
				t.Fatalf("assignments after resume = %+v, want one resumed assignment", assignments)
			}
			record, ok, err = resumed.Proposal(ctx, proposal.ID)
			if err != nil || !ok {
				t.Fatalf("Proposal after resume ok=%v err=%v, want record", ok, err)
			}
			if record.Status != ApplyStatusApplied || record.LatestResult == nil || !record.LatestResult.Applied || len(record.ApplyAttempts) != 2 {
				t.Fatalf("record after resume = %+v, want applied latest result and two attempts", record)
			}
		})
	}
}

func TestService_PreflightApplyUsesCommittedActionResults(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			actions := []Action{
				{
					Kind: ActionCreateWorkItem,
					Patch: rawPatch(t, map[string]any{
						"id":         "work_committed_only",
						"project_id": project.ID,
						"title":      "Committed outside compatibility store",
					}),
				},
				{
					Kind: ActionCreateAssignment,
					Patch: rawPatch(t, map[string]any{
						"id":           "asgn_committed_only",
						"project_id":   project.ID,
						"work_item_id": "work_committed_only",
						"role_id":      "software_developer",
						"root_id":      project.Roots[0].ID,
						"driver_kind":  projectwork.AssignmentDriverHecateTask,
					}),
				},
			}
			committed := []ActionResult{{
				Kind: ActionCreateWorkItem,
				ID:   "work_committed_only",
				Data: map[string]string{
					"project_id":   project.ID,
					"work_item_id": "work_committed_only",
				},
			}}

			if idx, err := fixture.service.preflightApply(ctx, actions, committed); err != nil {
				t.Fatalf("preflightApply failed at %d: %v", idx, err)
			}
			if _, ok, err := fixture.work.GetWorkItem(ctx, project.ID, "work_committed_only"); err != nil || ok {
				t.Fatalf("compatibility work item ok=%v err=%v, want absent committed-result-only item", ok, err)
			}
		})
	}
}

func TestService_ApplyRejectsNonQueuedAssignmentProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name        string
		patch       map[string]any
		errContains string
	}{
		{
			name: "execution link",
			patch: map[string]any{
				"status":  projectwork.AssignmentStatusQueued,
				"task_id": "task_existing",
			},
			errContains: "cannot bind chats, tasks, runs",
		},
		{
			name: "execution ref",
			patch: map[string]any{
				"status": projectwork.AssignmentStatusQueued,
				"execution_ref": map[string]any{
					"kind":    "task_run",
					"task_id": "task_existing",
				},
			},
			errContains: "cannot bind chats, tasks, runs",
		},
		{
			name: "running status",
			patch: map[string]any{
				"status": projectwork.AssignmentStatusRunning,
			},
			errContains: "must create queued assignments",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMemoryAssistantFixture(t)
			project := createTestProject(t, ctx, fixture.projects)
			if _, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:        "work_execution_link",
				ProjectID: project.ID,
				Title:     "Reject assignment links",
				Status:    projectwork.WorkItemStatusReady,
			}); err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			patch := map[string]any{
				"project_id":   project.ID,
				"work_item_id": "work_execution_link",
				"role_id":      "software_developer",
				"driver_kind":  projectwork.AssignmentDriverHecateTask,
			}
			for key, value := range tc.patch {
				patch[key] = value
			}

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_assignment_links_" + tc.name,
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateAssignment,
					Patch: rawPatch(t, patch),
				}},
			}, true)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Apply err = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("Apply err = %v, want %q", err, tc.errContains)
			}
			assignments, err := fixture.work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: project.ID})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			if len(assignments) != 0 {
				t.Fatalf("assignments = %+v, want none after rejected proposal", assignments)
			}
		})
	}
}

func TestService_ApplyCreatesMemoryCandidateOnly(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)

			_, err := fixture.service.Apply(ctx, Proposal{
				ID:                   "pa_memory_candidate",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind: ActionCreateMemoryCandidate,
					Patch: rawPatch(t, map[string]any{
						"id":             "memcand_a",
						"project_id":     project.ID,
						"title":          "Project assistant decision",
						"body":           "Project Assistant creates candidates, not durable entries.",
						"suggested_kind": "decision",
					}),
				}},
			}, true)
			if err != nil {
				t.Fatalf("Apply memory candidate: %v", err)
			}

			candidates, err := fixture.memoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
				ProjectID: project.ID,
				Status:    memory.CandidateStatusPending,
			})
			if err != nil {
				t.Fatalf("list candidates: %v", err)
			}
			if len(candidates) != 1 || candidates[0].ID != "memcand_a" {
				t.Fatalf("candidates = %+v, want one pending candidate", candidates)
			}
			entries, err := fixture.memoryEntries.List(ctx, memory.Filter{ProjectID: project.ID, IncludeDisabled: true})
			if err != nil {
				t.Fatalf("list memory entries: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("memory entries = %+v, want no durable entries", entries)
			}
		})
	}
}

func TestService_ApplyRepeatedProposalConflicts(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_repeat",
				RequiresConfirmation: true,
				Actions: []Action{{
					Kind:  ActionCreateProject,
					Patch: rawPatch(t, map[string]string{"id": "proj_repeat", "name": "Repeat"}),
				}},
			}

			if _, err := fixture.service.Apply(ctx, proposal, true); err != nil {
				t.Fatalf("first Apply: %v", err)
			}
			_, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("second Apply err = %v, want ErrConflict", err)
			}
		})
	}
}

func TestService_ApplyPreflightBlocksStaleTargetsBeforeMutatingAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_preflight_resume",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind:  ActionCreateProject,
						Patch: rawPatch(t, map[string]string{"id": "proj_preflight", "name": "Preflight"}),
					},
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]string{
							"id":         "work_after_retry",
							"project_id": "proj_after_retry",
							"title":      "Resume after missing project",
						}),
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Apply err = %v, want ErrNotFound", err)
			}
			var applyErr *ApplyError
			if !errors.As(err, &applyErr) {
				t.Fatalf("Apply err = %T %v, want ApplyError", err, err)
			}
			if applyErr.FailedActionIndex != 1 {
				t.Fatalf("failed_action_index = %d, want 1", applyErr.FailedActionIndex)
			}
			if result.Status != ApplyStatusBlockedBeforeApply || result.TotalActionCount != 2 || result.CommittedActionCount != 0 || result.ResumeActionIndex != 0 || result.FailedActionIndex == nil || *result.FailedActionIndex != 1 {
				t.Fatalf("partial result progress = %+v, want failed action 1 and resume action 0", result)
			}
			if result.Applied || len(result.Actions) != 0 {
				t.Fatalf("partial result = %+v, want no action results before preflight failure", result)
			}
			if applyErr.Result.ProposalID != result.ProposalID || len(applyErr.Result.Actions) != len(result.Actions) {
				t.Fatalf("apply error result = %+v, want returned partial result %+v", applyErr.Result, result)
			}
			if _, ok, err := fixture.projects.Get(ctx, "proj_preflight"); err != nil || ok {
				t.Fatalf("get preflight-blocked project ok=%v err=%v, want no durable mutation", ok, err)
			}

			if _, err := fixture.projects.Create(ctx, projects.Project{ID: "proj_after_retry", Name: "After retry"}); err != nil {
				t.Fatalf("create missing project before retry: %v", err)
			}
			result, err = fixture.service.Apply(ctx, proposal, true)
			if err != nil {
				t.Fatalf("retry Apply: %v", err)
			}
			if !result.Applied || len(result.Actions) != 2 {
				t.Fatalf("retry result = %+v, want both actions applied", result)
			}
			if _, ok, err := fixture.projects.Get(ctx, "proj_preflight"); err != nil || !ok {
				t.Fatalf("get project after retry ok=%v err=%v, want created project", ok, err)
			}
			item, ok, err := fixture.work.GetWorkItem(ctx, "proj_after_retry", "work_after_retry")
			if err != nil || !ok {
				t.Fatalf("get work item ok=%v err=%v", ok, err)
			}
			if item.Title != "Resume after missing project" {
				t.Fatalf("work item title = %q, want resumed action title", item.Title)
			}

			projectsAfterRetry, err := fixture.projects.List(ctx)
			if err != nil {
				t.Fatalf("list projects: %v", err)
			}
			var preflightProjectCount int
			for _, project := range projectsAfterRetry {
				if project.ID == "proj_preflight" {
					preflightProjectCount++
				}
			}
			if preflightProjectCount != 1 {
				t.Fatalf("proj_preflight count = %d, want one non-duplicated project", preflightProjectCount)
			}
		})
	}
}

func TestService_ApplyPreflightBlocksCloseoutBeforeMutatingAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:        "work_closeout_preflight",
				ProjectID: project.ID,
				Title:     "Closeout preflight",
				Status:    projectwork.WorkItemStatusReview,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			proposal := Proposal{
				ID:                   "pa_closeout_preflight",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateHandoff,
						Patch: rawPatch(t, map[string]any{
							"id":                      "handoff_pending",
							"project_id":              project.ID,
							"work_item_id":            workItem.ID,
							"title":                   "Pending follow-up",
							"summary":                 "Follow-up should block closeout.",
							"recommended_next_action": "Resolve before marking done.",
						}),
					},
					{
						Kind:   ActionUpdateWorkItem,
						Target: map[string]string{"project_id": project.ID, "work_item_id": workItem.ID},
						Patch:  rawPatch(t, map[string]string{"status": projectwork.WorkItemStatusDone}),
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrConflict) || !errors.Is(err, projectwork.ErrWorkItemCloseoutBlocked) {
				t.Fatalf("Apply err = %v result=%+v, want closeout conflict", err, result)
			}
			var applyErr *ApplyError
			if !errors.As(err, &applyErr) {
				t.Fatalf("Apply err = %T %v, want ApplyError", err, err)
			}
			if applyErr.FailedActionIndex != 1 || len(applyErr.Result.Actions) != 0 {
				t.Fatalf("apply error = %+v, want preflight failure at done action with no committed actions", applyErr)
			}
			if applyErr.Result.Status != ApplyStatusBlockedBeforeApply || applyErr.Result.TotalActionCount != 2 || applyErr.Result.CommittedActionCount != 0 || applyErr.Result.ResumeActionIndex != 0 || applyErr.Result.FailedActionIndex == nil || *applyErr.Result.FailedActionIndex != 1 {
				t.Fatalf("apply error progress = %+v, want failed action 1 and resume action 0", applyErr.Result)
			}
			handoffs, err := fixture.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: project.ID, WorkItemID: workItem.ID})
			if err != nil {
				t.Fatalf("ListHandoffs: %v", err)
			}
			if len(handoffs) != 0 {
				t.Fatalf("handoffs = %+v, want no durable handoff after preflight closeout block", handoffs)
			}
			stored, ok, err := fixture.work.GetWorkItem(ctx, project.ID, workItem.ID)
			if err != nil || !ok {
				t.Fatalf("GetWorkItem ok=%v err=%v, want stored item", ok, err)
			}
			if stored.Status == projectwork.WorkItemStatusDone {
				t.Fatalf("work item status = %q, want not done after preflight closeout block", stored.Status)
			}
		})
	}
}

func TestService_ApplyPreflightBlocksMissingHandoffTargetBeforeMutatingAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			project := createTestProject(t, ctx, fixture.projects)
			workItem, err := fixture.work.CreateWorkItem(ctx, projectwork.WorkItem{
				ID:        "work_handoff_preflight",
				ProjectID: project.ID,
				Title:     "Handoff preflight",
				Status:    projectwork.WorkItemStatusReview,
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}

			proposal := Proposal{
				ID:                   "pa_handoff_preflight",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind: ActionCreateHandoff,
						Patch: rawPatch(t, map[string]any{
							"id":                      "handoff_preflight",
							"project_id":              project.ID,
							"work_item_id":            workItem.ID,
							"title":                   "Needs follow-up",
							"summary":                 "A follow-up is required.",
							"recommended_next_action": "Create the missing assignment.",
						}),
					},
					{
						Kind:   ActionUpdateHandoff,
						Target: map[string]string{"project_id": project.ID, "work_item_id": workItem.ID, "handoff_id": "handoff_preflight"},
						Patch:  rawPatch(t, map[string]string{"target_assignment_id": "asgn_missing"}),
					},
				},
			}

			result, err := fixture.service.Apply(ctx, proposal, true)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Apply err = %v, want ErrNotFound", err)
			}
			var applyErr *ApplyError
			if !errors.As(err, &applyErr) {
				t.Fatalf("Apply err = %T %v, want ApplyError", err, err)
			}
			if applyErr.FailedActionIndex != 1 {
				t.Fatalf("failed_action_index = %d, want 1", applyErr.FailedActionIndex)
			}
			if result.Status != ApplyStatusBlockedBeforeApply || result.TotalActionCount != 2 || result.CommittedActionCount != 0 || result.ResumeActionIndex != 0 || result.FailedActionIndex == nil || *result.FailedActionIndex != 1 {
				t.Fatalf("result progress = %+v, want failed action 1 and resume action 0", result)
			}
			if result.Applied || len(result.Actions) != 0 {
				t.Fatalf("result = %+v, want no partial action results", result)
			}
			handoffs, err := fixture.work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: project.ID, WorkItemID: workItem.ID})
			if err != nil {
				t.Fatalf("ListHandoffs: %v", err)
			}
			if len(handoffs) != 0 {
				t.Fatalf("handoffs = %+v, want no durable handoff after preflight failure", handoffs)
			}
		})
	}
}

func TestService_ApplyChangedProposalAfterPreflightFailureConflictsAcrossStores(t *testing.T) {
	t.Parallel()
	for _, builder := range assistantFixtureBuilders() {
		t.Run(builder.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := builder.build(t)
			proposal := Proposal{
				ID:                   "pa_preflight_changed",
				RequiresConfirmation: true,
				Actions: []Action{
					{
						Kind:  ActionCreateProject,
						Patch: rawPatch(t, map[string]string{"id": "proj_preflight_changed", "name": "Preflight"}),
					},
					{
						Kind: ActionCreateWorkItem,
						Patch: rawPatch(t, map[string]string{
							"id":         "work_changed",
							"project_id": "proj_missing_changed",
							"title":      "Original title",
						}),
					},
				},
			}
			if _, err := fixture.service.Apply(ctx, proposal, true); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Apply err = %v, want ErrNotFound", err)
			}

			changed := proposal
			changed.Actions = cloneActions(proposal.Actions)
			changed.Actions[1].Patch = rawPatch(t, map[string]string{
				"id":         "work_changed",
				"project_id": "proj_missing_changed",
				"title":      "Changed title",
			})
			_, err := fixture.service.Apply(ctx, changed, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("changed Apply err = %v, want ErrConflict", err)
			}
		})
	}
}

func assistantFixtureBuilders() []assistantFixtureBuilder {
	return []assistantFixtureBuilder{
		{name: "memory", build: newMemoryAssistantFixture},
	}
}

func newMemoryAssistantFixture(t *testing.T) assistantFixture {
	t.Helper()
	projectStore := projects.NewMemoryStore()
	chatStore := chat.NewMemoryStore()
	workStore := projectwork.NewMemoryStore()
	projectSkillStore := projectskills.NewMemoryStore()
	memoryStore := memory.NewMemoryStore()
	proposalStore := NewMemoryProposalStore()
	return assistantFixture{
		service:          projectassistantService(projectStore, chatStore, workStore, projectSkillStore, memoryStore, proposalStore),
		projects:         projectStore,
		chats:            chatStore,
		work:             workStore,
		projectSkills:    projectSkillStore,
		memoryEntries:    memoryStore,
		memoryCandidates: memoryStore,
		proposals:        proposalStore,
	}
}

func projectassistantService(projectStore projects.Store, chatStore chat.Store, workStore projectwork.Store, projectSkillStore projectskills.Store, memoryStore memory.Store, proposalStore ProposalStore) *Service {
	var candidateStore memory.CandidateStore
	if candidates, ok := memoryStore.(memory.CandidateStore); ok {
		candidateStore = candidates
	}
	return NewService(Stores{
		Projects:         projectStore,
		Chats:            chatStore,
		Work:             workStore,
		ProjectSkills:    projectSkillStore,
		Memory:           memoryStore,
		MemoryCandidates: candidateStore,
		Proposals:        proposalStore,
	}, sequenceIDGenerator())
}

func sequenceIDGenerator() IDGenerator {
	var seq int
	return func(prefix string) string {
		seq++
		return fmt.Sprintf("%s_%02d", prefix, seq)
	}
}

func rawPatch(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return payload
}

func rawPatchMap(t *testing.T, payload json.RawMessage) map[string]string {
	t.Helper()
	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	return decoded
}

func proposalStoreTestRecord(t *testing.T, id, projectID string, updatedAt time.Time) ProposalRecord {
	t.Helper()
	return ProposalRecord{
		ID:        id,
		ProjectID: projectID,
		Source:    ProposalSourceAPI,
		Status:    ProposalStatusProposed,
		Proposal: Proposal{
			ID:      id,
			Title:   "Stored proposal " + id,
			Summary: "Used by proposal store listing tests.",
			Actions: []Action{{
				Kind:   ActionCreateWorkItem,
				Target: map[string]string{"project_id": projectID},
				Patch: rawPatch(t, map[string]string{
					"project_id": projectID,
					"title":      "Work " + id,
				}),
			}},
			RequiresConfirmation: true,
		},
		CreatedAt: updatedAt.Add(-time.Minute),
		UpdatedAt: updatedAt,
	}
}

func proposalRecordIDs(records []ProposalRecord) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, record.ID)
	}
	return out
}

func contextRoleExists(roles []RoleContext, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func createTestProject(t *testing.T, ctx context.Context, store projects.Store) projects.Project {
	t.Helper()
	project, err := store.Create(ctx, projects.Project{
		ID:   "proj_alpha",
		Name: "Alpha",
		Roots: []projects.Root{{
			ID:     "root_a",
			Path:   filepath.Join(t.TempDir(), "alpha"),
			Kind:   "local",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func createTestChatSession(t *testing.T, ctx context.Context, store chat.Store, id string) chat.Session {
	t.Helper()
	session, err := store.Create(ctx, chat.Session{ID: id, Title: id})
	if err != nil {
		t.Fatalf("create chat session %q: %v", id, err)
	}
	return session
}
