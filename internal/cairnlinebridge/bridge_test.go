package cairnlinebridge

import (
	"context"
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

	snapshot := Snapshot{
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
			ID:                  "implementation",
			Name:                "Implementation",
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
			ID:                  "software_developer",
			ProjectID:           "proj_hecate",
			Name:                "Software Developer",
			Description:         "Implements backend and shared behavior.",
			Instructions:        "Keep handlers thin.",
			DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
			DefaultAgentProfile: "implementation",
			SkillIDs:            []string{"backend", "backend"},
			CreatedAt:           now,
			UpdatedAt:           now,
		}, {
			ID:                  "reviewer_qa",
			ProjectID:           "proj_hecate",
			Name:                "Reviewer QA",
			Description:         "Reviews behavior, risks, and verification gaps.",
			Instructions:        "Prioritize concrete defects.",
			DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
			DefaultAgentProfile: "implementation",
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
			OwnerRoleID:     "software_developer",
			RootID:          "root_main",
			ReviewerRoleIDs: []string{"reviewer_qa"},
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
		Assignments: []projectwork.Assignment{{
			ID:         "asgn_bridge",
			ProjectID:  "proj_hecate",
			WorkItemID: "work_bridge",
			RoleID:     "software_developer",
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
			RoleID:     "software_developer",
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
			AuthorRoleID:         "reviewer_qa",
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
			TargetRoleID:          "reviewer_qa",
			Title:                 "Review bridge parity",
			Summary:               "Artifact parity is ready for review.",
			RecommendedNextAction: "Verify launch packets include evidence, reviews, handoffs, and memory candidates.",
			LinkedArtifactIDs:     []string{"art_evidence", "art_review"},
			LinkedMemoryIDs:       []string{"memcand_bridge"},
			ContextRefs:           []string{"ctx_123"},
			Status:                projectwork.HandoffStatusPending,
			CreatedByRoleID:       "software_developer",
			CreatedAt:             now,
			UpdatedAt:             now,
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
			Status:              memory.CandidateStatusPending,
			CreatedAt:           now,
			UpdatedAt:           now,
		}},
	}

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
	if packet.Profile == nil || packet.Profile.ID != "implementation" || packet.Profile.MemoryPolicy != agentprofiles.MemoryInclude {
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
	if len(packet.Handoffs) != 1 || packet.Handoffs[0].ID != "handoff_review" || packet.Handoffs[0].FromRoleID != "software_developer" || packet.Handoffs[0].ToRoleID != "reviewer_qa" {
		t.Fatalf("packet handoffs = %+v, want mapped handoff roles", packet.Handoffs)
	}
	if len(packet.MemoryCandidates) != 1 || packet.MemoryCandidates[0].ID != "memcand_bridge" || packet.MemoryCandidates[0].TrustLabel != memory.TrustLabelGenerated || packet.MemoryCandidates[0].SourceRef != "handoff:handoff_review" {
		t.Fatalf("packet memory candidates = %+v, want mapped memory candidate provenance", packet.MemoryCandidates)
	}

	externalPacket, err := service.AssignmentLaunchPacket(ctx, "proj_hecate", "asgn_external")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() external error = %v", err)
	}
	if externalPacket.Assignment.Status != cairnline.AssignmentCompleted || externalPacket.Assignment.ExecutionMode != cairnline.ExecutionExternalAdapter || externalPacket.Assignment.ExecutionRef != "chat_123" {
		t.Fatalf("external assignment = %+v, want completed external-adapter assignment", externalPacket.Assignment)
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
