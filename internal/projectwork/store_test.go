package projectwork

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/storage"
)

func TestStoreConformance_ProjectWorkLifecycle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)

			roles, err := store.ListRoles(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("ListRoles built-ins: %v", err)
			}
			if len(roles) < 8 || !roleIDExists(roles, "product_manager") || !roleIDExists(roles, "reviewer_qa") {
				t.Fatalf("built-in roles = %+v, want default project team roles", roles)
			}

			custom, err := store.CreateRole(ctx, AgentRoleProfile{
				ID:                  "role_release_captain",
				ProjectID:           "proj_alpha",
				Name:                " Release Captain ",
				Description:         " Coordinates releases ",
				Instructions:        "Keep release notes current.",
				DefaultDriverKind:   " hecate_task ",
				DefaultProvider:     " ollama ",
				DefaultModel:        " ministral-3:latest ",
				DefaultAgentProfile: " implementation ",
				SkillIDs:            []string{"qa", "backend", "backend"},
			})
			if err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			if custom.Name != "Release Captain" || custom.BuiltIn {
				t.Fatalf("custom role = %+v, want normalized custom role", custom)
			}
			if custom.DefaultDriverKind != AssignmentDriverHecateTask || custom.DefaultProvider != "ollama" || custom.DefaultModel != "ministral-3:latest" || custom.DefaultAgentProfile != "implementation" {
				t.Fatalf("custom role defaults = %+v, want normalized execution defaults", custom)
			}
			if len(custom.SkillIDs) != 2 || custom.SkillIDs[0] != "backend" || custom.SkillIDs[1] != "qa" {
				t.Fatalf("custom role skill ids = %+v, want normalized skill ids", custom.SkillIDs)
			}
			updatedRole, err := store.UpdateRole(ctx, "proj_alpha", "role_release_captain", func(item *AgentRoleProfile) {
				item.DefaultDriverKind = AssignmentDriverExternalAgent
				item.DefaultProvider = "anthropic"
				item.DefaultModel = "claude-sonnet-4"
				item.DefaultAgentProfile = "safe_external_review"
				item.SkillIDs = []string{"review", "backend", "review"}
			})
			if err != nil {
				t.Fatalf("UpdateRole defaults: %v", err)
			}
			if updatedRole.DefaultDriverKind != AssignmentDriverExternalAgent || updatedRole.DefaultProvider != "anthropic" || updatedRole.DefaultModel != "claude-sonnet-4" || updatedRole.DefaultAgentProfile != "safe_external_review" {
				t.Fatalf("updated role defaults = %+v, want updated defaults", updatedRole)
			}
			if len(updatedRole.SkillIDs) != 2 || updatedRole.SkillIDs[0] != "backend" || updatedRole.SkillIDs[1] != "review" {
				t.Fatalf("updated role skill ids = %+v, want normalized skill ids", updatedRole.SkillIDs)
			}
			if _, err := store.UpdateRole(ctx, "proj_alpha", "role_release_captain", func(item *AgentRoleProfile) {
				item.DefaultDriverKind = "unsupported"
			}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpdateRole unsupported default driver error = %v, want ErrInvalid", err)
			}
			if _, err := store.CreateRole(ctx, AgentRoleProfile{ID: "product_manager", ProjectID: "proj_alpha", Name: "Override"}); !errors.Is(err, ErrBuiltInRole) {
				t.Fatalf("CreateRole built-in error = %v, want ErrBuiltInRole", err)
			}
			if err := store.DeleteRole(ctx, "proj_alpha", "product_manager"); !errors.Is(err, ErrBuiltInRole) {
				t.Fatalf("DeleteRole built-in error = %v, want ErrBuiltInRole", err)
			}

			work, err := store.CreateWorkItem(ctx, WorkItem{
				ID:              "work_api",
				ProjectID:       "proj_alpha",
				Title:           " Add project work API ",
				Brief:           "Persist work coordination metadata.",
				Priority:        "high",
				OwnerRoleID:     "software_developer",
				RootID:          "root_feature",
				ReviewerRoleIDs: []string{"reviewer_qa", "architect", "reviewer_qa"},
			})
			if err != nil {
				t.Fatalf("CreateWorkItem: %v", err)
			}
			if work.Status != WorkItemStatusBacklog || work.RootID != "root_feature" || len(work.ReviewerRoleIDs) != 2 || work.ReviewerRoleIDs[0] != "architect" {
				t.Fatalf("work item = %+v, want defaults and normalized reviewers", work)
			}

			got, ok, err := store.GetWorkItem(ctx, "proj_alpha", "work_api")
			if err != nil || !ok {
				t.Fatalf("GetWorkItem ok=%v err=%v, want work item", ok, err)
			}
			got.ReviewerRoleIDs[0] = "mutated"
			gotAgain, _, err := store.GetWorkItem(ctx, "proj_alpha", "work_api")
			if err != nil {
				t.Fatalf("GetWorkItem after mutation: %v", err)
			}
			if gotAgain.ReviewerRoleIDs[0] != "architect" {
				t.Fatalf("stored reviewers mutated to %+v", gotAgain.ReviewerRoleIDs)
			}

			updatedWork, err := store.UpdateWorkItem(ctx, "proj_alpha", "work_api", func(item *WorkItem) {
				item.Status = WorkItemStatusReady
				item.RootID = "root_review"
				item.ReviewerRoleIDs = []string{"reviewer_qa"}
			})
			if err != nil {
				t.Fatalf("UpdateWorkItem: %v", err)
			}
			if updatedWork.Status != WorkItemStatusReady || updatedWork.RootID != "root_review" || len(updatedWork.ReviewerRoleIDs) != 1 {
				t.Fatalf("updated work item = %+v, want ready with one reviewer", updatedWork)
			}

			assignment, err := store.CreateAssignment(ctx, Assignment{
				ID:         "asgn_impl",
				ProjectID:  "proj_alpha",
				WorkItemID: "work_api",
				RoleID:     "software_developer",
				RootID:     "root_assignment",
				ExecutionRef: AssignmentExecutionRef{
					Kind:              AssignmentExecutionKindTaskRun,
					TaskID:            "task_123",
					RunID:             "run_123",
					ContextSnapshotID: "ctx_123",
				},
				ContextPacket: []byte(`{"id":"ctx_123","version":"chat.context.v1"}`),
			})
			if err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if assignment.Status != AssignmentStatusQueued || assignment.RootID != "root_assignment" {
				t.Fatalf("assignment status = %q, want queued", assignment.Status)
			}
			if assignment.DriverKind != AssignmentDriverHecateTask {
				t.Fatalf("assignment driver_kind = %q, want hecate_task", assignment.DriverKind)
			}
			if string(assignment.ContextPacket) != `{"id":"ctx_123","version":"chat.context.v1"}` {
				t.Fatalf("assignment context packet = %s, want stored packet", string(assignment.ContextPacket))
			}
			if assignment.ExecutionRef.TaskID != "task_123" || assignment.ExecutionRef.RunID != "run_123" || assignment.ExecutionRef.ContextSnapshotID != "ctx_123" {
				t.Fatalf("assignment execution_ref = %+v, want stored canonical links", assignment.ExecutionRef)
			}

			startedAt := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
			updatedAssignment, err := store.UpdateAssignment(ctx, "proj_alpha", "asgn_impl", func(item *Assignment) {
				item.DriverKind = AssignmentDriverExternalAgent
				item.Status = AssignmentStatusRunning
				item.RootID = "root_external"
				item.StartedAt = startedAt
				item.ContextPacket = []byte(`{"id":"ctx_456","version":"chat.context.v1"}`)
			})
			if err != nil {
				t.Fatalf("UpdateAssignment: %v", err)
			}
			if updatedAssignment.DriverKind != AssignmentDriverExternalAgent || updatedAssignment.Status != AssignmentStatusRunning || updatedAssignment.RootID != "root_external" || !updatedAssignment.StartedAt.Equal(startedAt) {
				t.Fatalf("updated assignment = %+v, want running with start time", updatedAssignment)
			}
			if string(updatedAssignment.ContextPacket) != `{"id":"ctx_456","version":"chat.context.v1"}` {
				t.Fatalf("updated assignment context packet = %s, want updated packet", string(updatedAssignment.ContextPacket))
			}
			if _, err := store.UpdateAssignment(ctx, "proj_alpha", "asgn_impl", func(item *Assignment) {
				item.DriverKind = "unsupported"
			}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpdateAssignment unsupported driver error = %v, want ErrInvalid", err)
			}

			artifact, err := store.CreateArtifact(ctx, CollaborationArtifact{
				ID:                     "art_brief",
				ProjectID:              "proj_alpha",
				WorkItemID:             "work_api",
				AssignmentID:           "asgn_impl",
				Kind:                   ArtifactKindReview,
				Title:                  "Implementation review",
				Body:                   "Changes requested.",
				AuthorRoleID:           "reviewer_qa",
				ReviewedAssignmentID:   "asgn_impl",
				ReviewVerdict:          ReviewVerdictChangesRequested,
				ReviewRisk:             ReviewRiskMedium,
				ReviewFollowUpRequired: true,
			})
			if err != nil {
				t.Fatalf("CreateArtifact: %v", err)
			}
			if artifact.Kind != ArtifactKindReview || artifact.ReviewedAssignmentID != "asgn_impl" || artifact.ReviewVerdict != ReviewVerdictChangesRequested || artifact.ReviewRisk != ReviewRiskMedium || !artifact.ReviewFollowUpRequired {
				t.Fatalf("artifact = %+v, want structured review artifact", artifact)
			}
			briefArtifact, err := store.CreateArtifact(ctx, CollaborationArtifact{
				ID:                     "art_note",
				ProjectID:              "proj_alpha",
				WorkItemID:             "work_api",
				AssignmentID:           "asgn_impl",
				Kind:                   ArtifactKindBrief,
				Title:                  "Implementation note",
				Body:                   "This is not a review.",
				ReviewedAssignmentID:   "asgn_impl",
				ReviewVerdict:          ReviewVerdictBlocked,
				ReviewRisk:             ReviewRiskHigh,
				ReviewFollowUpRequired: true,
			})
			if err != nil {
				t.Fatalf("CreateArtifact non-review with review fields: %v", err)
			}
			if briefArtifact.ReviewedAssignmentID != "" || briefArtifact.ReviewVerdict != "" || briefArtifact.ReviewRisk != "" || briefArtifact.ReviewFollowUpRequired {
				t.Fatalf("non-review artifact = %+v, want review fields cleared", briefArtifact)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_bad_verdict", ProjectID: "proj_alpha", WorkItemID: "work_api", AssignmentID: "asgn_impl", Kind: ArtifactKindReview, Body: "Invalid verdict.", ReviewVerdict: "ship_it"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateArtifact invalid review_verdict error = %v, want ErrInvalid", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_bad_risk", ProjectID: "proj_alpha", WorkItemID: "work_api", AssignmentID: "asgn_impl", Kind: ArtifactKindReview, Body: "Invalid risk.", ReviewRisk: "spicy"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("CreateArtifact invalid review_risk error = %v, want ErrInvalid", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_missing_reviewed", ProjectID: "proj_alpha", WorkItemID: "work_api", AssignmentID: "asgn_impl", Kind: ArtifactKindReview, Body: "Missing reviewed assignment.", ReviewedAssignmentID: "asgn_missing"}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("CreateArtifact missing reviewed_assignment_id error = %v, want ErrNotFound", err)
			}
			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_other", ProjectID: "proj_alpha", Title: "Other work"}); err != nil {
				t.Fatalf("CreateWorkItem other: %v", err)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_other", ProjectID: "proj_alpha", WorkItemID: "work_other", RoleID: "software_developer"}); err != nil {
				t.Fatalf("CreateAssignment other: %v", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_cross_reviewed", ProjectID: "proj_alpha", WorkItemID: "work_api", AssignmentID: "asgn_impl", Kind: ArtifactKindReview, Body: "Cross-work reviewed assignment.", ReviewedAssignmentID: "asgn_other"}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("CreateArtifact cross-work reviewed_assignment_id error = %v, want ErrNotFound", err)
			}
			if err := store.DeleteWorkItem(ctx, "proj_alpha", "work_other"); err != nil {
				t.Fatalf("DeleteWorkItem other: %v", err)
			}

			handoff, err := store.CreateHandoff(ctx, Handoff{
				ID:                    "handoff_impl",
				ProjectID:             "proj_alpha",
				WorkItemID:            "work_api",
				SourceAssignmentID:    "asgn_impl",
				SourceRunID:           "run_123",
				TargetRoleID:          "reviewer_qa",
				Title:                 "Implementation handoff",
				Summary:               "Backend substrate is ready for review.",
				RecommendedNextAction: "Review the API and storage behavior.",
				LinkedArtifactIDs:     []string{"art_brief", "art_brief"},
				LinkedMemoryIDs:       []string{"mem_project"},
				ContextRefs:           []string{"ctx_123"},
				ProvenanceKind:        "agent_draft",
				TrustLabel:            "operator_reviewed",
				CreatedByRoleID:       "software_developer",
			})
			if err != nil {
				t.Fatalf("CreateHandoff: %v", err)
			}
			if handoff.Status != HandoffStatusPending || len(handoff.LinkedArtifactIDs) != 1 || handoff.ProvenanceKind != "agent_draft" {
				t.Fatalf("handoff = %+v, want pending normalized handoff", handoff)
			}
			updatedHandoff, err := store.UpdateHandoff(ctx, "proj_alpha", "work_api", "handoff_impl", func(item *Handoff) {
				item.Status = HandoffStatusAccepted
				item.TargetAssignmentID = "asgn_impl"
			})
			if err != nil {
				t.Fatalf("UpdateHandoff: %v", err)
			}
			if updatedHandoff.Status != HandoffStatusAccepted || updatedHandoff.TargetAssignmentID != "asgn_impl" || updatedHandoff.StatusChangedAt.IsZero() {
				t.Fatalf("updated handoff = %+v, want accepted linked handoff", updatedHandoff)
			}
			if _, err := store.UpdateHandoff(ctx, "proj_alpha", "work_api", "handoff_impl", func(item *Handoff) {
				item.Status = "unsupported"
			}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("UpdateHandoff unsupported status error = %v, want ErrInvalid", err)
			}

			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_api", ProjectID: "proj_alpha", Title: "Duplicate"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateWorkItem error = %v, want ErrDuplicate", err)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_impl", ProjectID: "proj_alpha", WorkItemID: "work_api", RoleID: "software_developer"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateAssignment error = %v, want ErrDuplicate", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_brief", ProjectID: "proj_alpha", WorkItemID: "work_api", Kind: ArtifactKindBrief, Body: "Duplicate"}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateArtifact error = %v, want ErrDuplicate", err)
			}
			if _, err := store.CreateHandoff(ctx, Handoff{ID: "handoff_impl", ProjectID: "proj_alpha", WorkItemID: "work_api", Title: "Duplicate", Summary: "Duplicate.", RecommendedNextAction: "Ignore."}); !errors.Is(err, ErrDuplicate) {
				t.Fatalf("duplicate CreateHandoff error = %v, want ErrDuplicate", err)
			}

			assignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListAssignments: %v", err)
			}
			if len(assignments) != 1 || assignments[0].ID != "asgn_impl" {
				t.Fatalf("assignments = %+v, want created assignment", assignments)
			}
			artifacts, err := store.ListArtifacts(ctx, ArtifactFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListArtifacts: %v", err)
			}
			if len(artifacts) != 2 || artifacts[0].ID != "art_brief" || artifacts[1].ID != "art_note" {
				t.Fatalf("artifacts = %+v, want created review and note artifacts", artifacts)
			}
			if artifacts[0].ReviewVerdict != ReviewVerdictChangesRequested || artifacts[0].ReviewRisk != ReviewRiskMedium || !artifacts[0].ReviewFollowUpRequired {
				t.Fatalf("listed review artifact = %+v, want structured review fields", artifacts[0])
			}
			if artifacts[1].ReviewedAssignmentID != "" || artifacts[1].ReviewVerdict != "" || artifacts[1].ReviewRisk != "" || artifacts[1].ReviewFollowUpRequired {
				t.Fatalf("listed non-review artifact = %+v, want review fields cleared", artifacts[1])
			}
			handoffs, err := store.ListHandoffs(ctx, HandoffFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListHandoffs: %v", err)
			}
			if len(handoffs) != 1 || handoffs[0].ID != "handoff_impl" {
				t.Fatalf("handoffs = %+v, want created handoff", handoffs)
			}

			if err := store.DeleteAssignment(ctx, "proj_alpha", "work_api", "asgn_impl"); err != nil {
				t.Fatalf("DeleteAssignment: %v", err)
			}
			assignments, err = store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListAssignments after assignment delete: %v", err)
			}
			artifacts, err = store.ListArtifacts(ctx, ArtifactFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListArtifacts after assignment delete: %v", err)
			}
			handoffs, err = store.ListHandoffs(ctx, HandoffFilter{ProjectID: "proj_alpha", WorkItemID: "work_api"})
			if err != nil {
				t.Fatalf("ListHandoffs after assignment delete: %v", err)
			}
			if len(assignments) != 0 || len(artifacts) != 0 || len(handoffs) != 0 {
				t.Fatalf("assignment delete left assignments=%+v artifacts=%+v handoffs=%+v", assignments, artifacts, handoffs)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_impl", ProjectID: "proj_alpha", WorkItemID: "work_api", RoleID: "software_developer"}); err != nil {
				t.Fatalf("recreate assignment after delete: %v", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_brief", ProjectID: "proj_alpha", WorkItemID: "work_api", AssignmentID: "asgn_impl", Kind: ArtifactKindBrief, Body: "Brief again."}); err != nil {
				t.Fatalf("recreate artifact after delete: %v", err)
			}
			if _, err := store.CreateHandoff(ctx, Handoff{ID: "handoff_impl", ProjectID: "proj_alpha", WorkItemID: "work_api", SourceAssignmentID: "asgn_impl", Title: "Handoff again", Summary: "Ready again.", RecommendedNextAction: "Review again."}); err != nil {
				t.Fatalf("recreate handoff after delete: %v", err)
			}

			if err := store.DeleteWorkItem(ctx, "proj_alpha", "work_api"); err != nil {
				t.Fatalf("DeleteWorkItem: %v", err)
			}
			assignments, err = store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"})
			if err != nil {
				t.Fatalf("ListAssignments after delete: %v", err)
			}
			artifacts, err = store.ListArtifacts(ctx, ArtifactFilter{ProjectID: "proj_alpha"})
			if err != nil {
				t.Fatalf("ListArtifacts after delete: %v", err)
			}
			handoffs, err = store.ListHandoffs(ctx, HandoffFilter{ProjectID: "proj_alpha"})
			if err != nil {
				t.Fatalf("ListHandoffs after delete: %v", err)
			}
			if len(assignments) != 0 || len(artifacts) != 0 || len(handoffs) != 0 {
				t.Fatalf("work item delete left assignments=%+v artifacts=%+v handoffs=%+v", assignments, artifacts, handoffs)
			}
		})
	}
}

