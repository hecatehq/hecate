package cairnlinebridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
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
	if got := SnapshotExecutionProfileCount(snapshot); got != 2 {
		t.Fatalf("SnapshotExecutionProfileCount() = %d, want project and role execution profiles", got)
	}

	packet, err := service.AssignmentLaunchPacket(ctx, "proj_hecate", "asgn_bridge")
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket() error = %v", err)
	}
	if packet.Project.ID != "proj_hecate" || packet.Project.DefaultRootID != "root_main" || packet.WorkItem.RootID != "root_main" {
		t.Fatalf("packet project/work = %+v/%+v, want Hecate project with default root and root-scoped work", packet.Project, packet.WorkItem)
	}
	if packet.Project.DefaultProfileID != "bridge_implementation" || packet.Project.DefaultExecutionProfileID != projectExecutionProfileID(snapshot.Project) {
		t.Fatalf("packet project defaults = %+v, want mapped project profile and execution defaults", packet.Project)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if len(executionProfiles) != SnapshotExecutionProfileCount(snapshot) {
		t.Fatalf("execution profile count = %d, want %d", len(executionProfiles), SnapshotExecutionProfileCount(snapshot))
	}
	projectExecutionProfile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileID(snapshot.Project))
	if projectExecutionProfile == nil ||
		projectExecutionProfile.AgentKind != "hecate" ||
		projectExecutionProfile.ProviderHint != "ollama" ||
		projectExecutionProfile.ModelHint != "qwen3-coder" ||
		projectExecutionProfile.ToolsPolicy != "allow" ||
		projectExecutionProfile.AdapterOptions["workspace_mode"] != "worktree" ||
		projectExecutionProfile.AdapterOptions["system_prompt"] != "Stay crisp." ||
		projectExecutionProfile.AdapterOptions["compact_tool_output"] != true {
		t.Fatalf("project execution profile = %+v, want Hecate project-level execution defaults", projectExecutionProfile)
	}
	if len(packet.Project.ContextSources) != 1 {
		t.Fatalf("packet project sources = %+v, want one context source", packet.Project.ContextSources)
	}
	source := packet.Project.ContextSources[0]
	if source.Locator != "AGENTS.md" || source.Format != "agents_md" || source.Scope != "workspace" || source.SourceCategory != "workspace_guidance" || source.Metadata["root_id"] != "root_main" || !source.CreatedAt.Equal(now) || !source.UpdatedAt.Equal(now) {
		t.Fatalf("packet project source = %+v, want portable context-source metadata", source)
	}
	if packet.Role == nil || packet.Role.DefaultExecutionMode != cairnline.ExecutionOrchestrated {
		t.Fatalf("packet role = %+v, want orchestrated role from Hecate task driver", packet.Role)
	}
	if packet.Role.DefaultExecutionProfileID != roleExecutionProfileID(snapshot.Roles[0]) {
		t.Fatalf("packet role = %+v, want role execution-profile default", packet.Role)
	}
	if packet.Role.DefaultProfileID != "bridge_implementation" || packet.Assignment.ProfileID != "bridge_implementation" {
		t.Fatalf("packet profile hints = role:%q assignment:%q, want unresolved Hecate preset id", packet.Role.DefaultProfileID, packet.Assignment.ProfileID)
	}
	if packet.ExecutionProfile == nil || packet.ExecutionProfile.ID != roleExecutionProfileID(snapshot.Roles[0]) || packet.ExecutionProfile.AgentKind != "hecate" || packet.ExecutionProfile.ProviderHint != "openai" || packet.ExecutionProfile.ModelHint != "gpt-5" {
		t.Fatalf("packet execution profile = %+v, want mapped Hecate role execution defaults", packet.ExecutionProfile)
	}
	if packet.Assignment.ExecutionMode != cairnline.ExecutionOrchestrated || packet.Assignment.RootID != "root_main" || packet.Assignment.ContextSnapshotID != "ctx_123" {
		t.Fatalf("packet assignment = %+v, want orchestrated root-scoped assignment", packet.Assignment)
	}
	if len(packet.Skills) != 1 || packet.Skills[0].ID != "backend" || len(packet.Skills[0].SourceRefs) != 1 || len(packet.Skills[0].SuggestedTools) != 2 || packet.Skills[0].RequiredPermissions.Writes == nil || *packet.Skills[0].RequiredPermissions.Writes {
		t.Fatalf("packet skills = %+v, want mapped backend skill with provenance and capability hints", packet.Skills)
	}
	if len(packet.Artifacts) != 1 || packet.Artifacts[0].ID != "art_decision" || packet.Artifacts[0].Kind != projectwork.ArtifactKindDecisionNote || packet.Artifacts[0].AuthorRoleID != "bridge_developer" {
		t.Fatalf("packet artifacts = %+v, want mapped generic collaboration artifact", packet.Artifacts)
	}
	if len(packet.Evidence) != 1 || packet.Evidence[0].ID != "art_evidence" || packet.Evidence[0].AssignmentID != "asgn_external" || packet.Evidence[0].Locator != "https://github.com/hecatehq/hecate/actions/runs/123" || packet.Evidence[0].SourceKind != "pull_request" || packet.Evidence[0].ExternalID != "PR 123" || packet.Evidence[0].Provider != "github" {
		t.Fatalf("packet evidence = %+v, want mapped assignment-scoped evidence link", packet.Evidence)
	}
	if len(packet.Reviews) != 1 || packet.Reviews[0].ID != "art_review" || packet.Reviews[0].AssignmentID != "asgn_external" || packet.Reviews[0].Verdict != cairnline.ReviewVerdictChangesRequested || packet.Reviews[0].Risk != cairnline.ReviewRiskMedium {
		t.Fatalf("packet reviews = %+v, want mapped review with portable verdict", packet.Reviews)
	}
	if len(packet.Handoffs) != 1 || packet.Handoffs[0].ID != "handoff_review" || packet.Handoffs[0].FromRoleID != "bridge_developer" || packet.Handoffs[0].ToRoleID != "bridge_reviewer" {
		t.Fatalf("packet handoffs = %+v, want mapped handoff roles", packet.Handoffs)
	}
	if packet.Handoffs[0].SourceAssignmentID != "asgn_external" || packet.Handoffs[0].SourceRunID != "run_external" || packet.Handoffs[0].TargetAssignmentID != "asgn_bridge" || packet.Handoffs[0].TargetWorkItemID != "work_bridge" || packet.Handoffs[0].Status != cairnline.HandoffStatusOpen {
		t.Fatalf("packet handoff refs = %+v, want structured source/target refs and open status", packet.Handoffs[0])
	}
	if packet.Handoffs[0].RecommendedNextAction == "" || len(packet.Handoffs[0].LinkedArtifactIDs) != 2 || len(packet.Handoffs[0].LinkedMemoryIDs) != 1 || len(packet.Handoffs[0].ContextRefs) != 1 || packet.Handoffs[0].ProvenanceKind != "agent_draft" || packet.Handoffs[0].TrustLabel != "operator_reviewed" {
		t.Fatalf("packet handoff metadata = %+v, want linked artifacts/memory/context provenance", packet.Handoffs[0])
	}
	if len(packet.Memory) != 1 || packet.Memory[0].ID != "mem_bridge" || packet.Memory[0].TrustLabel != memory.TrustLabelOperatorMemory {
		t.Fatalf("packet memory = %+v, want mapped accepted memory", packet.Memory)
	}
	if len(packet.MemoryCandidates) != 1 || packet.MemoryCandidates[0].ID != "memcand_bridge" || packet.MemoryCandidates[0].SuggestedTrustLabel != memory.TrustLabelGenerated || packet.MemoryCandidates[0].SuggestedSourceID != "handoff_review" || len(packet.MemoryCandidates[0].SourceRefs) != 1 {
		t.Fatalf("packet memory candidates = %+v, want mapped memory candidate provenance", packet.MemoryCandidates)
	}
	proposals, err := service.ListAssistantProposals(ctx, "proj_hecate")
	if err != nil {
		t.Fatalf("ListAssistantProposals() error = %v", err)
	}
	if len(proposals) != 1 || proposals[0].ID != "pa_bridge" || proposals[0].Status != cairnline.AssistantProposalStatusApplied || proposals[0].LatestResult == nil || len(proposals[0].ApplyAttempts) != 1 {
		t.Fatalf("assistant proposals = %+v, want one imported portable applied proposal with attempt ledger", proposals)
	}
	if len(proposals[0].Proposal.Warnings) != 1 || proposals[0].Proposal.Warnings[0] != "Review generated follow-up scope before applying." {
		t.Fatalf("assistant proposal warnings = %+v, want warning metadata preserved", proposals[0].Proposal.Warnings)
	}
	if len(proposals[0].Proposal.Actions) != 4 ||
		proposals[0].Proposal.Actions[0].Kind != cairnline.AssistantActionAttachProjectRoot ||
		proposals[0].Proposal.Actions[0].Root == nil ||
		proposals[0].Proposal.Actions[0].Root.ID != "root_proposal" ||
		proposals[0].Proposal.Actions[1].Kind != cairnline.AssistantActionSetProjectDefaults ||
		proposals[0].Proposal.Actions[1].Project == nil ||
		proposals[0].Proposal.Actions[1].Project.DefaultRootID != "root_proposal" ||
		proposals[0].Proposal.Actions[2].Kind != cairnline.AssistantActionRemoveProjectRoot ||
		proposals[0].Proposal.Actions[2].Target.RootID != "root_legacy" ||
		proposals[0].Proposal.Actions[3].Kind != cairnline.AssistantActionCreateWorkItem ||
		proposals[0].Proposal.Actions[3].WorkItem == nil ||
		proposals[0].Proposal.Actions[3].WorkItem.ID != "work_from_proposal" {
		t.Fatalf("assistant proposal actions = %+v, want mapped root/default and create-work-item actions", proposals[0].Proposal.Actions)
	}
	if proposals[0].LatestResult.TotalActionCount != 4 || len(proposals[0].LatestResult.Actions) != 4 || proposals[0].LatestResult.Actions[0].RootID != "root_proposal" || proposals[0].LatestResult.Actions[1].RootID != "root_proposal" || proposals[0].LatestResult.Actions[2].RootID != "root_legacy" {
		t.Fatalf("assistant proposal latest result = %+v, want root action result refs", proposals[0].LatestResult)
	}
	workItems, err := service.ListWorkItems(ctx, "proj_hecate")
	if err != nil {
		t.Fatalf("ListWorkItems() error = %v", err)
	}
	if len(workItems) != len(snapshot.WorkItems) {
		t.Fatalf("work item count after proposal import = %d, want %d; import must not replay proposal actions", len(workItems), len(snapshot.WorkItems))
	}
	readiness, err := service.WorkItemCloseoutReadiness(ctx, "proj_hecate", "work_bridge")
	if err != nil {
		t.Fatalf("WorkItemCloseoutReadiness() error = %v", err)
	}
	if readiness.CompletedAssignments != 1 || len(readiness.MissingEvidenceAssignmentIDs) != 0 {
		t.Fatalf("work readiness = %+v, want completed external assignment covered by mapped evidence", readiness)
	}
	brief, err := service.ProjectOperationsBrief(ctx, "proj_hecate")
	if err != nil {
		t.Fatalf("ProjectOperationsBrief() error = %v", err)
	}
	if brief.Status != cairnline.ProjectOperationsStatusAttention || brief.Next == nil || brief.Next.Kind != cairnline.ProjectOperationKindAssignment || brief.Next.AssignmentID != "asgn_bridge" {
		t.Fatalf("operations brief = %+v, want queued assignment next", brief)
	}
	if brief.Counts.Assignments != 2 || brief.Counts.ActiveAssignments != 0 || brief.Counts.BlockedAssignments != 1 || brief.Counts.PendingMemoryCandidates != 1 || brief.Counts.ReviewFollowUps != 1 || brief.Counts.OpenHandoffs != 1 || brief.Counts.MissingEvidence != 0 {
		t.Fatalf("operations counts = %+v, want blocked queued assignment, memory candidate, review follow-up, and open handoff", brief.Counts)
	}
	if !hasCairnlineOperation(brief.Items, cairnline.ProjectOperationKindAssignment, "work_bridge", "asgn_bridge") ||
		!hasCairnlineOperation(brief.Items, cairnline.ProjectOperationKindHandoff, "work_bridge", "handoff_review") ||
		!hasCairnlineOperation(brief.Items, cairnline.ProjectOperationKindReviewFollowUp, "work_bridge", "art_review") {
		t.Fatalf("operations items = %+v, want assignment, handoff, and review follow-up items", brief.Items)
	}
	activity, err := service.ProjectActivity(ctx, "proj_hecate")
	if err != nil {
		t.Fatalf("ProjectActivity() error = %v", err)
	}
	if activity.Counts.Assignments != 2 || activity.Counts.Active != 0 || activity.Counts.Blocked != 1 || activity.Counts.Completed != 1 || len(activity.Buckets.Blocked) != 1 || len(activity.Buckets.Completed) != 1 {
		t.Fatalf("activity = %+v, want blocked and completed assignment buckets", activity)
	}
	if !hasCairnlineActivity(activity.Items, cairnline.ProjectActivityBucketBlocked, "work_bridge", "asgn_bridge") ||
		!hasCairnlineActivity(activity.Items, cairnline.ProjectActivityBucketCompleted, "work_bridge", "asgn_external") {
		t.Fatalf("activity items = %+v, want blocked queued assignment and completed external assignment", activity.Items)
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

func TestCairnlineSnapshotMapsHecateSnapshotToPortableContract(t *testing.T) {
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	source := bridgeSnapshotFixture(now)
	snapshot := CairnlineSnapshot([]Snapshot{source})

	if snapshot.Version != cairnline.SnapshotVersion {
		t.Fatalf("snapshot version = %d, want %d", snapshot.Version, cairnline.SnapshotVersion)
	}
	if len(snapshot.Projects) != 1 || snapshot.Projects[0].ID != source.Project.ID {
		t.Fatalf("snapshot projects = %+v, want mapped Hecate project", snapshot.Projects)
	}
	if len(snapshot.ExecutionProfiles) != SnapshotExecutionProfileCount(source) {
		t.Fatalf("snapshot execution profiles = %+v, want %d unique execution profiles", snapshot.ExecutionProfiles, SnapshotExecutionProfileCount(source))
	}
	if len(snapshot.ProjectSkills) != len(source.Skills) || snapshot.ProjectSkills[0].RequiredPermissions.Writes == nil || *snapshot.ProjectSkills[0].RequiredPermissions.Writes {
		t.Fatalf("snapshot skills = %+v, want project skill capability metadata", snapshot.ProjectSkills)
	}
	if len(snapshot.WorkItems) != len(source.WorkItems) || len(snapshot.Assignments) != len(source.Assignments) {
		t.Fatalf("snapshot work/assignments = %d/%d, want %d/%d", len(snapshot.WorkItems), len(snapshot.Assignments), len(source.WorkItems), len(source.Assignments))
	}
	if len(snapshot.Artifacts) != 1 || len(snapshot.Evidence) != 1 || len(snapshot.Reviews) != 1 {
		t.Fatalf("snapshot artifacts/evidence/reviews = %d/%d/%d, want split portable collaboration records", len(snapshot.Artifacts), len(snapshot.Evidence), len(snapshot.Reviews))
	}
	if len(snapshot.Handoffs) != len(source.Handoffs) || snapshot.Handoffs[0].StatusChangedAt.IsZero() {
		t.Fatalf("snapshot handoffs = %+v, want handoff with status-change timestamp", snapshot.Handoffs)
	}
	if len(snapshot.MemoryEntries) != len(source.MemoryEntries) || len(snapshot.MemoryCandidates) != len(source.MemoryCandidates) {
		t.Fatalf("snapshot memory entries/candidates = %d/%d, want %d/%d", len(snapshot.MemoryEntries), len(snapshot.MemoryCandidates), len(source.MemoryEntries), len(source.MemoryCandidates))
	}
	if len(snapshot.AssistantProposals) == 0 || len(snapshot.AssistantProposals[0].Proposal.Actions) == 0 {
		t.Fatalf("snapshot assistant proposals = %+v, want proposal ledger imported without replay", snapshot.AssistantProposals)
	}
}

func TestAssistantProposalRecordProjectsCairnlineLedgerBackToHecate(t *testing.T) {
	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	source := bridgeAssistantProposalFixture(now)
	imported, ok := AssistantProposalRecord(source)
	if !ok {
		t.Fatalf("AssistantProposalRecord() ok = false, want portable proposal fixture imported")
	}
	projected, ok := ProjectAssistantProposalRecord(imported)
	if !ok {
		t.Fatalf("ProjectAssistantProposalRecord() ok = false, want Cairnline proposal projected back to Hecate")
	}
	if projected.ID != source.ID || projected.ProjectID != source.ProjectID || projected.Status != projectassistant.ApplyStatusApplied || projected.LatestResult == nil || len(projected.ApplyAttempts) != 1 {
		t.Fatalf("projected proposal = %+v, want applied Hecate proposal record with one attempt", projected)
	}
	if projected.LatestResult.CommittedActionCount != 4 || projected.LatestResult.ResumeActionIndex != 4 || len(projected.LatestResult.Actions) != 4 {
		t.Fatalf("projected latest result = %+v, want Hecate apply progress counts", projected.LatestResult)
	}
	if len(projected.Proposal.Actions) != 4 {
		t.Fatalf("projected actions = %+v, want four portable actions", projected.Proposal.Actions)
	}
	var attachRoot assistantRootPatch
	if err := json.Unmarshal(projected.Proposal.Actions[0].Patch, &attachRoot); err != nil {
		t.Fatalf("decode attach-root patch: %v", err)
	}
	if projected.Proposal.Actions[0].Kind != projectassistant.ActionAttachProjectRoot || attachRoot.ID != "root_proposal" || attachRoot.GitBranch != "proposal/root-actions" {
		t.Fatalf("projected attach-root action = %+v patch %+v, want root action reconstructed", projected.Proposal.Actions[0], attachRoot)
	}
	var defaults assistantDefaultsPatch
	if err := json.Unmarshal(projected.Proposal.Actions[1].Patch, &defaults); err != nil {
		t.Fatalf("decode defaults patch: %v", err)
	}
	if defaults.DefaultRootID == nil || *defaults.DefaultRootID != "root_proposal" {
		t.Fatalf("projected defaults patch = %+v, want default root id", defaults)
	}
	var work assistantWorkItemPatch
	if err := json.Unmarshal(projected.Proposal.Actions[3].Patch, &work); err != nil {
		t.Fatalf("decode work patch: %v", err)
	}
	if projected.Proposal.Actions[3].Kind != projectassistant.ActionCreateWorkItem || work.ID != "work_from_proposal" || work.RootID != "root_main" {
		t.Fatalf("projected work action = %+v patch %+v, want create-work-item action reconstructed", projected.Proposal.Actions[3], work)
	}
	if projected.ApplyAttempts[0].Result.CommittedActionCount != 4 || projected.ApplyAttempts[0].Result.ResumeActionIndex != 4 {
		t.Fatalf("projected apply attempt = %+v, want preserved Hecate apply progress", projected.ApplyAttempts[0])
	}
}

func TestProjectAssistantProposalRecordKeepsNeedsConfirmationReviewable(t *testing.T) {
	now := time.Date(2026, 6, 27, 13, 30, 0, 0, time.UTC)
	source, ok := AssistantProposalRecord(bridgeAssistantProposalFixture(now))
	if !ok {
		t.Fatal("AssistantProposalRecord() ok = false, want portable proposal fixture")
	}
	result := cairnline.AssistantApplyResult{
		ProposalID:       source.ID,
		Status:           cairnline.AssistantApplyStatusNeedsConfirm,
		Confirmed:        false,
		TotalActionCount: len(source.Proposal.Actions),
	}
	source.Status = cairnline.AssistantProposalStatusNeedsConfirm
	source.LatestResult = &result
	source.ApplyAttempts = []cairnline.AssistantApplyAttempt{{
		ID:         "paatt_bridge_needs_confirmation",
		ProposalID: source.ID,
		Status:     cairnline.AssistantApplyStatusNeedsConfirm,
		Confirmed:  false,
		Result:     result,
		CreatedAt:  now,
	}}
	source.AppliedAt = nil

	projected, ok := ProjectAssistantProposalRecord(source)
	if !ok {
		t.Fatal("ProjectAssistantProposalRecord() ok = false, want confirmation-required proposal projected")
	}
	if projected.Status != projectassistant.ProposalStatusProposed {
		t.Fatalf("projected status = %q, want proposed so confirmation-required proposals remain reviewable", projected.Status)
	}
	if projected.LatestResult == nil || projected.LatestResult.Status != projectassistant.ApplyStatusBlockedBeforeApply || projected.LatestResult.Applied || projected.LatestResult.CommittedActionCount != 0 {
		t.Fatalf("projected latest result = %+v, want explicit unconfirmed apply gate", projected.LatestResult)
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
	if !hasRole(loaded.Roles, "bridge_developer") || !hasRole(loaded.Roles, "bridge_reviewer") {
		t.Fatalf("loaded roles missing bridge roles: %+v", loaded.Roles)
	}
	if len(loaded.Skills) != len(snapshot.Skills) || len(loaded.WorkItems) != len(snapshot.WorkItems) || len(loaded.Assignments) != len(snapshot.Assignments) {
		t.Fatalf("loaded counts skills=%d work=%d assignments=%d, want %d/%d/%d", len(loaded.Skills), len(loaded.WorkItems), len(loaded.Assignments), len(snapshot.Skills), len(snapshot.WorkItems), len(snapshot.Assignments))
	}
	if len(loaded.Artifacts) != len(snapshot.Artifacts) || len(loaded.Handoffs) != len(snapshot.Handoffs) || len(loaded.MemoryEntries) != len(snapshot.MemoryEntries) || len(loaded.MemoryCandidates) != len(snapshot.MemoryCandidates) || len(loaded.AssistantProposals) != len(snapshot.AssistantProposals) {
		t.Fatalf("loaded collaboration counts artifacts=%d handoffs=%d memory_entries=%d memory_candidates=%d proposals=%d, want %d/%d/%d/%d/%d", len(loaded.Artifacts), len(loaded.Handoffs), len(loaded.MemoryEntries), len(loaded.MemoryCandidates), len(loaded.AssistantProposals), len(snapshot.Artifacts), len(snapshot.Handoffs), len(snapshot.MemoryEntries), len(snapshot.MemoryCandidates), len(snapshot.AssistantProposals))
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
	proposals, err := service.ListAssistantProposals(ctx, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("ListAssistantProposals() loaded snapshot error = %v", err)
	}
	if len(proposals) != 1 || proposals[0].ID != "pa_bridge" {
		t.Fatalf("loaded assistant proposals = %+v, want portable proposal imported into Cairnline", proposals)
	}
	if len(proposals[0].Proposal.Warnings) != 1 || proposals[0].Proposal.Warnings[0] != "Review generated follow-up scope before applying." {
		t.Fatalf("loaded assistant proposal warnings = %+v, want preserved warnings", proposals[0].Proposal.Warnings)
	}
}

func TestProjectActivityFromStores(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	sources := bridgeMemorySources()
	snapshot := bridgeSnapshotFixture(now)
	seedHecateSources(t, ctx, sources, snapshot)

	activity, loaded, err := ProjectActivityFromStores(ctx, sources, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("ProjectActivityFromStores() error = %v", err)
	}
	if loaded.Project.ID != snapshot.Project.ID || len(loaded.Assignments) != len(snapshot.Assignments) {
		t.Fatalf("loaded snapshot = %+v, want project and assignment state", loaded)
	}
	if activity.Counts.Assignments != 2 || activity.Counts.Active != 0 || activity.Counts.Blocked != 1 || activity.Counts.Completed != 1 {
		t.Fatalf("activity counts = %+v, want seeded blocked/completed assignments", activity.Counts)
	}
	if !hasCairnlineActivity(activity.Items, cairnline.ProjectActivityBucketBlocked, "work_bridge", "asgn_bridge") ||
		!hasCairnlineActivity(activity.Items, cairnline.ProjectActivityBucketCompleted, "work_bridge", "asgn_external") {
		t.Fatalf("activity items = %+v, want blocked and completed assignment metadata", activity.Items)
	}
}

func TestProjectOperationsBriefFromStores(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	sources := bridgeMemorySources()
	snapshot := bridgeSnapshotFixture(now)
	seedHecateSources(t, ctx, sources, snapshot)

	brief, loaded, err := ProjectOperationsBriefFromStores(ctx, sources, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("ProjectOperationsBriefFromStores() error = %v", err)
	}
	if loaded.Project.ID != snapshot.Project.ID || len(loaded.Assignments) != len(snapshot.Assignments) {
		t.Fatalf("loaded snapshot = %+v, want project and assignment state", loaded)
	}
	if brief.Status != cairnline.ProjectOperationsStatusAttention || brief.Next == nil || brief.Next.Kind != cairnline.ProjectOperationKindAssignment || brief.Next.AssignmentID != "asgn_bridge" {
		t.Fatalf("operations brief = %+v, want queued assignment attention from seeded Hecate stores", brief)
	}
	if brief.Counts.Assignments != 2 || brief.Counts.ActiveAssignments != 0 || brief.Counts.BlockedAssignments != 1 || brief.Counts.PendingMemoryCandidates != 1 || brief.Counts.OpenHandoffs != 1 {
		t.Fatalf("operations counts = %+v, want assignment, memory, and handoff parity", brief.Counts)
	}
}

func TestUpsertProjectMirrorsProjectRootAndContextSourceMutations(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)

	project := projects.Project{
		ID:                   "proj_write",
		Name:                 "Write Adapter",
		Description:          "Prove project writes can land in Cairnline.",
		DefaultProvider:      "openai",
		DefaultModel:         "gpt-5",
		DefaultAgentProfile:  "implementation",
		DefaultToolsEnabled:  boolPtrForTest(true),
		DefaultWorkspaceMode: "worktree",
		Roots: []projects.Root{{
			ID:        "root_main",
			Path:      "/tmp/hecate-write",
			Kind:      "git",
			GitRemote: "https://github.com/hecatehq/hecate",
			GitBranch: "main",
			Active:    true,
			CreatedAt: now,
			UpdatedAt: now,
		}},
		DefaultRootID: "root_main",
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
			Metadata:       map[string]string{"root_id": "root_main"},
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	created, err := UpsertProject(ctx, service, project)
	if err != nil {
		t.Fatalf("UpsertProject(create) error = %v", err)
	}
	if created.ID != project.ID || created.DefaultRootID != "root_main" || created.DefaultProfileID != "implementation" || created.DefaultExecutionProfileID != projectExecutionProfileID(project) {
		t.Fatalf("created project = %+v, want mapped Hecate project defaults", created)
	}
	if len(created.Roots) != 1 || created.Roots[0].Path != "/tmp/hecate-write" || created.Roots[0].GitBranch != "main" {
		t.Fatalf("created roots = %+v, want portable root metadata", created.Roots)
	}
	if len(created.ContextSources) != 1 || created.ContextSources[0].Locator != "AGENTS.md" || created.ContextSources[0].Metadata["root_id"] != "root_main" {
		t.Fatalf("created sources = %+v, want portable context-source metadata", created.ContextSources)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileID(project)); profile == nil || profile.ProviderHint != "openai" || profile.ModelHint != "gpt-5" || profile.ToolsPolicy != "allow" || profile.AdapterOptions["workspace_mode"] != "worktree" {
		t.Fatalf("project execution profile = %+v, want Hecate project execution defaults", profile)
	}

	project.Name = "Write Adapter Updated"
	project.DefaultProvider = ""
	project.DefaultModel = ""
	project.DefaultToolsEnabled = nil
	project.DefaultWorkspaceMode = ""
	project.Roots = []projects.Root{{
		ID:     "root_docs",
		Path:   "/tmp/hecate-docs",
		Kind:   "folder",
		Active: true,
	}}
	project.DefaultRootID = "root_docs"
	project.ContextSources = []projects.ContextSource{{
		ID:             "ctx_readme",
		Kind:           "workspace_instruction",
		Title:          "README.md",
		Path:           "README.md",
		Enabled:        false,
		Format:         "markdown",
		Scope:          "workspace",
		TrustLabel:     "workspace_guidance",
		SourceCategory: "workspace_guidance",
	}}

	updated, err := UpsertProject(ctx, service, project)
	if err != nil {
		t.Fatalf("UpsertProject(update) error = %v", err)
	}
	if updated.Name != "Write Adapter Updated" || updated.DefaultRootID != "root_docs" || updated.DefaultExecutionProfileID != "" {
		t.Fatalf("updated project = %+v, want updated project and removed execution default", updated)
	}
	if len(updated.Roots) != 1 || updated.Roots[0].ID != "root_docs" {
		t.Fatalf("updated roots = %+v, want replaced root set", updated.Roots)
	}
	if len(updated.ContextSources) != 1 || updated.ContextSources[0].ID != "ctx_readme" || updated.ContextSources[0].Enabled {
		t.Fatalf("updated sources = %+v, want replaced source set", updated.ContextSources)
	}
	executionProfiles, err = service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() after update error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileIDValue(project)); profile != nil {
		t.Fatalf("project execution profile = %+v, want removed after project defaults cleared", profile)
	}
}

func TestUpsertProjectDefaultsPreservesRootAndSourceState(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 10, 30, 0, 0, time.UTC)

	project := projects.Project{
		ID:                   "proj_defaults",
		Name:                 "Defaults Adapter",
		DefaultProvider:      "openai",
		DefaultModel:         "gpt-5",
		DefaultAgentProfile:  "implementation",
		DefaultToolsEnabled:  boolPtrForTest(true),
		DefaultWorkspaceMode: "worktree",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-defaults",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject(create) error = %v", err)
	}
	if _, _, err := service.CreateRoot(ctx, project.ID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   "/tmp/hecate-defaults-cairnline-only",
		Kind:   "folder",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(cairnline-only) error = %v", err)
	}
	if _, _, err := service.CreateContextSource(ctx, project.ID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only) error = %v", err)
	}

	project.DefaultProvider = "anthropic"
	project.DefaultModel = "claude-sonnet-4-5"
	project.DefaultAgentProfile = "architecture"
	project.UpdatedAt = now.Add(time.Minute)
	updated, err := UpsertProjectDefaults(ctx, service, project)
	if err != nil {
		t.Fatalf("UpsertProjectDefaults(update) error = %v", err)
	}
	if updated.DefaultProfileID != "architecture" || updated.DefaultExecutionProfileID != projectExecutionProfileID(project) {
		t.Fatalf("updated defaults = %+v, want portable project defaults", updated)
	}
	if findCairnlineRoot(updated.Roots, "root_cairnline_only") == nil {
		t.Fatalf("updated roots = %+v, want Cairnline-only root preserved", updated.Roots)
	}
	if findCairnlineSource(updated.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("updated sources = %+v, want Cairnline-only source preserved", updated.ContextSources)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileID(project)); profile == nil || profile.ProviderHint != "anthropic" || profile.ModelHint != "claude-sonnet-4-5" {
		t.Fatalf("project execution profile = %+v, want updated defaults", profile)
	}

	project.DefaultProvider = ""
	project.DefaultModel = ""
	project.DefaultToolsEnabled = nil
	project.DefaultWorkspaceMode = ""
	project.DefaultSystemPrompt = ""
	project.DefaultCompactToolOutput = nil
	project.UpdatedAt = now.Add(2 * time.Minute)
	updated, err = UpsertProjectDefaults(ctx, service, project)
	if err != nil {
		t.Fatalf("UpsertProjectDefaults(clear) error = %v", err)
	}
	if updated.DefaultExecutionProfileID != "" {
		t.Fatalf("cleared defaults = %+v, want no execution profile default", updated)
	}
	executionProfiles, err = service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() after clear error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileIDValue(project)); profile != nil {
		t.Fatalf("project execution profile = %+v, want deleted after defaults clear", profile)
	}
}

