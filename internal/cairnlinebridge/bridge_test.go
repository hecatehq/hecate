package cairnlinebridge

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestSeedMirrorsProjectWorkIntoCairnline(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	snapshot := bridgeSnapshotFixture(now)

	if err := Seed(ctx, service, snapshot); err != nil {
		t.Fatalf("Seed() error = %v", err)
	}

	packet, err := service.AssignmentLaunchPacket(ctx, "proj_hecate", "asgn_bridge")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() error = %v", err)
	}
	if packet.Project.ID != "proj_hecate" || packet.WorkItem.RootID != "root_main" {
		t.Fatalf("packet project/work = %+v/%+v, want Hecate project with root-scoped work", packet.Project, packet.WorkItem)
	}
	if packet.Role == nil || packet.Role.DefaultExecutionMode != cairnline.ExecutionOrchestrated {
		t.Fatalf("packet role = %+v, want orchestrated role from Hecate task driver", packet.Role)
	}
	if packet.Profile == nil || packet.Profile.ID != "bridge_implementation" || packet.Profile.MemoryPolicy != agentprofiles.MemoryInclude {
		t.Fatalf("packet profile = %+v, want mapped Hecate agent profile", packet.Profile)
	}
	if packet.ExecutionProfile == nil || packet.ExecutionProfile.AgentKind != "hecate" || packet.ExecutionProfile.WritesPolicy != "allow" || packet.ExecutionProfile.NetworkPolicy != "block" {
		t.Fatalf("packet execution profile = %+v, want mapped Hecate execution posture", packet.ExecutionProfile)
	}
	if packet.Assignment.ExecutionMode != cairnline.ExecutionOrchestrated || packet.Assignment.RootID != "root_main" || packet.Assignment.ContextSnapshotID != "ctx_123" {
		t.Fatalf("packet assignment = %+v, want orchestrated root-scoped assignment", packet.Assignment)
	}
	if len(packet.Skills) != 1 || packet.Skills[0].ID != "backend" || len(packet.Skills[0].SourceRefs) != 1 {
		t.Fatalf("packet skills = %+v, want mapped backend skill with provenance", packet.Skills)
	}
	if len(packet.Evidence) != 1 || packet.Evidence[0].ID != "art_evidence" || packet.Evidence[0].Locator != "https://github.com/hecatehq/hecate/actions/runs/123" {
		t.Fatalf("packet evidence = %+v, want mapped evidence link", packet.Evidence)
	}
	if len(packet.Reviews) != 1 || packet.Reviews[0].ID != "art_review" || packet.Reviews[0].AssignmentID != "asgn_external" || packet.Reviews[0].Verdict != cairnline.ReviewVerdictConcerns || packet.Reviews[0].Risk != cairnline.ReviewRiskMedium {
		t.Fatalf("packet reviews = %+v, want mapped review with reduced verdict", packet.Reviews)
	}
	if len(packet.Handoffs) != 1 || packet.Handoffs[0].ID != "handoff_review" || packet.Handoffs[0].FromRoleID != "bridge_developer" || packet.Handoffs[0].ToRoleID != "bridge_reviewer" {
		t.Fatalf("packet handoffs = %+v, want mapped handoff roles", packet.Handoffs)
	}
	if len(packet.Memory) != 1 || packet.Memory[0].ID != "mem_bridge" || packet.Memory[0].TrustLabel != memory.TrustLabelOperatorMemory {
		t.Fatalf("packet memory = %+v, want mapped accepted memory", packet.Memory)
	}
	if len(packet.MemoryCandidates) != 1 || packet.MemoryCandidates[0].ID != "memcand_bridge" || packet.MemoryCandidates[0].SuggestedTrustLabel != memory.TrustLabelGenerated || packet.MemoryCandidates[0].SuggestedSourceID != "handoff_review" || len(packet.MemoryCandidates[0].SourceRefs) != 1 {
		t.Fatalf("packet memory candidates = %+v, want mapped memory candidate provenance", packet.MemoryCandidates)
	}
	candidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{ProjectID: "proj_hecate", IncludeResolved: true})
	if err != nil {
		t.Fatalf("ListMemoryCandidates(include resolved) error = %v", err)
	}
	rejectedCandidate := findCairnlineMemoryCandidate(candidates, "memcand_rejected")
	if len(candidates) != 2 || rejectedCandidate == nil || rejectedCandidate.Status != cairnline.MemoryCandidateRejected || rejectedCandidate.StatusReason != "Already captured" {
		t.Fatalf("all memory candidates = %+v, want pending and rejected state preserved", candidates)
	}

	externalPacket, err := service.AssignmentLaunchPacket(ctx, "proj_hecate", "asgn_external")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() external error = %v", err)
	}
	if externalPacket.Assignment.Status != cairnline.AssignmentCompleted || externalPacket.Assignment.ExecutionMode != cairnline.ExecutionExternalAdapter || externalPacket.Assignment.ExecutionRef != "chat_123" {
		t.Fatalf("external assignment = %+v, want completed external-adapter assignment", externalPacket.Assignment)
	}
}