func TestStoreConformance_ProjectCleanup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)
			if _, err := store.CreateRole(ctx, AgentRoleProfile{ID: "role_custom", ProjectID: "proj_alpha", Name: "Custom"}); err != nil {
				t.Fatalf("CreateRole: %v", err)
			}
			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_alpha", ProjectID: "proj_alpha", Title: "Alpha"}); err != nil {
				t.Fatalf("CreateWorkItem alpha: %v", err)
			}
			if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_beta", ProjectID: "proj_beta", Title: "Beta"}); err != nil {
				t.Fatalf("CreateWorkItem beta: %v", err)
			}
			if _, err := store.CreateAssignment(ctx, Assignment{ID: "asgn_alpha", ProjectID: "proj_alpha", WorkItemID: "work_alpha", RoleID: "software_developer"}); err != nil {
				t.Fatalf("CreateAssignment: %v", err)
			}
			if _, err := store.CreateArtifact(ctx, CollaborationArtifact{ID: "art_alpha", ProjectID: "proj_alpha", WorkItemID: "work_alpha", Kind: ArtifactKindDecisionNote, Body: "Ship it."}); err != nil {
				t.Fatalf("CreateArtifact: %v", err)
			}

			deleted, err := store.DeleteProject(ctx, "proj_alpha")
			if err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}
			if deleted != 4 {
				t.Fatalf("DeleteProject deleted = %d, want 4", deleted)
			}
			if roles, err := store.ListRoles(ctx, "proj_alpha"); err != nil || roleIDExists(roles, "role_custom") {
				t.Fatalf("roles after project delete = %+v err=%v, want only built-ins", roles, err)
			}
			if items, err := store.ListWorkItems(ctx, "proj_alpha"); err != nil || len(items) != 0 {
				t.Fatalf("alpha work items after delete = %+v err=%v, want none", items, err)
			}
			if items, err := store.ListWorkItems(ctx, "proj_beta"); err != nil || len(items) != 1 {
				t.Fatalf("beta work items after alpha delete = %+v err=%v, want retained", items, err)
			}

			cleared, err := store.Clear(ctx)
			if err != nil {
				t.Fatalf("Clear: %v", err)
			}
			if cleared != 1 {
				t.Fatalf("Clear deleted = %d, want remaining beta work item", cleared)
			}
		})
	}
}