func TestUpsertProjectMetadataPreservesRootAndSourceState(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 11, 30, 0, 0, time.UTC)

	project := projects.Project{
		ID:          "proj_metadata",
		Name:        "Metadata Adapter",
		Description: "Before",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-metadata",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject(create) error = %v", err)
	}
	if _, _, err := service.CreateRoot(ctx, project.ID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   "/tmp/hecate-metadata-cairnline-only",
		Kind:   "folder",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(cairnline-only) error = %v", err)
	}
	if _, _, err := service.CreateContextSource(ctx, project.ID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only) error = %v", err)
	}

	project.Name = "Metadata Adapter Updated"
	project.Description = "After"
	project.UpdatedAt = now.Add(time.Minute)
	updated, err := UpsertProjectMetadata(ctx, service, project)
	if err != nil {
		t.Fatalf("UpsertProjectMetadata(update) error = %v", err)
	}
	if updated.Name != "Metadata Adapter Updated" || updated.Description != "After" {
		t.Fatalf("updated project = %+v, want updated metadata", updated)
	}
	if findCairnlineRoot(updated.Roots, "root_cairnline_only") == nil {
		t.Fatalf("updated roots = %+v, want Cairnline-only root preserved", updated.Roots)
	}
	if findCairnlineSource(updated.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("updated sources = %+v, want Cairnline-only source preserved", updated.ContextSources)
	}
}