func TestLoadSnapshotReadsHecateStores(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	sources := bridgeMemorySources()
	snapshot := bridgeSnapshotFixture(now)
	seedHecateSources(t, ctx, sources, snapshot)

	loaded, err := LoadSnapshot(ctx, sources, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("LoadSnapshot() error = %v", err)
	}
	if loaded.Project.ID != snapshot.Project.ID {
		t.Fatalf("loaded project = %+v, want %q", loaded.Project, snapshot.Project.ID)
	}
	if !hasAgentProfile(loaded.AgentProfiles, "bridge_implementation") {
		t.Fatalf("loaded profiles missing bridge_implementation: %+v", loaded.AgentProfiles)
	}
	if !hasRole(loaded.Roles, "bridge_developer") || !hasRole(loaded.Roles, "bridge_reviewer") {
		t.Fatalf("loaded roles missing bridge roles: %+v", loaded.Roles)
	}
	if len(loaded.Skills) != len(snapshot.Skills) || len(loaded.WorkItems) != len(snapshot.WorkItems) || len(loaded.Assignments) != len(snapshot.Assignments) {
		t.Fatalf("loaded counts skills=%d work=%d assignments=%d, want %d/%d/%d", len(loaded.Skills), len(loaded.WorkItems), len(loaded.Assignments), len(snapshot.Skills), len(snapshot.WorkItems), len(snapshot.Assignments))
	}
	if len(loaded.Artifacts) != len(snapshot.Artifacts) || len(loaded.Handoffs) != len(snapshot.Handoffs) || len(loaded.MemoryEntries) != len(snapshot.MemoryEntries) || len(loaded.MemoryCandidates) != len(snapshot.MemoryCandidates) {
		t.Fatalf("loaded collaboration counts artifacts=%d handoffs=%d memory_entries=%d memory_candidates=%d, want %d/%d/%d/%d", len(loaded.Artifacts), len(loaded.Handoffs), len(loaded.MemoryEntries), len(loaded.MemoryCandidates), len(snapshot.Artifacts), len(snapshot.Handoffs), len(snapshot.MemoryEntries), len(snapshot.MemoryCandidates))
	}

	service := cairnline.NewMemoryService()
	if err := Seed(ctx, service, loaded); err != nil {
		t.Fatalf("Seed() loaded snapshot error = %v", err)
	}
	packet, err := service.AssignmentLaunchPacket(ctx, snapshot.Project.ID, "asgn_bridge")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() loaded snapshot error = %v", err)
	}
	if len(packet.Evidence) != 1 || len(packet.Reviews) != 1 || len(packet.Handoffs) != 1 || len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
		t.Fatalf("loaded launch packet collaboration counts evidence=%d reviews=%d handoffs=%d memory_entries=%d memory_candidates=%d, want all one", len(packet.Evidence), len(packet.Reviews), len(packet.Handoffs), len(packet.Memory), len(packet.MemoryCandidates))
	}
}