func TestStoreConformance_ListOptionsLimitAndStatuses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(t *testing.T) Store { return NewMemoryStore() }},
		{name: "sqlite", new: func(t *testing.T) Store { return newSQLiteTestStore(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := tc.new(t)
			for _, item := range []WorkItem{
				{ID: "work_backlog", ProjectID: "proj_alpha", Title: "Backlog", Status: WorkItemStatusBacklog},
				{ID: "work_ready", ProjectID: "proj_alpha", Title: "Ready", Status: WorkItemStatusReady},
				{ID: "work_done", ProjectID: "proj_alpha", Title: "Done", Status: WorkItemStatusDone},
			} {
				if _, err := store.CreateWorkItem(ctx, item); err != nil {
					t.Fatalf("CreateWorkItem(%s): %v", item.ID, err)
				}
			}
			activeWork, err := store.ListWorkItems(ctx, "proj_alpha", ListOptions{
				Statuses: []string{WorkItemStatusReady, WorkItemStatusBacklog},
			})
			if err != nil {
				t.Fatalf("ListWorkItems(active): %v", err)
			}
			if len(activeWork) != 2 || workItemIDExists(activeWork, "work_done") {
				t.Fatalf("active work items = %+v, want ready/backlog only", activeWork)
			}
			limitedWork, err := store.ListWorkItems(ctx, "proj_alpha", ListOptions{
				Statuses: []string{WorkItemStatusReady, WorkItemStatusBacklog},
				Limit:    1,
			})
			if err != nil {
				t.Fatalf("ListWorkItems(limited): %v", err)
			}
			if len(limitedWork) != 1 || limitedWork[0].Status == WorkItemStatusDone {
				t.Fatalf("limited work items = %+v, want one active item", limitedWork)
			}

			for _, assignment := range []Assignment{
				{ID: "asgn_queued", ProjectID: "proj_alpha", WorkItemID: "work_ready", RoleID: "architect", Status: AssignmentStatusQueued},
				{ID: "asgn_running", ProjectID: "proj_alpha", WorkItemID: "work_ready", RoleID: "software_developer", Status: AssignmentStatusRunning},
				{ID: "asgn_completed", ProjectID: "proj_alpha", WorkItemID: "work_ready", RoleID: "reviewer_qa", Status: AssignmentStatusCompleted},
			} {
				if _, err := store.CreateAssignment(ctx, assignment); err != nil {
					t.Fatalf("CreateAssignment(%s): %v", assignment.ID, err)
				}
			}
			activeAssignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"}, ListOptions{
				Statuses: []string{AssignmentStatusQueued, AssignmentStatusRunning},
			})
			if err != nil {
				t.Fatalf("ListAssignments(active): %v", err)
			}
			if len(activeAssignments) != 2 || assignmentIDExists(activeAssignments, "asgn_completed") {
				t.Fatalf("active assignments = %+v, want queued/running only", activeAssignments)
			}
			limitedAssignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"}, ListOptions{
				Statuses: []string{AssignmentStatusQueued, AssignmentStatusRunning},
				Limit:    1,
			})
			if err != nil {
				t.Fatalf("ListAssignments(limited): %v", err)
			}
			if len(limitedAssignments) != 1 || limitedAssignments[0].Status == AssignmentStatusCompleted {
				t.Fatalf("limited assignments = %+v, want one active assignment", limitedAssignments)
			}
		})
	}
}