func TestReplaceProjectRootsPreservesContextSourceState(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)

	project := projects.Project{
		ID:   "proj_replace_roots",
		Name: "Replace Roots",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-roots-main",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject(create) error = %v", err)
	}
	if _, _, err := service.CreateRoot(ctx, project.ID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   "/tmp/hecate-roots-cairnline-only",
		Kind:   "folder",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(cairnline-only) error = %v", err)
	}
	if _, _, err := service.CreateContextSource(ctx, project.ID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only) error = %v", err)
	}

	project.Roots = []projects.Root{{
		ID:     "root_replacement",
		Path:   "/tmp/hecate-roots-replacement",
		Kind:   "git",
		Active: true,
	}}
	project.DefaultRootID = "root_replacement"
	updated, err := ReplaceProjectRoots(ctx, service, project, project.Roots)
	if err != nil {
		t.Fatalf("ReplaceProjectRoots() error = %v", err)
	}
	if len(updated.Roots) != 1 || updated.Roots[0].ID != "root_replacement" || updated.DefaultRootID != "root_replacement" {
		t.Fatalf("updated roots = %+v default=%q, want replacement root only", updated.Roots, updated.DefaultRootID)
	}
	if findCairnlineSource(updated.ContextSources, "ctx_cairnline_only") == nil {
		t.Fatalf("updated sources = %+v, want Cairnline-only source preserved", updated.ContextSources)
	}
}