func TestSeedProjectFromStoresPersistsToCairnlineSQLite(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	sources := bridgeMemorySources()
	snapshot := bridgeSnapshotFixture(now)
	seedHecateSources(t, ctx, sources, snapshot)
	dbPath := filepath.Join(t.TempDir(), "cairnline.db")

	service, store, err := cairnline.NewSQLiteService(ctx, dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteService() error = %v", err)
	}
	if _, err := SeedProjectFromStores(ctx, service, sources, snapshot.Project.ID); err != nil {
		t.Fatalf("SeedProjectFromStores() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close seeded store: %v", err)
	}

	reopened, reopenedStore, err := cairnline.NewSQLiteService(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen NewSQLiteService() error = %v", err)
	}
	defer reopenedStore.Close()
	packet, err := reopened.AssignmentLaunchPacket(ctx, snapshot.Project.ID, "asgn_bridge")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() reopened error = %v", err)
	}
	if packet.Project.ID != snapshot.Project.ID || packet.Assignment.RootID != "root_main" {
		t.Fatalf("reopened packet project/assignment = %+v/%+v, want persisted root-scoped launch packet", packet.Project, packet.Assignment)
	}
	if len(packet.Evidence) != 1 || len(packet.Reviews) != 1 || len(packet.Handoffs) != 1 || len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
		t.Fatalf("reopened launch packet collaboration counts evidence=%d reviews=%d handoffs=%d memory_entries=%d memory_candidates=%d, want all one", len(packet.Evidence), len(packet.Reviews), len(packet.Handoffs), len(packet.Memory), len(packet.MemoryCandidates))
	}
}

func TestExecutionModeMapsHecateDrivers(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		want   string
	}{
		{name: "hecate task", driver: projectwork.AssignmentDriverHecateTask, want: cairnline.ExecutionOrchestrated},
		{name: "external agent", driver: projectwork.AssignmentDriverExternalAgent, want: cairnline.ExecutionExternalAdapter},
		{name: "unspecified", driver: "", want: cairnline.ExecutionMCPPull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExecutionMode(tt.driver); got != tt.want {
				t.Fatalf("ExecutionMode(%q) = %q, want %q", tt.driver, got, tt.want)
			}
		})
	}
}