func TestMemoryStore_UpdateRejectsIDChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateWorkItem(ctx, WorkItem{ID: "work_alpha", ProjectID: "proj_alpha", Title: "Alpha"}); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if _, err := store.UpdateWorkItem(ctx, "proj_alpha", "work_alpha", func(item *WorkItem) {
		item.ID = "work_beta"
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateWorkItem id change error = %v, want ErrInvalid", err)
	}
}

func TestSQLiteStore_AddsAssignmentDriverKindToExistingTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectwork.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	assignmentsTbl := client.QualifiedTable("project_work_assignments")
	if _, err := client.DB().ExecContext(ctx, `
CREATE TABLE `+assignmentsTbl+` (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	work_item_id TEXT NOT NULL,
	role_id TEXT NOT NULL,
	status TEXT NOT NULL,
	task_id TEXT NOT NULL DEFAULT '',
	run_id TEXT NOT NULL DEFAULT '',
	chat_session_id TEXT NOT NULL DEFAULT '',
	message_id TEXT NOT NULL DEFAULT '',
	context_snapshot_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	started_at TEXT NOT NULL DEFAULT '',
	completed_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY(project_id, id)
)`); err != nil {
		t.Fatalf("create legacy assignments table: %v", err)
	}
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := client.DB().ExecContext(ctx, `
INSERT INTO `+assignmentsTbl+` (
	id, project_id, work_item_id, role_id, status, task_id, run_id, context_snapshot_id, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"asgn_legacy", "proj_alpha", "work_alpha", "software_developer", AssignmentStatusQueued, "task_legacy", "run_legacy", "ctx_legacy", now, now,
	); err != nil {
		t.Fatalf("insert legacy assignment: %v", err)
	}

	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	assignments, err := store.ListAssignments(ctx, AssignmentFilter{ProjectID: "proj_alpha"})
	if err != nil {
		t.Fatalf("ListAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].DriverKind != AssignmentDriverHecateTask {
		t.Fatalf("assignments = %+v, want legacy assignment backfilled to hecate_task", assignments)
	}
	if assignments[0].ExecutionRef.TaskID != "task_legacy" || assignments[0].ExecutionRef.RunID != "run_legacy" || assignments[0].ExecutionRef.ContextSnapshotID != "ctx_legacy" {
		t.Fatalf("assignment execution_ref = %+v, want legacy columns folded into canonical ref", assignments[0].ExecutionRef)
	}
}

func TestSQLiteStore_AddsRoleDefaultColumnsToExistingTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, err := storage.NewSQLiteClient(ctx, storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectwork.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	rolesTbl := client.QualifiedTable("project_work_roles")
	if _, err := client.DB().ExecContext(ctx, `
CREATE TABLE `+rolesTbl+` (
	id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	instructions TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(project_id, id)
)`); err != nil {
		t.Fatalf("create legacy roles table: %v", err)
	}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := client.DB().ExecContext(ctx, `
INSERT INTO `+rolesTbl+` (
	id, project_id, name, description, instructions, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"role_legacy", "proj_alpha", "Legacy role", "Older schema", "Keep going.", now, now,
	); err != nil {
		t.Fatalf("insert legacy role: %v", err)
	}

	store, err := NewSQLiteStore(ctx, client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	roles, err := store.ListRoles(ctx, "proj_alpha")
	if err != nil {
		t.Fatalf("ListRoles: %v", err)
	}
	var legacy AgentRoleProfile
	for _, role := range roles {
		if role.ID == "role_legacy" {
			legacy = role
			break
		}
	}
	if legacy.ID == "" {
		t.Fatalf("roles = %+v, want legacy role", roles)
	}
	if legacy.DefaultDriverKind != "" || legacy.DefaultProvider != "" || legacy.DefaultModel != "" || legacy.DefaultAgentProfile != "" {
		t.Fatalf("legacy role defaults = %+v, want empty migrated defaults", legacy)
	}
}

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	client, err := storage.NewSQLiteClient(context.Background(), storage.SQLiteConfig{
		Path:        filepath.Join(t.TempDir(), "projectwork.db"),
		TablePrefix: "test",
	})
	if err != nil {
		t.Fatalf("NewSQLiteClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	store, err := NewSQLiteStore(context.Background(), client)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	return store
}

func roleIDExists(roles []AgentRoleProfile, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func workItemIDExists(items []WorkItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func assignmentIDExists(items []Assignment, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