func TestReplaceProjectContextSourcesPreservesRootState(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 13, 30, 0, 0, time.UTC)

	project := projects.Project{
		ID:   "proj_replace_sources",
		Name: "Replace Sources",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-sources-main",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
		ContextSources: []projects.ContextSource{{
			ID:      "ctx_agents",
			Kind:    "workspace_instruction",
			Title:   "AGENTS.md",
			Path:    "AGENTS.md",
			Enabled: true,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject(create) error = %v", err)
	}
	if _, _, err := service.CreateRoot(ctx, project.ID, cairnline.Root{
		ID:     "root_cairnline_only",
		Path:   "/tmp/hecate-sources-cairnline-only",
		Kind:   "folder",
		Active: true,
	}); err != nil {
		t.Fatalf("CreateRoot(cairnline-only) error = %v", err)
	}
	if _, _, err := service.CreateContextSource(ctx, project.ID, cairnline.Source{
		ID:      "ctx_cairnline_only",
		Kind:    "operator_note",
		Title:   "Cairnline-only source",
		Locator: "cairnline://source",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateContextSource(cairnline-only) error = %v", err)
	}

	project.ContextSources = []projects.ContextSource{{
		ID:      "ctx_replacement",
		Kind:    "doc",
		Title:   "Replacement",
		Path:    "docs/replacement.md",
		Enabled: true,
	}}
	updated, err := ReplaceProjectContextSources(ctx, service, project, project.ContextSources)
	if err != nil {
		t.Fatalf("ReplaceProjectContextSources() error = %v", err)
	}
	if len(updated.ContextSources) != 1 || updated.ContextSources[0].ID != "ctx_replacement" {
		t.Fatalf("updated sources = %+v, want replacement source only", updated.ContextSources)
	}
	if findCairnlineRoot(updated.Roots, "root_cairnline_only") == nil {
		t.Fatalf("updated roots = %+v, want Cairnline-only root preserved", updated.Roots)
	}
}

func TestUpsertContextSourceMirrorsSingleSourceMutations(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)

	project := projects.Project{
		ID:        "proj_sources",
		Name:      "Source Adapter",
		Roots:     []projects.Root{{ID: "root_main", Path: "/tmp/hecate-sources", Active: true}},
		CreatedAt: now,
		UpdatedAt: now,
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
			Metadata:       map[string]string{"root_id": "root_main"},
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() seed error = %v", err)
	}

	created, err := UpsertContextSource(ctx, service, project, projects.ContextSource{
		ID:             "ctx_design",
		Kind:           "doc",
		Title:          "Projects",
		Path:           "docs/design/accepted/projects.md",
		Enabled:        true,
		Format:         "markdown",
		TrustLabel:     "operator_source",
		SourceCategory: "operator_source",
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("UpsertContextSource(create) error = %v", err)
	}
	if created.ID != "ctx_design" || created.Locator != "docs/design/accepted/projects.md" || !created.Enabled {
		t.Fatalf("created source = %+v, want enabled design source", created)
	}
	read, err := service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after create error = %v", err)
	}
	if len(read.ContextSources) != 2 || findCairnlineSource(read.ContextSources, "ctx_agents") == nil || findCairnlineSource(read.ContextSources, "ctx_design") == nil {
		t.Fatalf("sources after create = %+v, want original and created source", read.ContextSources)
	}

	updated, err := UpsertContextSource(ctx, service, project, projects.ContextSource{
		ID:             "ctx_design",
		Kind:           "doc",
		Title:          "Cairnline proposal",
		Path:           "docs/design/proposals/cairnline-portable-project-coordination.md",
		Enabled:        false,
		Format:         "markdown",
		TrustLabel:     "workspace_guidance",
		SourceCategory: "workspace_guidance",
		UpdatedAt:      now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertContextSource(update) error = %v", err)
	}
	if updated.ID != "ctx_design" || updated.Title != "Cairnline proposal" || updated.Locator != "docs/design/proposals/cairnline-portable-project-coordination.md" || updated.Enabled {
		t.Fatalf("updated source = %+v, want disabled proposal source", updated)
	}
	read, err = service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after update error = %v", err)
	}
	if len(read.ContextSources) != 2 {
		t.Fatalf("sources after update = %+v, want two sources", read.ContextSources)
	}
	if design := findCairnlineSource(read.ContextSources, "ctx_design"); design == nil || design.Title != "Cairnline proposal" || design.Enabled {
		t.Fatalf("ctx_design after update = %+v, want updated disabled source", design)
	}

	if err := DeleteContextSource(ctx, service, project.ID, "ctx_design"); err != nil {
		t.Fatalf("DeleteContextSource() error = %v", err)
	}
	read, err = service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after delete error = %v", err)
	}
	if len(read.ContextSources) != 1 || read.ContextSources[0].ID != "ctx_agents" {
		t.Fatalf("sources after delete = %+v, want only original source", read.ContextSources)
	}
	if err := DeleteContextSource(ctx, service, project.ID, "ctx_missing"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("DeleteContextSource(missing) error = %v, want ErrNotFound", err)
	}

	missingProject := projects.Project{
		ID:   "proj_missing_source",
		Name: "Missing Source Project",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-missing-source",
			Active: true,
		}},
	}
	createdFromSource, err := UpsertContextSource(ctx, service, missingProject, projects.ContextSource{
		ID:      "ctx_standalone",
		Kind:    "doc",
		Title:   "Standalone source",
		Path:    "README.md",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertContextSource(create missing project source) error = %v", err)
	}
	if createdFromSource.ID != "ctx_standalone" || createdFromSource.Locator != "README.md" {
		t.Fatalf("created missing-project source = %+v, want standalone source", createdFromSource)
	}
	readMissingProject, err := service.GetProject(ctx, missingProject.ID)
	if err != nil {
		t.Fatalf("GetProject() missing-project source error = %v", err)
	}
	if len(readMissingProject.ContextSources) != 1 || readMissingProject.ContextSources[0].ID != "ctx_standalone" {
		t.Fatalf("missing-project sources = %+v, want created standalone source", readMissingProject.ContextSources)
	}
}

func TestUpsertRootMirrorsSingleRootMutations(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 11, 15, 0, 0, time.UTC)

	project := projects.Project{
		ID:        "proj_roots",
		Name:      "Root Adapter",
		Roots:     []projects.Root{{ID: "root_main", Path: "/tmp/hecate-roots", Kind: "git", GitBranch: "main", Active: true}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() seed error = %v", err)
	}

	created, err := UpsertRoot(ctx, service, project, projects.Root{
		ID:        "root_worktree",
		Path:      "/tmp/hecate-roots/.worktrees/feature",
		Kind:      "git_worktree",
		GitBranch: "feature/root-mirror",
		Active:    true,
	})
	if err != nil {
		t.Fatalf("UpsertRoot(create) error = %v", err)
	}
	if created.ID != "root_worktree" || created.Path != "/tmp/hecate-roots/.worktrees/feature" || created.GitBranch != "feature/root-mirror" || !created.Active {
		t.Fatalf("created root = %+v, want active worktree root", created)
	}
	read, err := service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after create error = %v", err)
	}
	if len(read.Roots) != 2 || findCairnlineRoot(read.Roots, "root_main") == nil || findCairnlineRoot(read.Roots, "root_worktree") == nil {
		t.Fatalf("roots after create = %+v, want original and created root", read.Roots)
	}

	updated, err := UpsertRoot(ctx, service, project, projects.Root{
		ID:        "root_worktree",
		Path:      "/tmp/hecate-roots/.worktrees/feature-updated",
		Kind:      "git_worktree",
		GitBranch: "feature/root-mirror-updated",
		Active:    false,
	})
	if err != nil {
		t.Fatalf("UpsertRoot(update) error = %v", err)
	}
	if updated.ID != "root_worktree" || updated.Path != "/tmp/hecate-roots/.worktrees/feature-updated" || updated.GitBranch != "feature/root-mirror-updated" || updated.Active {
		t.Fatalf("updated root = %+v, want inactive updated worktree root", updated)
	}
	read, err = service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after update error = %v", err)
	}
	if worktree := findCairnlineRoot(read.Roots, "root_worktree"); worktree == nil || worktree.Path != "/tmp/hecate-roots/.worktrees/feature-updated" || worktree.Active {
		t.Fatalf("root_worktree after update = %+v, want updated inactive root", worktree)
	}

	if err := DeleteRoot(ctx, service, project.ID, "root_worktree"); err != nil {
		t.Fatalf("DeleteRoot() error = %v", err)
	}
	read, err = service.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject() after delete error = %v", err)
	}
	if len(read.Roots) != 1 || read.Roots[0].ID != "root_main" {
		t.Fatalf("roots after delete = %+v, want only original root", read.Roots)
	}
	if err := DeleteRoot(ctx, service, project.ID, "root_missing"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("DeleteRoot(missing) error = %v, want ErrNotFound", err)
	}

	missingProject := projects.Project{
		ID:   "proj_missing_root",
		Name: "Missing Root Project",
	}
	createdFromRoot, err := UpsertRoot(ctx, service, missingProject, projects.Root{
		ID:     "root_standalone",
		Path:   "/tmp/hecate-standalone-root",
		Kind:   "local",
		Active: true,
	})
	if err != nil {
		t.Fatalf("UpsertRoot(create missing project root) error = %v", err)
	}
	if createdFromRoot.ID != "root_standalone" || createdFromRoot.Path != "/tmp/hecate-standalone-root" {
		t.Fatalf("created missing-project root = %+v, want standalone root", createdFromRoot)
	}
	readMissingProject, err := service.GetProject(ctx, missingProject.ID)
	if err != nil {
		t.Fatalf("GetProject() missing-project root error = %v", err)
	}
	if len(readMissingProject.Roots) != 1 || readMissingProject.Roots[0].ID != "root_standalone" {
		t.Fatalf("missing-project roots = %+v, want created standalone root", readMissingProject.Roots)
	}
}

func TestDeleteProjectRemovesProjectAndProjectExecutionProfile(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	project := projects.Project{
		ID:              "proj_delete",
		Name:            "Delete Adapter",
		DefaultProvider: "openai",
		DefaultModel:    "gpt-5",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-delete",
			Active: true,
		}},
		DefaultRootID: "root_main",
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	if err := DeleteProject(ctx, service, project); err != nil {
		t.Fatalf("DeleteProject() error = %v", err)
	}
	if _, err := service.GetProject(ctx, project.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetProject() error = %v, want cairnline.ErrNotFound", err)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, projectExecutionProfileIDValue(project)); profile != nil {
		t.Fatalf("project execution profile = %+v, want deleted with project", profile)
	}
}

func TestUpsertRoleMirrorsRoleAndExecutionDefaults(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 11, 30, 0, 0, time.UTC)
	if _, err := UpsertProject(ctx, service, projects.Project{ID: "proj_roles", Name: "Role Adapter"}); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	role := projectwork.AgentRoleProfile{
		ID:                  "developer",
		ProjectID:           "proj_roles",
		Name:                "Developer",
		Description:         "Implements scoped changes.",
		Instructions:        "Keep changes small.",
		DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
		DefaultProvider:     "openai",
		DefaultModel:        "gpt-5",
		DefaultAgentProfile: "implementation",
		SkillIDs:            []string{"backend", "backend"},
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	created, err := UpsertRole(ctx, service, role)
	if err != nil {
		t.Fatalf("UpsertRole(create) error = %v", err)
	}
	if created.ID != "developer" || created.DefaultProfileID != "implementation" || created.DefaultExecutionProfileID != roleExecutionProfileID(role) || created.DefaultExecutionMode != cairnline.ExecutionOrchestrated {
		t.Fatalf("created role = %+v, want mapped role defaults", created)
	}
	if len(created.DefaultSkillIDs) != 1 || created.DefaultSkillIDs[0] != "backend" {
		t.Fatalf("created role skill ids = %+v, want compacted backend skill", created.DefaultSkillIDs)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, roleExecutionProfileID(role)); profile == nil || profile.AgentKind != "hecate" || profile.ProviderHint != "openai" || profile.ModelHint != "gpt-5" {
		t.Fatalf("role execution profile = %+v, want role-level execution defaults", profile)
	}

	role.Name = "Developer Updated"
	role.DefaultProvider = ""
	role.DefaultModel = ""
	role.SkillIDs = []string{"frontend"}
	updated, err := UpsertRole(ctx, service, role)
	if err != nil {
		t.Fatalf("UpsertRole(update) error = %v", err)
	}
	if updated.Name != "Developer Updated" || updated.DefaultExecutionProfileID != "" || len(updated.DefaultSkillIDs) != 1 || updated.DefaultSkillIDs[0] != "frontend" {
		t.Fatalf("updated role = %+v, want updated role and removed execution default", updated)
	}
	executionProfiles, err = service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() after update error = %v", err)
	}
	if profile := findCairnlineExecutionProfile(executionProfiles, roleExecutionProfileIDValue(role)); profile != nil {
		t.Fatalf("role execution profile = %+v, want removed after role defaults cleared", profile)
	}

	if err := DeleteRole(ctx, service, role); err != nil {
		t.Fatalf("DeleteRole() error = %v", err)
	}
	roles, err := service.ListRoles(ctx, "proj_roles")
	if err != nil {
		t.Fatalf("ListRoles() error = %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("roles after delete = %+v, want empty", roles)
	}
}