func bridgeSnapshotFixture(now time.Time) Snapshot {
	return Snapshot{
		Project: projects.Project{
			ID:          "proj_hecate",
			Name:        "Hecate",
			Description: "Local AI operations console.",
			Roots: []projects.Root{{
				ID:        "root_main",
				Path:      "/Users/alice/dev/hecate",
				Kind:      "local",
				GitRemote: "git@github.com:hecatehq/hecate.git",
				GitBranch: "main",
				Active:    true,
			}},
			ContextSources: []projects.ContextSource{{
				ID:         "src_agents",
				Kind:       "workspace_instruction",
				Title:      "AGENTS.md",
				Path:       "AGENTS.md",
				Enabled:    true,
				TrustLabel: "workspace_guidance",
			}},
			CreatedAt: now,
			UpdatedAt: now,
		},
		AgentProfiles: []agentprofiles.Profile{{
			ID:                  "bridge_implementation",
			Name:                "Bridge Implementation",
			Description:         "Implement scoped changes with tests.",
			Instructions:        "Prefer small, reviewable changes.",
			Surface:             agentprofiles.SurfaceHecateTask,
			ProviderHint:        "ollama",
			ModelHint:           "qwen3-coder",
			ToolsEnabled:        true,
			WritesAllowed:       true,
			NetworkAllowed:      false,
			ApprovalPolicy:      agentprofiles.ApprovalRequire,
			ProjectMemoryPolicy: agentprofiles.MemoryInclude,
			ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
			SkillIDs:            []string{"backend"},
			CreatedAt:           now,
			UpdatedAt:           now,
		}},
		Skills: []projectskills.Skill{{
			ID:                     "backend",
			ProjectID:              "proj_hecate",
			Title:                  "Backend",
			Description:            "Backend implementation guidance.",
			Path:                   "docs-ai/skills/backend/SKILL.md",
			RootID:                 "root_main",
			Format:                 projectskills.FormatSkillMD,
			Enabled:                true,
			Status:                 projectskills.StatusAvailable,
			TrustLabel:             projectskills.TrustWorkspaceSkill,
			SourceContextSourceIDs: []string{"src_agents"},
			CreatedAt:              now,
			UpdatedAt:              now,
		}},
		Roles: []projectwork.AgentRoleProfile{{
			ID:                  "bridge_developer",
			ProjectID:           "proj_hecate",
			Name:                "Software Developer",
			Description:         "Implements backend and shared behavior.",
			Instructions:        "Keep handlers thin.",
			DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
			DefaultAgentProfile: "bridge_implementation",
			SkillIDs:            []string{"backend", "backend"},
			CreatedAt:           now,
			UpdatedAt:           now,
		}, {
			ID:                  "bridge_reviewer",
			ProjectID:           "proj_hecate",
			Name:                "Reviewer QA",
			Description:         "Reviews behavior, risks, and verification gaps.",
			Instructions:        "Prioritize concrete defects.",
			DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
			DefaultAgentProfile: "bridge_implementation",
			SkillIDs:            []string{"backend"},
			CreatedAt:           now,
			UpdatedAt:           now,
		}},
		WorkItems: []projectwork.WorkItem{{
			ID:              "work_bridge",
			ProjectID:       "proj_hecate",
			Title:           "Bridge Cairnline",
			Brief:           "Prove Hecate can seed Cairnline coordination state.",
			Status:          projectwork.WorkItemStatusReady,
			Priority:        "normal",
			OwnerRoleID:     "bridge_developer",
			RootID:          "root_main",
			ReviewerRoleIDs: []string{"bridge_reviewer"},
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
		Assignments: []projectwork.Assignment{{
			ID:         "asgn_bridge",
			ProjectID:  "proj_hecate",
			WorkItemID: "work_bridge",
			RoleID:     "bridge_developer",
			RootID:     "root_main",
			DriverKind: projectwork.AssignmentDriverHecateTask,
			Status:     projectwork.AssignmentStatusQueued,
			ExecutionRef: projectwork.AssignmentExecutionRef{
				ContextSnapshotID: "ctx_123",
			},
			CreatedAt: now,
			UpdatedAt: now,
		}, {
			ID:         "asgn_external",
			ProjectID:  "proj_hecate",
			WorkItemID: "work_bridge",
			RoleID:     "bridge_developer",
			RootID:     "root_main",
			DriverKind: projectwork.AssignmentDriverExternalAgent,
			Status:     projectwork.AssignmentStatusCompleted,
			ExecutionRef: projectwork.AssignmentExecutionRef{
				ChatSessionID: "chat_123",
			},
			CreatedAt: now,
			UpdatedAt: now,
		}},
		Artifacts: []projectwork.CollaborationArtifact{{
			ID:                 "art_evidence",
			ProjectID:          "proj_hecate",
			WorkItemID:         "work_bridge",
			AssignmentID:       "asgn_external",
			Kind:               projectwork.ArtifactKindEvidenceLink,
			Title:              "CI run",
			Body:               "Focused bridge tests passed.",
			EvidenceURL:        "https://github.com/hecatehq/hecate/actions/runs/123",
			EvidenceProvider:   "github",
			EvidenceTrustLabel: projectwork.EvidenceTrustOperatorProvided,
			CreatedAt:          now,
			UpdatedAt:          now,
		}, {
			ID:                   "art_review",
			ProjectID:            "proj_hecate",
			WorkItemID:           "work_bridge",
			Kind:                 projectwork.ArtifactKindReview,
			Title:                "Bridge review",
			Body:                 "Needs one follow-up around artifact parity.",
			AuthorRoleID:         "bridge_reviewer",
			ReviewedAssignmentID: "asgn_external",
			ReviewVerdict:        projectwork.ReviewVerdictChangesRequested,
			ReviewRisk:           projectwork.ReviewRiskMedium,
			CreatedAt:            now,
			UpdatedAt:            now,
		}},
		Handoffs: []projectwork.Handoff{{
			ID:                    "handoff_review",
			ProjectID:             "proj_hecate",
			WorkItemID:            "work_bridge",
			SourceAssignmentID:    "asgn_external",
			TargetRoleID:          "bridge_reviewer",
			Title:                 "Review bridge parity",
			Summary:               "Artifact parity is ready for review.",
			RecommendedNextAction: "Verify launch packets include evidence, reviews, handoffs, and memory candidates.",
			LinkedArtifactIDs:     []string{"art_evidence", "art_review"},
			LinkedMemoryIDs:       []string{"memcand_bridge"},
			ContextRefs:           []string{"ctx_123"},
			Status:                projectwork.HandoffStatusPending,
			CreatedByRoleID:       "bridge_developer",
			CreatedAt:             now,
			UpdatedAt:             now,
		}},
		MemoryEntries: []memory.Entry{{
			ID:         "mem_bridge",
			Scope:      memory.ScopeProject,
			ProjectID:  "proj_hecate",
			Title:      "Bridge replacement gate",
			Body:       "Cairnline replacement requires artifact parity before backend migration.",
			TrustLabel: memory.TrustLabelOperatorMemory,
			SourceKind: memory.SourceKindOperator,
			SourceID:   "handoff_review",
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		}},
		MemoryCandidates: []memory.Candidate{{
			ID:                  "memcand_bridge",
			ProjectID:           "proj_hecate",
			Title:               "Bridge replacement gate",
			Body:                "Cairnline replacement requires artifact parity before backend migration.",
			SuggestedKind:       "coordination_note",
			SuggestedTrustLabel: memory.TrustLabelGenerated,
			SuggestedSourceKind: "handoff",
			SuggestedSourceID:   "handoff_review",
			SourceRefs: []memory.CandidateSourceRef{{
				Kind:  "handoff",
				ID:    "handoff_review",
				Title: "Review bridge parity",
			}},
			Status:    memory.CandidateStatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		}, {
			ID:                  "memcand_rejected",
			ProjectID:           "proj_hecate",
			Title:               "Rejected bridge note",
			Body:                "This suggestion was already captured.",
			SuggestedKind:       "coordination_note",
			SuggestedTrustLabel: memory.TrustLabelGenerated,
			SuggestedSourceKind: memory.SourceKindGenerated,
			Status:              memory.CandidateStatusRejected,
			StatusReason:        "Already captured",
			CreatedAt:           now,
			UpdatedAt:           now,
		}},
	}
}

func bridgeMemorySources() SnapshotSources {
	memoryStore := memory.NewMemoryStore()
	return SnapshotSources{
		Projects:         projects.NewMemoryStore(),
		AgentProfiles:    agentprofiles.NewMemoryStore(),
		Skills:           projectskills.NewMemoryStore(),
		Work:             projectwork.NewMemoryStore(),
		Memory:           memoryStore,
		MemoryCandidates: memoryStore,
	}
}

func seedHecateSources(t *testing.T, ctx context.Context, sources SnapshotSources, snapshot Snapshot) {
	t.Helper()
	if _, err := sources.Projects.Create(ctx, snapshot.Project); err != nil {
		t.Fatalf("Create project: %v", err)
	}
	for _, profile := range snapshot.AgentProfiles {
		if _, err := sources.AgentProfiles.Create(ctx, profile); err != nil {
			t.Fatalf("Create profile %q: %v", profile.ID, err)
		}
	}
	if _, err := sources.Skills.UpsertDiscovered(ctx, snapshot.Project.ID, snapshot.Skills); err != nil {
		t.Fatalf("Upsert skills: %v", err)
	}
	for _, role := range snapshot.Roles {
		if _, err := sources.Work.CreateRole(ctx, role); err != nil {
			t.Fatalf("Create role %q: %v", role.ID, err)
		}
	}
	for _, item := range snapshot.WorkItems {
		if _, err := sources.Work.CreateWorkItem(ctx, item); err != nil {
			t.Fatalf("Create work item %q: %v", item.ID, err)
		}
	}
	for _, assignment := range snapshot.Assignments {
		if _, err := sources.Work.CreateAssignment(ctx, assignment); err != nil {
			t.Fatalf("Create assignment %q: %v", assignment.ID, err)
		}
	}
	for _, artifact := range snapshot.Artifacts {
		if _, err := sources.Work.CreateArtifact(ctx, artifact); err != nil {
			t.Fatalf("Create artifact %q: %v", artifact.ID, err)
		}
	}
	for _, handoff := range snapshot.Handoffs {
		if _, err := sources.Work.CreateHandoff(ctx, handoff); err != nil {
			t.Fatalf("Create handoff %q: %v", handoff.ID, err)
		}
	}
	for _, entry := range snapshot.MemoryEntries {
		if _, err := sources.Memory.Create(ctx, entry); err != nil {
			t.Fatalf("Create memory entry %q: %v", entry.ID, err)
		}
	}
	for _, candidate := range snapshot.MemoryCandidates {
		if _, err := sources.MemoryCandidates.CreateCandidate(ctx, candidate); err != nil {
			t.Fatalf("Create memory candidate %q: %v", candidate.ID, err)
		}
	}
}

func hasAgentProfile(profiles []agentprofiles.Profile, id string) bool {
	for _, profile := range profiles {
		if profile.ID == id {
			return true
		}
	}
	return false
}

func hasRole(roles []projectwork.AgentRoleProfile, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func findCairnlineMemoryCandidate(candidates []cairnline.MemoryCandidate, id string) *cairnline.MemoryCandidate {
	for idx := range candidates {
		if candidates[idx].ID == id {
			return &candidates[idx]
		}
	}
	return nil
}