func TestUpsertWorkItemMirrorsWorkItemMutations(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	project := projects.Project{
		ID:   "proj_work_items",
		Name: "Work Adapter",
		Roots: []projects.Root{
			{ID: "root_main", Path: "/tmp/hecate-work", Kind: "git", Active: true},
			{ID: "root_docs", Path: "/tmp/hecate-docs", Kind: "folder", Active: true},
		},
		DefaultRootID: "root_main",
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	item := projectwork.WorkItem{
		ID:              "work_docs",
		ProjectID:       "proj_work_items",
		Title:           "Document adapter",
		Brief:           "Write the bridge docs.",
		Status:          projectwork.WorkItemStatusBacklog,
		Priority:        "high",
		OwnerRoleID:     "writer",
		RootID:          "root_main",
		ReviewerRoleIDs: []string{"reviewer", "reviewer"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	created, err := UpsertWorkItem(ctx, service, item)
	if err != nil {
		t.Fatalf("UpsertWorkItem(create) error = %v", err)
	}
	if created.ID != item.ID || created.Status != projectwork.WorkItemStatusBacklog || created.Priority != "high" || created.RootID != "root_main" || len(created.ReviewerRoleIDs) != 1 || created.ReviewerRoleIDs[0] != "reviewer" {
		t.Fatalf("created work item = %+v, want mapped Hecate work item metadata", created)
	}
	mirroredRoles, err := service.ListRoles(ctx, item.ProjectID)
	if err != nil {
		t.Fatalf("ListRoles() error = %v", err)
	}
	if !hasCairnlineRole(mirroredRoles, "writer") || !hasCairnlineRole(mirroredRoles, "reviewer") {
		t.Fatalf("mirrored roles = %+v, want work item role placeholders", mirroredRoles)
	}

	item.Title = "Document adapter updated"
	item.Status = projectwork.WorkItemStatusReady
	item.Priority = "normal"
	item.RootID = "root_docs"
	item.ReviewerRoleIDs = []string{"reviewer"}
	updated, err := UpsertWorkItem(ctx, service, item)
	if err != nil {
		t.Fatalf("UpsertWorkItem(update) error = %v", err)
	}
	if updated.Title != "Document adapter updated" || updated.Status != projectwork.WorkItemStatusReady || updated.RootID != "root_docs" || len(updated.ReviewerRoleIDs) != 1 {
		t.Fatalf("updated work item = %+v, want updated work item metadata", updated)
	}

	if err := DeleteWorkItem(ctx, service, item.ProjectID, item.ID); err != nil {
		t.Fatalf("DeleteWorkItem() error = %v", err)
	}
	if _, err := service.GetWorkItem(ctx, item.ProjectID, item.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetWorkItem() error = %v, want cairnline.ErrNotFound", err)
	}
}

func TestUpsertAssignmentCreatesAndSyncsLifecycle(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 12, 30, 0, 0, time.UTC)
	project := projects.Project{
		ID:   "proj_assignments",
		Name: "Assignment Adapter",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-assignment",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	profileID := "implementation"
	role := projectwork.AgentRoleProfile{
		ID:                  "developer",
		ProjectID:           project.ID,
		Name:                "Developer",
		DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
		DefaultProvider:     "openai",
		DefaultModel:        "gpt-5",
		DefaultAgentProfile: profileID,
		SkillIDs:            []string{"backend"},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if _, err := UpsertRole(ctx, service, role); err != nil {
		t.Fatalf("UpsertRole() error = %v", err)
	}
	workItem := projectwork.WorkItem{
		ID:        "work_bridge",
		ProjectID: project.ID,
		Title:     "Bridge assignments",
		Status:    projectwork.WorkItemStatusReady,
		RootID:    "root_main",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertWorkItem(ctx, service, workItem); err != nil {
		t.Fatalf("UpsertWorkItem() error = %v", err)
	}
	assignment := projectwork.Assignment{
		ID:         "asgn_bridge",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     role.ID,
		RootID:     "root_main",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
		ExecutionRef: projectwork.AssignmentExecutionRef{
			ContextSnapshotID: "ctx_queued",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	queued, err := UpsertAssignment(ctx, service, assignment, role)
	if err != nil {
		t.Fatalf("UpsertAssignment(queued) error = %v", err)
	}
	if queued.Status != cairnline.AssignmentQueued || queued.ProfileID != profileID || queued.ExecutionProfileID != roleExecutionProfileID(role) || queued.ExecutionMode != cairnline.ExecutionOrchestrated || queued.ContextSnapshotID != "ctx_queued" {
		t.Fatalf("queued assignment = %+v, want created orchestrated assignment metadata", queued)
	}
	if queued.DesiredAgent.Kind != "hecate" || len(queued.DesiredAgent.SkillIDs) != 1 || queued.DesiredAgent.SkillIDs[0] != "backend" {
		t.Fatalf("queued desired agent = %+v, want Hecate desired agent with role skill ids", queued.DesiredAgent)
	}

	followUpWork := projectwork.WorkItem{
		ID:        "work_followup",
		ProjectID: project.ID,
		Title:     "Follow up assignments",
		Status:    projectwork.WorkItemStatusReady,
		RootID:    "root_main",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertWorkItem(ctx, service, followUpWork); err != nil {
		t.Fatalf("UpsertWorkItem(follow up) error = %v", err)
	}
	externalRole := projectwork.AgentRoleProfile{
		ID:                  "external_reviewer",
		ProjectID:           project.ID,
		Name:                "External Reviewer",
		DefaultDriverKind:   projectwork.AssignmentDriverExternalAgent,
		DefaultAgentProfile: profileID,
		SkillIDs:            []string{"review", "review"},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if _, err := UpsertRole(ctx, service, externalRole); err != nil {
		t.Fatalf("UpsertRole(external reviewer) error = %v", err)
	}
	assignment.WorkItemID = followUpWork.ID
	assignment.RoleID = externalRole.ID
	assignment.DriverKind = projectwork.AssignmentDriverExternalAgent
	assignment.ExecutionRef = projectwork.AssignmentExecutionRef{ContextSnapshotID: "ctx_updated"}
	updatedQueued, err := UpsertAssignment(ctx, service, assignment, externalRole)
	if err != nil {
		t.Fatalf("UpsertAssignment(update queued metadata) error = %v", err)
	}
	if updatedQueued.Status != cairnline.AssignmentQueued || updatedQueued.WorkItemID != followUpWork.ID || updatedQueued.RoleID != externalRole.ID || updatedQueued.ExecutionMode != cairnline.ExecutionExternalAdapter || updatedQueued.ContextSnapshotID != "ctx_updated" {
		t.Fatalf("updated queued assignment = %+v, want retargeted queued assignment metadata", updatedQueued)
	}
	if !updatedQueued.CreatedAt.Equal(queued.CreatedAt) {
		t.Fatalf("updated queued created_at = %s, want preserved %s", updatedQueued.CreatedAt, queued.CreatedAt)
	}
	if updatedQueued.DesiredAgent.Kind != cairnline.DesiredAgentAny || len(updatedQueued.DesiredAgent.SkillIDs) != 1 || updatedQueued.DesiredAgent.SkillIDs[0] != "review" {
		t.Fatalf("updated queued desired agent = %+v, want external role skill metadata", updatedQueued.DesiredAgent)
	}

	if _, err := service.ClaimAssignment(ctx, assignment.ProjectID, assignment.ID, "external_adapter"); err != nil {
		t.Fatalf("ClaimAssignment(pre-dispatch) error = %v", err)
	}
	assignment.ExecutionRef = projectwork.AssignmentExecutionRef{}
	assignment.StartedAt = time.Time{}
	assignment.CompletedAt = time.Time{}
	releasedQueued, err := UpsertAssignment(ctx, service, assignment, externalRole)
	if err != nil {
		t.Fatalf("UpsertAssignment(release queued) error = %v", err)
	}
	if releasedQueued.Status != cairnline.AssignmentQueued || releasedQueued.ClaimedBy != "" || releasedQueued.ExecutionRef != "" || releasedQueued.ContextSnapshotID != "" || !releasedQueued.StartedAt.IsZero() || !releasedQueued.CompletedAt.IsZero() {
		t.Fatalf("released queued assignment = %+v, want claimed assignment released for retry", releasedQueued)
	}

	assignment.Status = projectwork.AssignmentStatusRunning
	assignment.ExecutionRef = projectwork.AssignmentExecutionRef{TaskID: "task_1", RunID: "run_1", ContextSnapshotID: "ctx_running"}
	assignment.StartedAt = now.Add(5 * time.Minute)
	running, err := UpsertAssignment(ctx, service, assignment, externalRole)
	if err != nil {
		t.Fatalf("UpsertAssignment(running) error = %v", err)
	}
	if running.Status != cairnline.AssignmentRunning || running.ClaimedBy != "external_adapter" || running.ExecutionRef != "run_1" || running.WorkItemID != followUpWork.ID || running.RoleID != externalRole.ID || !running.StartedAt.Equal(assignment.StartedAt) {
		t.Fatalf("running assignment = %+v, want claimed running assignment", running)
	}
	if _, err := UpsertAssignment(ctx, service, assignment, externalRole); err != nil {
		t.Fatalf("UpsertAssignment(running idempotent) error = %v", err)
	}

	assignment.Status = projectwork.AssignmentStatusCompleted
	assignment.CompletedAt = now.Add(10 * time.Minute)
	completed, err := UpsertAssignment(ctx, service, assignment, externalRole)
	if err != nil {
		t.Fatalf("UpsertAssignment(completed) error = %v", err)
	}
	if completed.Status != cairnline.AssignmentCompleted || completed.ExecutionRef != "run_1" || !completed.StartedAt.Equal(assignment.StartedAt) || !completed.CompletedAt.Equal(assignment.CompletedAt) {
		t.Fatalf("completed assignment = %+v, want completed assignment with execution ref", completed)
	}
	if _, err := UpsertAssignment(ctx, service, assignment, externalRole); err != nil {
		t.Fatalf("UpsertAssignment(completed idempotent) error = %v", err)
	}

	if err := DeleteAssignment(ctx, service, assignment.ProjectID, assignment.ID); err != nil {
		t.Fatalf("DeleteAssignment() error = %v", err)
	}
	if _, err := service.GetAssignment(ctx, assignment.ProjectID, assignment.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetAssignment() error = %v, want cairnline.ErrNotFound", err)
	}
}

func TestRecordCollaborationArtifactsAndUpsertHandoff(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 13, 0, 0, 0, time.UTC)
	project := projects.Project{
		ID:   "proj_collab",
		Name: "Collaboration Adapter",
		Roots: []projects.Root{{
			ID:     "root_main",
			Path:   "/tmp/hecate-collab",
			Kind:   "git",
			Active: true,
		}},
		DefaultRootID: "root_main",
	}
	if _, err := UpsertProject(ctx, service, project); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	role := projectwork.AgentRoleProfile{
		ID:                  "developer",
		ProjectID:           project.ID,
		Name:                "Developer",
		DefaultDriverKind:   projectwork.AssignmentDriverHecateTask,
		DefaultAgentProfile: "implementation",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if _, err := UpsertRole(ctx, service, role); err != nil {
		t.Fatalf("UpsertRole() error = %v", err)
	}
	workItem := projectwork.WorkItem{
		ID:        "work_collab",
		ProjectID: project.ID,
		Title:     "Record collaboration",
		Status:    projectwork.WorkItemStatusReady,
		RootID:    "root_main",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if _, err := UpsertWorkItem(ctx, service, workItem); err != nil {
		t.Fatalf("UpsertWorkItem() error = %v", err)
	}
	assignment := projectwork.Assignment{
		ID:         "asgn_collab",
		ProjectID:  project.ID,
		WorkItemID: workItem.ID,
		RoleID:     role.ID,
		RootID:     "root_main",
		DriverKind: projectwork.AssignmentDriverHecateTask,
		Status:     projectwork.AssignmentStatusQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if _, err := UpsertAssignment(ctx, service, assignment, role); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}

	generic := projectwork.CollaborationArtifact{
		ID:           "art_decision",
		ProjectID:    project.ID,
		WorkItemID:   workItem.ID,
		AssignmentID: assignment.ID,
		Kind:         projectwork.ArtifactKindDecisionNote,
		Title:        "Decision",
		Body:         "Keep the bridge seams non-authoritative.",
		AuthorRoleID: role.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	recordedArtifact, err := RecordArtifact(ctx, service, generic)
	if err != nil {
		t.Fatalf("RecordArtifact() error = %v", err)
	}
	if recordedArtifact.ID != generic.ID || recordedArtifact.AssignmentID != assignment.ID || recordedArtifact.AuthorRoleID != role.ID || recordedArtifact.Body != generic.Body {
		t.Fatalf("recorded artifact = %+v, want generic collaboration artifact metadata", recordedArtifact)
	}
	generic.Body = "Replay should not mutate create-only artifacts."
	replayedArtifact, err := RecordArtifact(ctx, service, generic)
	if err != nil {
		t.Fatalf("RecordArtifact(replay) error = %v", err)
	}
	if replayedArtifact.Body != recordedArtifact.Body {
		t.Fatalf("replayed artifact body = %q, want original create-only body %q", replayedArtifact.Body, recordedArtifact.Body)
	}

	evidenceArtifact := projectwork.CollaborationArtifact{
		ID:                 "art_evidence",
		ProjectID:          project.ID,
		WorkItemID:         workItem.ID,
		AssignmentID:       assignment.ID,
		Kind:               projectwork.ArtifactKindEvidenceLink,
		Title:              "CI run",
		Body:               "Tests passed.",
		EvidenceSourceKind: "pull_request",
		EvidenceURL:        "https://github.com/hecatehq/hecate/actions/runs/42",
		EvidenceExternalID: "PR 42",
		EvidenceProvider:   "github",
		EvidenceTrustLabel: projectwork.EvidenceTrustOperatorProvided,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	evidence, err := RecordEvidence(ctx, service, evidenceArtifact)
	if err != nil {
		t.Fatalf("RecordEvidence() error = %v", err)
	}
	if evidence.ID != "art_evidence" || evidence.AssignmentID != assignment.ID || evidence.Locator != evidenceArtifact.EvidenceURL || evidence.SourceKind != "pull_request" || evidence.ExternalID != "PR 42" || evidence.Provider != "github" || evidence.TrustLabel != projectwork.EvidenceTrustOperatorProvided {
		t.Fatalf("recorded evidence = %+v, want evidence link metadata", evidence)
	}

	reviewArtifact := projectwork.CollaborationArtifact{
		ID:                   "art_review",
		ProjectID:            project.ID,
		WorkItemID:           workItem.ID,
		AssignmentID:         assignment.ID,
		Kind:                 projectwork.ArtifactKindReview,
		Title:                "Review",
		Body:                 "Needs one follow-up.",
		AuthorRoleID:         role.ID,
		ReviewedAssignmentID: assignment.ID,
		ReviewVerdict:        projectwork.ReviewVerdictChangesRequested,
		ReviewRisk:           projectwork.ReviewRiskHigh,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	review, err := RecordReview(ctx, service, reviewArtifact)
	if err != nil {
		t.Fatalf("RecordReview() error = %v", err)
	}
	if review.ID != "art_review" || review.AssignmentID != assignment.ID || review.ReviewerRoleID != role.ID || review.Verdict != cairnline.ReviewVerdictChangesRequested || review.Risk != cairnline.ReviewRiskHigh {
		t.Fatalf("recorded review = %+v, want portable review metadata", review)
	}

	handoff := projectwork.Handoff{
		ID:                    "handoff_review",
		ProjectID:             project.ID,
		WorkItemID:            workItem.ID,
		SourceAssignmentID:    assignment.ID,
		SourceRunID:           "run_collab",
		TargetRoleID:          role.ID,
		Title:                 "Review follow-up",
		Summary:               "Please address the review concern.",
		RecommendedNextAction: "Create a follow-up assignment.",
		LinkedArtifactIDs:     []string{"art_review", "art_review"},
		ContextRefs:           []string{"ctx_1"},
		Status:                projectwork.HandoffStatusPending,
		ProvenanceKind:        "operator",
		TrustLabel:            "operator_reviewed",
		CreatedByRoleID:       role.ID,
		CreatedAt:             now,
		UpdatedAt:             now,
		StatusChangedAt:       now.Add(-time.Minute),
	}
	createdHandoff, err := UpsertHandoff(ctx, service, handoff)
	if err != nil {
		t.Fatalf("UpsertHandoff(create) error = %v", err)
	}
	if createdHandoff.ID != handoff.ID || createdHandoff.SourceAssignmentID != assignment.ID || createdHandoff.FromRoleID != role.ID || createdHandoff.ToRoleID != role.ID || len(createdHandoff.LinkedArtifactIDs) != 1 || createdHandoff.Status != cairnline.HandoffStatusOpen || !createdHandoff.StatusChangedAt.Equal(handoff.StatusChangedAt) {
		t.Fatalf("created handoff = %+v, want mapped handoff metadata", createdHandoff)
	}

	handoff.Status = projectwork.HandoffStatusAccepted
	handoff.TargetAssignmentID = assignment.ID
	handoff.TargetWorkItemID = workItem.ID
	handoff.Summary = "Accepted follow-up."
	handoff.StatusChangedAt = now.Add(2 * time.Minute)
	updatedHandoff, err := UpsertHandoff(ctx, service, handoff)
	if err != nil {
		t.Fatalf("UpsertHandoff(update) error = %v", err)
	}
	if updatedHandoff.Status != cairnline.HandoffStatusAccepted || updatedHandoff.TargetAssignmentID != assignment.ID || updatedHandoff.TargetWorkItemID != workItem.ID || updatedHandoff.Body != "Accepted follow-up." || !updatedHandoff.StatusChangedAt.Equal(handoff.StatusChangedAt) {
		t.Fatalf("updated handoff = %+v, want accepted handoff update", updatedHandoff)
	}

	if err := DeleteHandoff(ctx, service, handoff.ProjectID, handoff.WorkItemID, handoff.ID); err != nil {
		t.Fatalf("DeleteHandoff() error = %v", err)
	}
	if _, err := service.GetHandoff(ctx, handoff.ProjectID, handoff.WorkItemID, handoff.ID); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetHandoff() error = %v, want cairnline.ErrNotFound", err)
	}
}

func TestUpsertProjectSkillMirrorsSkillMetadata(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)
	if _, err := UpsertProject(ctx, service, projects.Project{
		ID:   "proj_skills",
		Name: "Skill Adapter",
	}); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}

	skill := projectskills.Skill{
		ID:                     "backend",
		ProjectID:              "proj_skills",
		Title:                  "Backend",
		Description:            "Server implementation guidance.",
		Path:                   ".agents/skills/backend/SKILL.md",
		RootID:                 "root_main",
		Format:                 projectskills.FormatSkillMD,
		SuggestedTools:         []string{"git.diff", "file.read"},
		RequiredPermissions:    projectskills.RequiredPermissions{Writes: boolPtrForTest(false)},
		Enabled:                false,
		Status:                 projectskills.StatusAvailable,
		TrustLabel:             projectskills.TrustWorkspaceSkill,
		SourceContextSourceIDs: []string{"ctx_agents"},
		Warnings:               []string{"Review before use."},
		DiscoveredAt:           now,
		CreatedAt:              now.Add(-time.Hour),
		UpdatedAt:              now,
	}

	created, err := UpsertProjectSkill(ctx, service, skill)
	if err != nil {
		t.Fatalf("UpsertProjectSkill(create) error = %v", err)
	}
	if created.ID != "backend" || created.ProjectID != "proj_skills" || created.Title != "Backend" || created.Path != ".agents/skills/backend/SKILL.md" || created.RootID != "root_main" {
		t.Fatalf("created skill = %+v, want portable skill metadata", created)
	}
	if created.Enabled {
		t.Fatalf("created skill enabled = true, want disabled operator override preserved")
	}
	if created.Status != cairnline.SkillStatusAvailable || created.TrustLabel != cairnline.SkillTrustWorkspace || len(created.SourceRefs) != 1 || created.SourceRefs[0] != "ctx_agents" || len(created.Warnings) != 1 {
		t.Fatalf("created skill = %+v, want status/trust/provenance/warnings", created)
	}
	if len(created.SuggestedTools) != 2 || created.RequiredPermissions.Writes == nil || *created.RequiredPermissions.Writes {
		t.Fatalf("created skill capability hints = %+v / %+v, want mapped tools and permissions", created.SuggestedTools, created.RequiredPermissions)
	}

	skill.Title = "Backend Lead"
	skill.Description = "Operator edited description."
	skill.SuggestedTools = []string{"shell.exec"}
	skill.RequiredPermissions = projectskills.RequiredPermissions{Network: boolPtrForTest(false)}
	skill.Enabled = true
	skill.Status = projectskills.StatusConflict
	skill.TrustLabel = "operator_curated_skill"
	skill.Warnings = []string{"Duplicate skill id."}
	updated, err := UpsertProjectSkill(ctx, service, skill)
	if err != nil {
		t.Fatalf("UpsertProjectSkill(update) error = %v", err)
	}
	if updated.Title != "Backend Lead" || updated.Description != "Operator edited description." || !updated.Enabled || updated.Status != cairnline.SkillStatusConflict || updated.TrustLabel != "operator_curated_skill" {
		t.Fatalf("updated skill = %+v, want operator-edited metadata", updated)
	}
	if len(updated.SuggestedTools) != 1 || updated.SuggestedTools[0] != "shell.exec" || updated.RequiredPermissions.Network == nil || *updated.RequiredPermissions.Network {
		t.Fatalf("updated skill capability hints = %+v / %+v, want updated tools and permissions", updated.SuggestedTools, updated.RequiredPermissions)
	}
	if updated.CreatedAt.IsZero() || !updated.DiscoveredAt.Equal(now) {
		t.Fatalf("updated skill times = created %s discovered %s, want preserved created and Hecate discovered time", updated.CreatedAt, updated.DiscoveredAt)
	}
}

func TestUpsertProjectSkillsMirrorsBatch(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	if _, err := UpsertProject(ctx, service, projects.Project{
		ID:   "proj_skill_batch",
		Name: "Skill Batch",
	}); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	written, err := UpsertProjectSkills(ctx, service, []projectskills.Skill{
		{
			ID:        "backend",
			ProjectID: "proj_skill_batch",
			Title:     "Backend",
			Path:      ".agents/skills/backend/SKILL.md",
			Format:    projectskills.FormatSkillMD,
			Enabled:   true,
			Status:    projectskills.StatusAvailable,
		},
		{
			ID:        "frontend",
			ProjectID: "proj_skill_batch",
			Title:     "Frontend",
			Path:      ".agents/skills/frontend/SKILL.md",
			Format:    projectskills.FormatSkillMD,
			Enabled:   true,
			Status:    projectskills.StatusAvailable,
		},
	})
	if err != nil {
		t.Fatalf("UpsertProjectSkills() error = %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("written skills = %+v, want two upserted skills", written)
	}
	items, err := service.ListProjectSkills(ctx, "proj_skill_batch")
	if err != nil {
		t.Fatalf("ListProjectSkills() error = %v", err)
	}
	if len(items) != 2 || findCairnlineProjectSkill(items, "backend") == nil || findCairnlineProjectSkill(items, "frontend") == nil {
		t.Fatalf("project skills = %+v, want backend and frontend", items)
	}
}

func TestUpsertMemoryEntryMirrorsMemoryMetadata(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	if _, err := UpsertProject(ctx, service, projects.Project{
		ID:   "proj_memory",
		Name: "Memory Adapter",
	}); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}

	entry := memory.Entry{
		ID:         "mem_backend",
		Scope:      memory.ScopeProject,
		ProjectID:  "proj_memory",
		Title:      "Backend Memory",
		Body:       "Prefer bounded bridge seams before route switches.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		SourceID:   "operator_note",
		Enabled:    false,
	}
	created, err := UpsertMemoryEntry(ctx, service, entry)
	if err != nil {
		t.Fatalf("UpsertMemoryEntry(create) error = %v", err)
	}
	if created.ID != "mem_backend" || created.ProjectID != "proj_memory" || created.Title != "Backend Memory" || created.Body != entry.Body || created.TrustLabel != memory.TrustLabelOperatorMemory || created.SourceKind != memory.SourceKindOperator || created.SourceID != "operator_note" {
		t.Fatalf("created memory = %+v, want Hecate memory metadata", created)
	}
	if created.Enabled {
		t.Fatalf("created memory enabled = true, want disabled Hecate memory preserved")
	}

	entry.Title = "Backend Memory Updated"
	entry.Body = "Now routed through the reusable memory seam."
	entry.Enabled = true
	entry.SourceKind = "handoff"
	entry.SourceID = "handoff_1"
	updated, err := UpsertMemoryEntry(ctx, service, entry)
	if err != nil {
		t.Fatalf("UpsertMemoryEntry(update) error = %v", err)
	}
	if updated.Title != "Backend Memory Updated" || updated.Body != entry.Body || !updated.Enabled || updated.SourceKind != "handoff" || updated.SourceID != "handoff_1" {
		t.Fatalf("updated memory = %+v, want updated Hecate memory metadata", updated)
	}
	if err := DeleteMemoryEntry(ctx, service, "proj_memory", "mem_backend"); err != nil {
		t.Fatalf("DeleteMemoryEntry() error = %v", err)
	}
	if _, err := service.GetMemoryEntry(ctx, "proj_memory", "mem_backend"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetMemoryEntry() error = %v, want cairnline.ErrNotFound", err)
	}
}

func TestUpsertMemoryCandidateMirrorsResolvedState(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	if _, err := UpsertProject(ctx, service, projects.Project{
		ID:   "proj_memory_candidates",
		Name: "Memory Candidate Adapter",
	}); err != nil {
		t.Fatalf("UpsertProject() error = %v", err)
	}
	if _, err := UpsertMemoryEntry(ctx, service, memory.Entry{
		ID:         "mem_promoted",
		Scope:      memory.ScopeProject,
		ProjectID:  "proj_memory_candidates",
		Title:      "Promoted Memory",
		Body:       "Use the promoted entry id generated by Hecate.",
		TrustLabel: memory.TrustLabelGenerated,
		SourceKind: "handoff",
		SourceID:   "handoff_memory",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("UpsertMemoryEntry(promoted) error = %v", err)
	}

	candidate := memory.Candidate{
		ID:                  "memcand_promoted",
		ProjectID:           "proj_memory_candidates",
		Title:               "Candidate",
		Body:                "Candidate body.",
		SuggestedKind:       "project_pattern",
		SuggestedTrustLabel: memory.TrustLabelGenerated,
		SuggestedSourceKind: "handoff",
		SuggestedSourceID:   "handoff_memory",
		SourceRefs: []memory.CandidateSourceRef{{
			Kind:  "handoff",
			ID:    "handoff_memory",
			Title: "Memory handoff",
			URL:   "https://example.test/handoff",
		}},
		Status:           memory.CandidateStatusPromoted,
		PromotedMemoryID: "mem_promoted",
	}
	created, err := UpsertMemoryCandidate(ctx, service, candidate)
	if err != nil {
		t.Fatalf("UpsertMemoryCandidate(create promoted) error = %v", err)
	}
	if created.Status != cairnline.MemoryCandidatePromoted || created.PromotedMemoryID != "mem_promoted" || created.SuggestedKind != "project_pattern" || created.SuggestedTrustLabel != memory.TrustLabelGenerated || len(created.SourceRefs) != 1 || created.SourceRefs[0].ID != "handoff_memory" {
		t.Fatalf("created candidate = %+v, want promoted Hecate candidate metadata", created)
	}

	candidate.Status = memory.CandidateStatusRejected
	candidate.StatusReason = "Too speculative"
	candidate.PromotedMemoryID = ""
	updated, err := UpsertMemoryCandidate(ctx, service, candidate)
	if err != nil {
		t.Fatalf("UpsertMemoryCandidate(update rejected) error = %v", err)
	}
	if updated.Status != cairnline.MemoryCandidateRejected || updated.StatusReason != "Too speculative" || updated.PromotedMemoryID != "" {
		t.Fatalf("updated candidate = %+v, want rejected Hecate candidate state", updated)
	}
	if err := DeleteMemoryCandidate(ctx, service, "proj_memory_candidates", "memcand_promoted"); err != nil {
		t.Fatalf("DeleteMemoryCandidate() error = %v", err)
	}
	if _, err := service.GetMemoryCandidate(ctx, "proj_memory_candidates", "memcand_promoted"); !errors.Is(err, cairnline.ErrNotFound) {
		t.Fatalf("GetMemoryCandidate() error = %v, want cairnline.ErrNotFound", err)
	}
}

func TestSeedSnapshotsMirrorsMultipleProjectsWithPresetHintsOnly(t *testing.T) {
	ctx := context.Background()
	service := cairnline.NewMemoryService()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	snapshots := []Snapshot{{
		Project: projects.Project{
			ID:                  "proj_one",
			Name:                "One",
			DefaultAgentProfile: "shared_architect",
			DefaultProvider:     "openai",
			DefaultModel:        "gpt-5",
			CreatedAt:           now,
			UpdatedAt:           now,
		},
	}, {
		Project: projects.Project{
			ID:                  "proj_two",
			Name:                "Two",
			DefaultAgentProfile: "shared_architect",
			CreatedAt:           now,
			UpdatedAt:           now,
		},
	}}

	if err := SeedSnapshots(ctx, service, snapshots); err != nil {
		t.Fatalf("SeedSnapshots() error = %v", err)
	}
	projects, err := service.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v, want two seeded projects", projects)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		t.Fatalf("ListExecutionProfiles() error = %v", err)
	}
	if len(executionProfiles) != 1 || executionProfiles[0].ID != projectExecutionProfileID(snapshots[0].Project) {
		t.Fatalf("execution profiles = %+v, want only project execution defaults", executionProfiles)
	}
	for _, item := range projects {
		if item.DefaultProfileID != "shared_architect" {
			t.Fatalf("project = %+v, want opaque preset hint preserved", item)
		}
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
	if packet.Project.ID != snapshot.Project.ID || packet.Project.DefaultRootID != "root_main" || packet.Assignment.RootID != "root_main" {
		t.Fatalf("reopened packet project/assignment = %+v/%+v, want persisted default root and root-scoped launch packet", packet.Project, packet.Assignment)
	}
	if len(packet.Project.ContextSources) != 1 || packet.Project.ContextSources[0].Metadata["root_id"] != "root_main" || packet.Project.ContextSources[0].Format != "agents_md" {
		t.Fatalf("reopened packet project sources = %+v, want persisted context-source metadata", packet.Project.ContextSources)
	}
	if len(packet.Evidence) != 1 || len(packet.Reviews) != 1 || len(packet.Handoffs) != 1 || len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
		t.Fatalf("reopened launch packet collaboration counts evidence=%d reviews=%d handoffs=%d memory_entries=%d memory_candidates=%d, want all one", len(packet.Evidence), len(packet.Reviews), len(packet.Handoffs), len(packet.Memory), len(packet.MemoryCandidates))
	}
	proposals, err := reopened.ListAssistantProposals(ctx, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("reopened ListAssistantProposals() error = %v", err)
	}
	if len(proposals) != 1 || proposals[0].ID != "pa_bridge" || proposals[0].Status != cairnline.AssistantProposalStatusApplied || proposals[0].LatestResult == nil || len(proposals[0].ApplyAttempts) != 1 || proposals[0].AppliedAt == nil {
		t.Fatalf("reopened assistant proposals = %+v, want persisted imported proposal ledger", proposals)
	}
	if len(proposals[0].Proposal.Warnings) != 1 || proposals[0].Proposal.Warnings[0] != "Review generated follow-up scope before applying." {
		t.Fatalf("reopened assistant proposal warnings = %+v, want persisted warnings", proposals[0].Proposal.Warnings)
	}
	if packet.Evidence[0].AssignmentID != "asgn_external" {
		t.Fatalf("reopened evidence = %+v, want persisted assignment-scoped evidence", packet.Evidence[0])
	}
	if packet.Handoffs[0].SourceAssignmentID != "asgn_external" || packet.Handoffs[0].TargetAssignmentID != "asgn_bridge" || len(packet.Handoffs[0].LinkedArtifactIDs) != 2 || packet.Handoffs[0].TrustLabel != "operator_reviewed" {
		t.Fatalf("reopened handoff = %+v, want persisted structured refs and provenance", packet.Handoffs[0])
	}
	readiness, err := reopened.WorkItemCloseoutReadiness(ctx, snapshot.Project.ID, "work_bridge")
	if err != nil {
		t.Fatalf("reopened WorkItemCloseoutReadiness() error = %v", err)
	}
	if readiness.CompletedAssignments != 1 || len(readiness.MissingEvidenceAssignmentIDs) != 0 {
		t.Fatalf("reopened readiness = %+v, want completed external assignment covered by persisted evidence", readiness)
	}
	brief, err := reopened.ProjectOperationsBrief(ctx, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("reopened ProjectOperationsBrief() error = %v", err)
	}
	if brief.Counts.Assignments != 2 || brief.Counts.ActiveAssignments != 0 || brief.Counts.BlockedAssignments != 1 || brief.Counts.PendingMemoryCandidates != 1 || brief.Counts.OpenHandoffs != 1 {
		t.Fatalf("reopened operations counts = %+v, want persisted operations parity", brief.Counts)
	}
	activity, err := reopened.ProjectActivity(ctx, snapshot.Project.ID)
	if err != nil {
		t.Fatalf("reopened ProjectActivity() error = %v", err)
	}
	if activity.Counts.Assignments != 2 || activity.Counts.Active != 0 || activity.Counts.Blocked != 1 || activity.Counts.Completed != 1 || len(activity.Buckets.Recent) != 2 {
		t.Fatalf("reopened activity = %+v, want persisted activity parity", activity)
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
	defaultToolsEnabled := true
	defaultCompactOutput := true
	return Snapshot{
		Project: projects.Project{
			ID:                       "proj_hecate",
			Name:                     "Hecate",
			Description:              "Local AI operations console.",
			DefaultProvider:          "ollama",
			DefaultModel:             "qwen3-coder",
			DefaultAgentProfile:      "bridge_implementation",
			DefaultToolsEnabled:      &defaultToolsEnabled,
			DefaultWorkspaceMode:     "worktree",
			DefaultSystemPrompt:      "Stay crisp.",
			DefaultCompactToolOutput: &defaultCompactOutput,
			Roots: []projects.Root{{
				ID:        "root_main",
				Path:      "/Users/alice/dev/hecate",
				Kind:      "local",
				GitRemote: "git@github.com:hecatehq/hecate.git",
				GitBranch: "main",
				Active:    true,
			}},
			DefaultRootID: "root_main",
			ContextSources: []projects.ContextSource{{
				ID:             "src_agents",
				Kind:           "workspace_instruction",
				Title:          "AGENTS.md",
				Path:           "AGENTS.md",
				Enabled:        true,
				Format:         "agents_md",
				Scope:          "workspace",
				TrustLabel:     "workspace_guidance",
				SourceCategory: "workspace_guidance",
				Metadata:       map[string]string{"root_id": "root_main"},
				CreatedAt:      now,
				UpdatedAt:      now,
			}},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Skills: []projectskills.Skill{{
			ID:                     "backend",
			ProjectID:              "proj_hecate",
			Title:                  "Backend",
			Description:            "Backend implementation guidance.",
			Path:                   "docs-ai/skills/backend/SKILL.md",
			RootID:                 "root_main",
			Format:                 projectskills.FormatSkillMD,
			SuggestedTools:         []string{"git.diff", "file.read"},
			RequiredPermissions:    projectskills.RequiredPermissions{Writes: boolPtrForTest(false)},
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
			DefaultProvider:     "openai",
			DefaultModel:        "gpt-5",
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
			ID:           "art_decision",
			ProjectID:    "proj_hecate",
			WorkItemID:   "work_bridge",
			AssignmentID: "asgn_bridge",
			Kind:         projectwork.ArtifactKindDecisionNote,
			Title:        "Bridge decision",
			Body:         "Keep generic collaboration artifacts portable.",
			AuthorRoleID: "bridge_developer",
			CreatedAt:    now,
			UpdatedAt:    now,
		}, {
			ID:                 "art_evidence",
			ProjectID:          "proj_hecate",
			WorkItemID:         "work_bridge",
			AssignmentID:       "asgn_external",
			Kind:               projectwork.ArtifactKindEvidenceLink,
			Title:              "CI run",
			Body:               "Focused bridge tests passed.",
			EvidenceSourceKind: "pull_request",
			EvidenceURL:        "https://github.com/hecatehq/hecate/actions/runs/123",
			EvidenceExternalID: "PR 123",
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
			SourceRunID:           "run_external",
			SourceChatSessionID:   "chat_123",
			SourceMessageID:       "msg_123",
			TargetRoleID:          "bridge_reviewer",
			TargetAssignmentID:    "asgn_bridge",
			TargetWorkItemID:      "work_bridge",
			Title:                 "Review bridge parity",
			Summary:               "Artifact parity is ready for review.",
			RecommendedNextAction: "Verify launch packets include evidence, reviews, handoffs, and memory candidates.",
			LinkedArtifactIDs:     []string{"art_evidence", "art_review"},
			LinkedMemoryIDs:       []string{"memcand_bridge"},
			ContextRefs:           []string{"ctx_123"},
			Status:                projectwork.HandoffStatusPending,
			ProvenanceKind:        "agent_draft",
			TrustLabel:            "operator_reviewed",
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
		AssistantProposals: []projectassistant.ProposalRecord{
			bridgeAssistantProposalFixture(now),
			bridgeHecateOnlyProposalFixture(now),
		},
	}
}

func bridgeAssistantProposalFixture(now time.Time) projectassistant.ProposalRecord {
	appliedAt := now.Add(2 * time.Minute)
	result := projectassistant.ApplyResult{
		ProposalID:           "pa_bridge",
		Status:               projectassistant.ApplyStatusApplied,
		Applied:              true,
		TotalActionCount:     4,
		CommittedActionCount: 4,
		Actions: []projectassistant.ActionResult{{
			Kind: projectassistant.ActionAttachProjectRoot,
			ID:   "root_proposal",
			Data: map[string]string{
				"project_id": "proj_hecate",
				"root_id":    "root_proposal",
			},
		}, {
			Kind: projectassistant.ActionSetProjectDefaults,
			ID:   "proj_hecate",
			Data: map[string]string{
				"project_id": "proj_hecate",
				"root_id":    "root_proposal",
			},
		}, {
			Kind: projectassistant.ActionRemoveProjectRoot,
			ID:   "root_legacy",
			Data: map[string]string{
				"project_id": "proj_hecate",
				"root_id":    "root_legacy",
			},
		}, {
			Kind: projectassistant.ActionCreateWorkItem,
			ID:   "work_from_proposal",
			Data: map[string]string{
				"project_id":   "proj_hecate",
				"work_item_id": "work_from_proposal",
			},
		}},
	}
	return projectassistant.ProposalRecord{
		ID:        "pa_bridge",
		ProjectID: "proj_hecate",
		Source:    projectassistant.ProposalSourceDraft,
		SourceID:  "deterministic",
		Proposal: projectassistant.Proposal{
			ID:      "pa_bridge",
			Title:   "Create follow-up work",
			Summary: "Queue one portable follow-up work item.",
			Warnings: []string{
				"Review generated follow-up scope before applying.",
			},
			Actions: []projectassistant.Action{{
				Kind:   projectassistant.ActionAttachProjectRoot,
				Target: map[string]string{"project_id": "proj_hecate"},
				Patch: bridgeRawPatch(map[string]any{
					"id":         "root_proposal",
					"path":       "/Users/alice/dev/hecate-proposal",
					"kind":       "git_worktree",
					"git_branch": "proposal/root-actions",
					"active":     true,
				}),
				Reason: "Attach a proposed worktree root.",
			}, {
				Kind:   projectassistant.ActionSetProjectDefaults,
				Target: map[string]string{"project_id": "proj_hecate"},
				Patch: bridgeRawPatch(map[string]any{
					"default_root_id": "root_proposal",
					"default_model":   "qwen3-coder",
				}),
				Reason: "Make the proposed root the default portable root.",
			}, {
				Kind:   projectassistant.ActionRemoveProjectRoot,
				Target: map[string]string{"project_id": "proj_hecate", "root_id": "root_legacy"},
				Reason: "Remove an obsolete root.",
			}, {
				Kind:   projectassistant.ActionCreateWorkItem,
				Target: map[string]string{"project_id": "proj_hecate"},
				Patch: bridgeRawPatch(map[string]any{
					"id":            "work_from_proposal",
					"project_id":    "proj_hecate",
					"title":         "Follow up bridge export",
					"brief":         "Verify assistant proposal ledger import.",
					"status":        projectwork.WorkItemStatusReady,
					"priority":      "normal",
					"owner_role_id": "bridge_developer",
					"root_id":       "root_main",
				}),
				Reason: "Capture a reviewable follow-up.",
			}},
			RequiresConfirmation: true,
			TraceID:              "trace_bridge",
		},
		Status:       projectassistant.ApplyStatusApplied,
		LatestResult: &result,
		ApplyAttempts: []projectassistant.ApplyAttempt{{
			ID:         "paatt_bridge",
			ProposalID: "pa_bridge",
			Status:     projectassistant.ApplyStatusApplied,
			Confirmed:  true,
			Result:     result,
			CreatedAt:  appliedAt,
		}},
		CreatedAt: appliedAt.Add(-time.Minute),
		UpdatedAt: appliedAt,
		AppliedAt: &appliedAt,
	}
}

func bridgeHecateOnlyProposalFixture(now time.Time) projectassistant.ProposalRecord {
	return projectassistant.ProposalRecord{
		ID:        "pa_hecate_defaults",
		ProjectID: "proj_hecate",
		Source:    projectassistant.ProposalSourceBootstrap,
		SourceID:  "bootstrap",
		Proposal: projectassistant.Proposal{
			ID:      "pa_hecate_defaults",
			Title:   "Set Hecate defaults",
			Summary: "Hecate-only project default updates are not portable Cairnline actions.",
			Actions: []projectassistant.Action{{
				Kind:   projectassistant.ActionSetProjectDefaults,
				Target: map[string]string{"project_id": "proj_hecate"},
				Patch: bridgeRawPatch(map[string]any{
					"default_model": "qwen3-coder",
				}),
			}},
			RequiresConfirmation: true,
		},
		Status:    projectassistant.ProposalStatusProposed,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func bridgeRawPatch(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func bridgeMemorySources() SnapshotSources {
	memoryStore := memory.NewMemoryStore()
	return SnapshotSources{
		Projects:         projects.NewMemoryStore(),
		Skills:           projectskills.NewMemoryStore(),
		Work:             projectwork.NewMemoryStore(),
		Memory:           memoryStore,
		MemoryCandidates: memoryStore,
		Proposals:        projectassistant.NewMemoryProposalStore(),
	}
}

func seedHecateSources(t *testing.T, ctx context.Context, sources SnapshotSources, snapshot Snapshot) {
	t.Helper()
	if _, err := sources.Projects.Create(ctx, snapshot.Project); err != nil {
		t.Fatalf("Create project: %v", err)
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
	for _, proposal := range snapshot.AssistantProposals {
		record := proposal
		attempts := append([]projectassistant.ApplyAttempt(nil), record.ApplyAttempts...)
		record.ApplyAttempts = nil
		if _, err := sources.Proposals.UpsertProposal(ctx, record); err != nil {
			t.Fatalf("Upsert proposal %q: %v", proposal.ID, err)
		}
		for _, attempt := range attempts {
			if _, err := sources.Proposals.RecordApplyAttempt(ctx, attempt); err != nil {
				t.Fatalf("Record proposal attempt %q: %v", attempt.ID, err)
			}
		}
	}
}

func hasRole(roles []projectwork.AgentRoleProfile, id string) bool {
	for _, role := range roles {
		if role.ID == id {
			return true
		}
	}
	return false
}

func hasCairnlineRole(roles []cairnline.Role, id string) bool {
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

func findCairnlineExecutionProfile(profiles []cairnline.ExecutionProfile, id string) *cairnline.ExecutionProfile {
	for idx := range profiles {
		if profiles[idx].ID == id {
			return &profiles[idx]
		}
	}
	return nil
}

func findCairnlineRoot(roots []cairnline.Root, id string) *cairnline.Root {
	for idx := range roots {
		if roots[idx].ID == id {
			return &roots[idx]
		}
	}
	return nil
}

func findCairnlineSource(sources []cairnline.Source, id string) *cairnline.Source {
	for idx := range sources {
		if sources[idx].ID == id {
			return &sources[idx]
		}
	}
	return nil
}

func findCairnlineProjectSkill(skills []cairnline.ProjectSkill, id string) *cairnline.ProjectSkill {
	for idx := range skills {
		if skills[idx].ID == id {
			return &skills[idx]
		}
	}
	return nil
}

func boolPtrForTest(value bool) *bool {
	return &value
}

func hasCairnlineOperation(items []cairnline.ProjectOperationItem, kind, workItemID, refID string) bool {
	for _, item := range items {
		if item.Kind != kind || item.WorkItemID != workItemID {
			continue
		}
		if item.AssignmentID == refID || item.ArtifactID == refID || item.MemoryCandidateID == refID {
			return true
		}
	}
	return false
}

func hasCairnlineActivity(items []cairnline.ProjectActivityItem, bucket, workItemID, assignmentID string) bool {
	for _, item := range items {
		if item.Bucket == bucket && item.WorkItemID == workItemID && item.AssignmentID == assignmentID && item.WorkItemTitle != "" && item.RoleName != "" {
			return true
		}
	}
	return false
}
