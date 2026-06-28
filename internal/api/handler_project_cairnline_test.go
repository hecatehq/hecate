package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func TestProjectCairnlineExportAPI_WritesRefreshableSQLiteExport(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: dataDir}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)
	root := t.TempDir()

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":           "Cairnline Export",
		"workspace_path": root,
		"workspace_kind": "git",
	}))
	projectID := project.Data.ID
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "bridge_profile",
		Name:                "Bridge profile",
		Surface:             agentprofiles.SurfaceHecateTask,
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		ToolsEnabled:        true,
		WritesAllowed:       true,
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	if _, err := handler.projectSkills.UpsertDiscovered(t.Context(), projectID, []projectskills.Skill{{
		ID:          "backend",
		ProjectID:   projectID,
		Title:       "Backend",
		Path:        "docs-ai/skills/backend/SKILL.md",
		RootID:      project.Data.Roots[0].ID,
		Format:      projectskills.FormatSkillMD,
		Enabled:     true,
		Status:      projectskills.StatusAvailable,
		TrustLabel:  projectskills.TrustWorkspaceSkill,
		Description: "Backend guidance.",
	}}); err != nil {
		t.Fatalf("Upsert skills: %v", err)
	}
	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "bridge_developer",
		"name":                  "Bridge Developer",
		"default_agent_profile": "bridge_profile",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
		"skill_ids":             []string{"backend"},
	}))
	reviewer := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "bridge_reviewer",
		"name":                  "Bridge Reviewer",
		"default_agent_profile": "bridge_profile",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
	}))
	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":                "work_export",
		"title":             "Export to Cairnline",
		"brief":             "Prove Hecate can write a Cairnline DB.",
		"owner_role_id":     role.Data.ID,
		"reviewer_role_ids": []string{reviewer.Data.ID},
		"root_id":           project.Data.Roots[0].ID,
	}))
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_export",
		"role_id":     role.Data.ID,
		"driver_kind": projectwork.AssignmentDriverHecateTask,
		"root_id":     project.Data.Roots[0].ID,
	}))
	mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "art_review",
		"kind":                   projectwork.ArtifactKindReview,
		"title":                  "Review",
		"body":                   "Looks usable for export.",
		"author_role_id":         reviewer.Data.ID,
		"reviewed_assignment_id": assignment.Data.ID,
		"review_verdict":         projectwork.ReviewVerdictApproved,
		"review_risk":            projectwork.ReviewRiskLow,
	}))
	mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                   "art_evidence",
		"kind":                 projectwork.ArtifactKindEvidenceLink,
		"title":                "Evidence",
		"body":                 "Export verified by test.",
		"evidence_url":         "https://example.test/run",
		"evidence_trust_label": projectwork.EvidenceTrustOperatorProvided,
	}))
	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_export",
		"target_role_id":          reviewer.Data.ID,
		"title":                   "Review export",
		"summary":                 "Cairnline export is ready.",
		"recommended_next_action": "Inspect the exported launch packet.",
		"created_by_role_id":      role.Data.ID,
	}))
	if _, err := handler.memory.Create(t.Context(), memory.Entry{
		ID:         "mem_export",
		Scope:      memory.ScopeProject,
		ProjectID:  projectID,
		Title:      "Export replacement gate",
		Body:       "Cairnline export should preserve accepted project memory.",
		TrustLabel: memory.TrustLabelOperatorMemory,
		SourceKind: memory.SourceKindOperator,
		SourceID:   handoff.Data.ID,
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Create memory entry: %v", err)
	}
	mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", projectJourneyJSON(t, map[string]any{
		"id":                    "memcand_export",
		"title":                 "Export gate",
		"body":                  "Cairnline export should preserve collaboration state.",
		"suggested_kind":        "coordination_note",
		"suggested_trust_label": memory.TrustLabelGenerated,
		"suggested_source_kind": "handoff",
		"suggested_source_id":   handoff.Data.ID,
	}))
	proposalResult := projectassistant.ApplyResult{
		ProposalID:           "pa_export",
		Status:               projectassistant.ApplyStatusApplied,
		Applied:              true,
		TotalActionCount:     1,
		CommittedActionCount: 1,
		Actions: []projectassistant.ActionResult{{
			Kind: projectassistant.ActionCreateWorkItem,
			ID:   "work_export_followup",
			Data: map[string]string{"project_id": projectID, "work_item_id": "work_export_followup"},
		}},
	}
	if _, err := handler.projectAssistantProposals.UpsertProposal(t.Context(), projectassistant.ProposalRecord{
		ID:        "pa_export",
		ProjectID: projectID,
		Source:    projectassistant.ProposalSourceDraft,
		SourceID:  "deterministic",
		Status:    projectassistant.ApplyStatusApplied,
		Proposal: projectassistant.Proposal{
			ID:      "pa_export",
			Title:   "Capture export follow-up",
			Summary: "Portable proposal ledger entry.",
			Warnings: []string{
				"Review exported assistant proposal before applying.",
			},
			Actions: []projectassistant.Action{{
				Kind:   projectassistant.ActionCreateWorkItem,
				Target: map[string]string{"project_id": projectID},
				Patch:  mustRawProjectCairnlinePatch(t, map[string]string{"id": "work_export_followup", "project_id": projectID, "title": "Export follow-up"}),
			}},
			RequiresConfirmation: true,
		},
		LatestResult: &proposalResult,
	}); err != nil {
		t.Fatalf("Upsert assistant proposal: %v", err)
	}
	if _, err := handler.projectAssistantProposals.RecordApplyAttempt(t.Context(), projectassistant.ApplyAttempt{
		ID:         "paatt_export",
		ProposalID: "pa_export",
		Status:     projectassistant.ApplyStatusApplied,
		Confirmed:  true,
		Result:     proposalResult,
	}); err != nil {
		t.Fatalf("Record assistant proposal attempt: %v", err)
	}

	readModel := mustRequestJSON[ProjectCairnlineReadModelResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/cairnline/read-model", "")
	if readModel.Object != "project_cairnline_read_model" || readModel.Data.ProjectID != projectID {
		t.Fatalf("read model envelope = %+v, want project_cairnline_read_model for project", readModel)
	}
	if readModel.Data.ReadSource != "snapshot_seeded_memory" || readModel.Data.DatabasePath != "" {
		t.Fatalf("read model source = %q path %q, want snapshot-seeded memory without a database path", readModel.Data.ReadSource, readModel.Data.DatabasePath)
	}
	if readModel.Data.RootCount != 1 || readModel.Data.ContextSourceCount != 0 || readModel.Data.WorkItemCount != 1 || readModel.Data.AssignmentCount != 1 || readModel.Data.ArtifactCount != 2 || readModel.Data.HandoffCount != 1 || readModel.Data.MemoryEntryCount != 1 || readModel.Data.MemoryCandidateCount != 1 || readModel.Data.AssistantProposalCount != 1 || readModel.Data.LaunchPacketCount != 1 {
		t.Fatalf("read model counts = %+v, want bridged project counts", readModel.Data)
	}
	if readModel.Data.AgentProfileCount == 0 || readModel.Data.ExecutionProfileCount == 0 {
		t.Fatalf("read model profile counts = agent %d execution %d, want seeded built-in/project profiles", readModel.Data.AgentProfileCount, readModel.Data.ExecutionProfileCount)
	}
	if readModel.Data.LaunchPacketWarningCount != 0 || len(readModel.Data.LaunchPacketErrors) != 0 {
		t.Fatalf("launch packet summary = warnings %d errors %+v, want clean portable packet coverage", readModel.Data.LaunchPacketWarningCount, readModel.Data.LaunchPacketErrors)
	}
	if readModel.Data.Operations.Status != cairnline.ProjectOperationsStatusAttention || readModel.Data.Operations.Counts.ActiveAssignments != 0 || readModel.Data.Operations.Counts.BlockedAssignments != 1 || readModel.Data.Operations.Counts.PendingMemoryCandidates != 1 || readModel.Data.Operations.Counts.OpenHandoffs != 1 {
		t.Fatalf("read model operations = %+v, want blocked queued assignment, pending memory, and handoff attention", readModel.Data.Operations)
	}
	if readModel.Data.Activity.Counts.Assignments != 1 || readModel.Data.Activity.Counts.Active != 0 || readModel.Data.Activity.Counts.Blocked != 1 || readModel.Data.Activity.Counts.Queued != 1 || len(readModel.Data.Activity.Buckets.Blocked) != 1 || readModel.Data.Activity.Buckets.Blocked[0].AssignmentID != assignment.Data.ID {
		t.Fatalf("read model activity = %+v, want blocked queued assignment activity", readModel.Data.Activity)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cairnline")); !os.IsNotExist(err) {
		t.Fatalf("read-model export dir stat error = %v, want not exist before export", err)
	}
	parity := mustRequestJSON[ProjectCairnlineParityReportResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/cairnline/parity-report", "")
	if parity.Object != "project_cairnline_parity_report" || parity.Data.ProjectID != projectID {
		t.Fatalf("parity envelope = %+v, want project_cairnline_parity_report for project", parity)
	}
	if !parity.Data.Match {
		t.Fatalf("parity report = %+v, want queued-assignment semantics aligned", parity.Data)
	}
	if parity.Data.Hecate.Activity.WorkItems != 1 || parity.Data.Hecate.Activity.Assignments != 1 || parity.Data.Cairnline.Activity.WorkItems != 1 || parity.Data.Cairnline.Activity.Assignments != 1 {
		t.Fatalf("parity activity counts = hecate %+v cairnline %+v, want matching work/assignment counts", parity.Data.Hecate.Activity, parity.Data.Cairnline.Activity)
	}
	if parity.Data.Hecate.Activity.Active != 0 || parity.Data.Cairnline.Activity.Active != 0 || parity.Data.Hecate.Activity.Blocked != 1 || parity.Data.Cairnline.Activity.Blocked != 1 {
		t.Fatalf("parity activity buckets = hecate %+v cairnline %+v, want matching blocked queued assignment counts", parity.Data.Hecate.Activity, parity.Data.Cairnline.Activity)
	}
	if parity.Data.Hecate.Graph.Roots != 1 || parity.Data.Cairnline.Graph.Roots != 1 || parity.Data.Hecate.Graph.ExecutionProfiles != readModel.Data.ExecutionProfileCount || parity.Data.Cairnline.Graph.ExecutionProfiles != readModel.Data.ExecutionProfileCount || parity.Data.Hecate.Graph.Artifacts != 2 || parity.Data.Cairnline.Graph.Artifacts != 2 || parity.Data.Hecate.Graph.MemoryEntries != 1 || parity.Data.Cairnline.Graph.MemoryEntries != 1 {
		t.Fatalf("parity graph counts = hecate %+v cairnline %+v, want matching portable graph counts", parity.Data.Hecate.Graph, parity.Data.Cairnline.Graph)
	}
	if parity.Data.Hecate.Operations.PendingMemoryCandidates != 1 || parity.Data.Cairnline.Operations.PendingMemoryCandidates != 1 || parity.Data.Hecate.Operations.OpenHandoffs != 1 || parity.Data.Cairnline.Operations.OpenHandoffs != 1 {
		t.Fatalf("parity operations counts = hecate %+v cairnline %+v, want matching memory and handoff counts", parity.Data.Hecate.Operations, parity.Data.Cairnline.Operations)
	}
	if parity.Data.Hecate.Operations.ItemCount == 0 || parity.Data.Hecate.Operations.ItemCount != parity.Data.Cairnline.Operations.ItemCount || parity.Data.Hecate.Operations.AvailableItemCount != parity.Data.Cairnline.Operations.AvailableItemCount || parity.Data.Hecate.Operations.ItemLimit != parity.Data.Cairnline.Operations.ItemLimit {
		t.Fatalf("parity operations summary = hecate %+v cairnline %+v, want matching rendered operations brief counts", parity.Data.Hecate.Operations, parity.Data.Cairnline.Operations)
	}
	for _, kind := range []string{"start_queued_assignment", "review_memory_candidates", "review_pending_handoff"} {
		if parity.Data.Hecate.Operations.KindCounts[kind] != 1 || parity.Data.Cairnline.Operations.KindCounts[kind] != 1 {
			t.Fatalf("parity operations kind_counts[%s] = hecate %d cairnline %d, want 1/1", kind, parity.Data.Hecate.Operations.KindCounts[kind], parity.Data.Cairnline.Operations.KindCounts[kind])
		}
	}
	if parity.Data.Hecate.Assistant.Proposals != 1 || parity.Data.Cairnline.Assistant.Proposals != 1 {
		t.Fatalf("parity assistant counts = hecate %+v cairnline %+v, want matching assistant proposal ledger counts", parity.Data.Hecate.Assistant, parity.Data.Cairnline.Assistant)
	}
	if parity.Data.Hecate.LaunchPackets.Assignments != 1 || parity.Data.Cairnline.LaunchPackets.Assignments != 1 || parity.Data.Hecate.LaunchPackets.Warnings != 0 || parity.Data.Cairnline.LaunchPackets.Warnings != 0 || parity.Data.Hecate.LaunchPackets.Errors != 0 || parity.Data.Cairnline.LaunchPackets.Errors != 0 {
		t.Fatalf("parity launch packet counts = hecate %+v cairnline %+v, want complete launch packet coverage", parity.Data.Hecate.LaunchPackets, parity.Data.Cairnline.LaunchPackets)
	}
	if len(parity.Data.Differences) != 0 {
		t.Fatalf("parity differences = %+v, want none for aligned queued assignment semantics", parity.Data.Differences)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "cairnline")); !os.IsNotExist(err) {
		t.Fatalf("parity export dir stat error = %v, want not exist before export", err)
	}

	first := mustRequestJSON[ProjectCairnlineExportResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/cairnline/export", "")
	second := mustRequestJSON[ProjectCairnlineExportResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/cairnline/export", "")
	if first.Data.DatabasePath != second.Data.DatabasePath {
		t.Fatalf("export paths = %q/%q, want refresh to same path", first.Data.DatabasePath, second.Data.DatabasePath)
	}
	if second.Data.ProjectID != projectID || second.Data.RootCount != 1 || second.Data.ContextSourceCount != 0 || second.Data.AgentProfileCount != readModel.Data.AgentProfileCount || second.Data.ExecutionProfileCount != readModel.Data.ExecutionProfileCount || second.Data.WorkItemCount != 1 || second.Data.AssignmentCount != 1 || second.Data.ArtifactCount != 2 || second.Data.HandoffCount != 1 || second.Data.MemoryEntryCount != 1 || second.Data.MemoryCandidateCount != 1 || second.Data.AssistantProposalCount != 1 {
		t.Fatalf("export response = %+v, want project counts", second.Data)
	}
	if !filepath.IsAbs(second.Data.DatabasePath) {
		t.Fatalf("database path = %q, want absolute path", second.Data.DatabasePath)
	}
	if filepath.Dir(second.Data.DatabasePath) != filepath.Join(dataDir, "cairnline", "projects") {
		t.Fatalf("database path = %q, want under data dir %q", second.Data.DatabasePath, dataDir)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), second.Data.DatabasePath)
	if err != nil {
		t.Fatalf("Open exported Cairnline DB: %v", err)
	}
	defer store.Close()
	packet, err := service.AssignmentLaunchPacket(t.Context(), projectID, assignment.Data.ID)
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket from exported DB: %v", err)
	}
	if packet.Project.ID != projectID || packet.Project.DefaultRootID != project.Data.DefaultRootID || packet.Assignment.RootID != project.Data.Roots[0].ID {
		t.Fatalf("packet project/assignment = %+v/%+v, want exported default root and root-scoped assignment", packet.Project, packet.Assignment)
	}
	executionProfiles, err := service.ListExecutionProfiles(t.Context())
	if err != nil {
		t.Fatalf("ListExecutionProfiles from exported DB: %v", err)
	}
	if len(executionProfiles) != second.Data.ExecutionProfileCount {
		t.Fatalf("execution profiles = %d, want exported count %d", len(executionProfiles), second.Data.ExecutionProfileCount)
	}
	if len(packet.Evidence) != 1 || len(packet.Reviews) != 1 || len(packet.Handoffs) != 1 || len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
		t.Fatalf("packet counts evidence=%d reviews=%d handoffs=%d memory_entries=%d memory_candidates=%d, want all one", len(packet.Evidence), len(packet.Reviews), len(packet.Handoffs), len(packet.Memory), len(packet.MemoryCandidates))
	}
	proposals, err := service.ListAssistantProposals(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ListAssistantProposals from exported DB: %v", err)
	}
	if len(proposals) != 1 || proposals[0].ID != "pa_export" || proposals[0].Status != cairnline.AssistantProposalStatusApplied || proposals[0].LatestResult == nil || len(proposals[0].ApplyAttempts) != 1 {
		t.Fatalf("assistant proposals = %+v, want exported proposal ledger", proposals)
	}
	if len(proposals[0].Proposal.Warnings) != 1 || proposals[0].Proposal.Warnings[0] != "Review exported assistant proposal before applying." {
		t.Fatalf("assistant proposal warnings = %+v, want exported warnings", proposals[0].Proposal.Warnings)
	}
	brief, err := service.ProjectOperationsBrief(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ProjectOperationsBrief from exported DB: %v", err)
	}
	if brief.Status != cairnline.ProjectOperationsStatusAttention || brief.Next == nil || brief.Counts.Assignments != 1 || brief.Counts.ActiveAssignments != 0 || brief.Counts.BlockedAssignments != 1 || brief.Counts.PendingMemoryCandidates != 1 || brief.Counts.OpenHandoffs != 1 {
		t.Fatalf("operations brief = %+v, want exported blocked queued assignment, pending memory, and handoff attention", brief)
	}
	activity, err := service.ProjectActivity(t.Context(), projectID)
	if err != nil {
		t.Fatalf("ProjectActivity from exported DB: %v", err)
	}
	if activity.Counts.Assignments != 1 || activity.Counts.Active != 0 || activity.Counts.Blocked != 1 || activity.Counts.Queued != 1 || len(activity.Buckets.Blocked) != 1 || activity.Buckets.Blocked[0].AssignmentID != assignment.Data.ID {
		t.Fatalf("activity = %+v, want exported blocked queued assignment activity", activity)
	}
}

func TestProjectCairnlineSyncAPI_WritesDurableAllProjectsSQLiteDB(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: dataDir}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	root := t.TempDir()
	firstProject := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":           "Cairnline Sync One",
		"workspace_path": root,
		"workspace_kind": "git",
	}))
	secondProject := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Cairnline Sync Two",
	}))
	if _, err := handler.agentProfiles.Create(t.Context(), agentprofiles.Profile{
		ID:                  "sync_profile",
		Name:                "Sync profile",
		Surface:             agentprofiles.SurfaceHecateTask,
		ProjectMemoryPolicy: agentprofiles.MemoryInclude,
		ContextSourcePolicy: agentprofiles.ContextIncludeEnabled,
		ProviderHint:        "openai",
		ModelHint:           "gpt-5",
	}); err != nil {
		t.Fatalf("Create profile: %v", err)
	}
	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+firstProject.Data.ID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "sync_developer",
		"name":                  "Sync Developer",
		"default_agent_profile": "sync_profile",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
	}))
	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+firstProject.Data.ID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":            "work_sync",
		"title":         "Sync Projects to Cairnline",
		"brief":         "Prove Hecate can write a durable all-project Cairnline DB.",
		"owner_role_id": role.Data.ID,
		"root_id":       firstProject.Data.Roots[0].ID,
	}))
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+firstProject.Data.ID+"/work-items/"+work.Data.ID+"/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_sync",
		"role_id":     role.Data.ID,
		"driver_kind": projectwork.AssignmentDriverHecateTask,
		"root_id":     firstProject.Data.Roots[0].ID,
	}))

	first := mustRequestJSON[ProjectCairnlineSyncResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/sync", "")
	second := mustRequestJSON[ProjectCairnlineSyncResponse](client, http.MethodPost, "/hecate/v1/projects/cairnline/sync", "")
	if first.Object != "project_cairnline_sync" || second.Object != "project_cairnline_sync" {
		t.Fatalf("sync envelopes = %+v / %+v, want project_cairnline_sync", first, second)
	}
	if first.Data.DatabasePath != second.Data.DatabasePath {
		t.Fatalf("sync paths = %q/%q, want refresh to same durable DB", first.Data.DatabasePath, second.Data.DatabasePath)
	}
	if second.Data.Authoritative {
		t.Fatalf("sync response authoritative = true, want replacement rehearsal only")
	}
	if !second.Data.Match || len(second.Data.Differences) != 0 || len(second.Data.IDDifferences) != 0 || len(second.Data.ContentDifferences) != 0 {
		t.Fatalf("sync parity = match %v differences %+v id_differences %+v content_differences %+v, want exact count, id, and content match", second.Data.Match, second.Data.Differences, second.Data.IDDifferences, second.Data.ContentDifferences)
	}
	if second.Data.Hecate.Projects != 2 || second.Data.Cairnline.Projects != 2 || second.Data.Hecate.Roots != 1 || second.Data.Cairnline.Roots != 1 || second.Data.Hecate.WorkItems != 1 || second.Data.Cairnline.WorkItems != 1 || second.Data.Hecate.Assignments != 1 || second.Data.Cairnline.Assignments != 1 {
		t.Fatalf("sync counts = hecate %+v cairnline %+v, want two projects and one rooted assignment", second.Data.Hecate, second.Data.Cairnline)
	}
	if second.Data.Hecate.LaunchPackets != 1 || second.Data.Cairnline.LaunchPackets != 1 || second.Data.Hecate.LaunchWarnings != 0 || second.Data.Cairnline.LaunchWarnings != 0 || second.Data.Hecate.LaunchErrors != 0 || second.Data.Cairnline.LaunchErrors != 0 {
		t.Fatalf("sync launch packet counts = hecate %+v cairnline %+v, want one clean packet", second.Data.Hecate, second.Data.Cairnline)
	}
	if second.Data.Hecate.AgentProfiles == 0 || second.Data.Cairnline.AgentProfiles == 0 || second.Data.Hecate.ExecutionProfiles == 0 || second.Data.Cairnline.ExecutionProfiles == 0 || second.Data.Hecate.Roles == 0 || second.Data.Cairnline.Roles == 0 {
		t.Fatalf("sync profile/role counts = hecate %+v cairnline %+v, want seeded portable defaults", second.Data.Hecate, second.Data.Cairnline)
	}
	if !filepath.IsAbs(second.Data.DatabasePath) {
		t.Fatalf("sync database path = %q, want absolute path", second.Data.DatabasePath)
	}
	if filepath.Dir(second.Data.DatabasePath) != filepath.Join(dataDir, "cairnline", "embedded") {
		t.Fatalf("sync database path = %q, want embedded DB under data dir %q", second.Data.DatabasePath, dataDir)
	}

	service, store, err := cairnline.NewSQLiteService(t.Context(), second.Data.DatabasePath)
	if err != nil {
		t.Fatalf("Open synced Cairnline DB: %v", err)
	}
	defer store.Close()
	projects, err := service.ListProjects(t.Context())
	if err != nil {
		t.Fatalf("ListProjects from synced DB: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("synced projects = %+v, want both Hecate projects", projects)
	}
	if _, err := service.GetProject(t.Context(), secondProject.Data.ID); err != nil {
		t.Fatalf("GetProject(%s) from synced DB: %v", secondProject.Data.ID, err)
	}
	packet, err := service.AssignmentLaunchPacket(t.Context(), firstProject.Data.ID, assignment.Data.ID)
	if err != nil {
		t.Fatalf("AssignmentLaunchPacket from synced DB: %v", err)
	}
	if packet.Project.ID != firstProject.Data.ID || packet.Assignment.ID != assignment.Data.ID || packet.Assignment.RootID != firstProject.Data.Roots[0].ID {
		t.Fatalf("packet = %+v, want synced rooted assignment launch packet", packet)
	}
	contentDigests, err := projectCairnlineServiceAllContentDigests(t.Context(), service)
	if err != nil {
		t.Fatalf("projectCairnlineServiceAllContentDigests() error = %v", err)
	}
	launchPacketID := scopedCairnlineID(firstProject.Data.ID, assignment.Data.ID)
	if contentDigests["launch_packets"][launchPacketID] == "" {
		t.Fatalf("launch packet content digests = %+v, want digest for %s", contentDigests["launch_packets"], launchPacketID)
	}
}

func TestProjectCairnlineMirrorParityAPI_MissingDatabaseDoesNotCreateMirror(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: dataDir}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Mirror Parity Missing DB",
	}))
	response := mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/cairnline/mirror-parity", "")
	if response.Object != "project_cairnline_mirror_parity" {
		t.Fatalf("object = %q, want project_cairnline_mirror_parity", response.Object)
	}
	if response.Data.DatabaseExists || response.Data.Match {
		t.Fatalf("mirror parity = %+v, want missing database and no match", response.Data)
	}
	if response.Data.Hecate.Projects != 1 || response.Data.Cairnline.Projects != 0 {
		t.Fatalf("mirror parity counts = hecate %+v cairnline %+v, want one Hecate project and empty mirror", response.Data.Hecate, response.Data.Cairnline)
	}
	if !hasProjectCairnlineIDDifference(response.Data.IDDifferences, "projects", []string{project.Data.ID}, nil) {
		t.Fatalf("id differences = %+v, want missing project id", response.Data.IDDifferences)
	}
	if _, err := os.Stat(handler.cairnlineEmbeddedDatabasePath()); !os.IsNotExist(err) {
		t.Fatalf("mirror DB stat error = %v, want read-only parity check to avoid creating the DB", err)
	}
}

func TestProjectCairnlineEmbeddedReadModelAPI_MissingDatabaseDoesNotCreateMirror(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: dataDir}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Embedded Read Model Missing DB",
	}))
	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/projects/"+project.Data.ID+"/cairnline/embedded-read-model", "")
	if _, err := os.Stat(handler.cairnlineEmbeddedDatabasePath()); !os.IsNotExist(err) {
		t.Fatalf("mirror DB stat error = %v, want embedded read-model probe to avoid creating the DB", err)
	}
}

func TestProjectCairnlineMirrorParityAPI_ReportsLiveMirrorMatch(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: dataDir},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name": "Live Mirror Parity",
	}))
	response := mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/cairnline/mirror-parity", "")
	if response.Object != "project_cairnline_mirror_parity" {
		t.Fatalf("object = %q, want project_cairnline_mirror_parity", response.Object)
	}
	if !response.Data.DatabaseExists || !response.Data.Match || response.Data.Authoritative {
		t.Fatalf("mirror parity = %+v, want existing non-authoritative mirror with exact parity", response.Data)
	}
	if len(response.Data.Differences) != 0 || len(response.Data.IDDifferences) != 0 || len(response.Data.ContentDifferences) != 0 {
		t.Fatalf("mirror parity differences = %+v id %+v content %+v, want none", response.Data.Differences, response.Data.IDDifferences, response.Data.ContentDifferences)
	}
	if response.Data.Hecate.AgentProfiles == 0 || response.Data.Cairnline.AgentProfiles != response.Data.Hecate.AgentProfiles {
		t.Fatalf("agent profile mirror counts = hecate %+v cairnline %+v, want built-in profiles seeded into the live mirror", response.Data.Hecate, response.Data.Cairnline)
	}
}

func TestProjectCairnlineMirrorParityAPI_MatchesRepresentativeLiveProjectJourney(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{
		Server:   config.ServerConfig{DataDir: dataDir},
		Projects: config.ProjectsConfig{CoordinationBackend: "cairnline"},
	}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)
	root := t.TempDir()
	writeProjectJourneyFile(t, root, "AGENTS.md", "# Project guidance\n\nUse small changes.\nSkill: `.hecate/skills/backend/SKILL.md`.\n")
	writeProjectJourneyFile(t, root, ".hecate/skills/backend/SKILL.md", "---\nname: Backend\ndescription: Backend work.\n---\n# Backend\n")

	profile := mustRequestJSONStatus[AgentProfileResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/agent-profiles", projectJourneyJSON(t, map[string]any{
		"id":                    "live_profile",
		"name":                  "Live Profile",
		"surface":               agentprofiles.SurfaceHecateTask,
		"execution_profile":     "live_execution",
		"provider_hint":         "openai",
		"model_hint":            "gpt-5",
		"project_memory_policy": agentprofiles.MemoryInclude,
		"context_source_policy": agentprofiles.ContextIncludeEnabled,
		"tools_enabled":         true,
		"writes_allowed":        true,
		"skill_ids":             []string{"backend"},
	}))
	if profile.Data.ID != "live_profile" {
		t.Fatalf("profile = %+v, want live_profile", profile.Data)
	}
	project := mustRequestJSONStatus[ProjectResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects", projectJourneyJSON(t, map[string]any{
		"name":                   "Live Mirror Journey",
		"description":            "Representative project graph.",
		"workspace_path":         root,
		"workspace_kind":         "git",
		"default_agent_profile":  "live_profile",
		"default_provider":       "openai",
		"default_model":          "gpt-5",
		"default_workspace_mode": "in_place",
	}))
	projectID := project.Data.ID
	if projectID == "" || len(project.Data.Roots) != 1 {
		t.Fatalf("project = %+v, want generated project with root", project.Data)
	}
	discoveredSources := mustRequestJSON[ProjectResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/context-sources/discover", `{}`)
	if !projectJourneyHasContextSource(discoveredSources.Data.ContextSources, "AGENTS.md", "workspace_instruction", "agents_md") {
		t.Fatalf("context sources = %+v, want discovered AGENTS.md source", discoveredSources.Data.ContextSources)
	}
	discoveredSkills := mustRequestJSON[ProjectSkillsResponse](client, http.MethodPost, "/hecate/v1/projects/"+projectID+"/skills/discover", "")
	if len(discoveredSkills.Data) != 1 || discoveredSkills.Data[0].ID != "backend" || discoveredSkills.Data[0].Status != projectskills.StatusAvailable {
		t.Fatalf("skills = %+v, want available backend skill", discoveredSkills.Data)
	}

	role := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "role_live",
		"name":                  "Live Implementer",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
		"default_agent_profile": "live_profile",
		"skill_ids":             []string{"backend"},
	}))
	reviewer := mustRequestJSONStatus[ProjectWorkRoleEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/roles", projectJourneyJSON(t, map[string]any{
		"id":                    "role_reviewer",
		"name":                  "Live Reviewer",
		"default_driver_kind":   projectwork.AssignmentDriverHecateTask,
		"default_agent_profile": "review_qa",
	}))
	work := mustRequestJSONStatus[ProjectWorkItemEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items", projectJourneyJSON(t, map[string]any{
		"id":                "work_live",
		"title":             "Prove live mirror parity",
		"brief":             "Exercise representative project coordination mutations.",
		"owner_role_id":     role.Data.ID,
		"reviewer_role_ids": []string{reviewer.Data.ID},
		"root_id":           project.Data.Roots[0].ID,
	}))
	assignment := mustRequestJSONStatus[ProjectWorkAssignmentEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/assignments", projectJourneyJSON(t, map[string]any{
		"id":          "asgn_live",
		"role_id":     role.Data.ID,
		"driver_kind": projectwork.AssignmentDriverHecateTask,
		"root_id":     project.Data.Roots[0].ID,
	}))
	mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                     "art_review_live",
		"kind":                   projectwork.ArtifactKindReview,
		"title":                  "Review",
		"body":                   "Representative live mirror review.",
		"author_role_id":         reviewer.Data.ID,
		"reviewed_assignment_id": assignment.Data.ID,
		"review_verdict":         projectwork.ReviewVerdictApproved,
		"review_risk":            projectwork.ReviewRiskLow,
	}))
	mustRequestJSONStatus[ProjectWorkArtifactEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/artifacts", projectJourneyJSON(t, map[string]any{
		"id":                   "art_evidence_live",
		"kind":                 projectwork.ArtifactKindEvidenceLink,
		"title":                "Evidence",
		"body":                 "Representative live mirror evidence.",
		"evidence_url":         "https://example.test/live-mirror",
		"evidence_trust_label": projectwork.EvidenceTrustOperatorProvided,
	}))
	handoff := mustRequestJSONStatus[ProjectHandoffEnvelope](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/work-items/"+work.Data.ID+"/handoffs", projectJourneyJSON(t, map[string]any{
		"id":                      "handoff_live",
		"source_assignment_id":    assignment.Data.ID,
		"target_role_id":          reviewer.Data.ID,
		"title":                   "Review live mirror",
		"summary":                 "Live mirror representative graph is ready.",
		"recommended_next_action": "Inspect mirror parity.",
		"created_by_role_id":      role.Data.ID,
	}))
	memoryEntry := mustRequestJSONStatus[ProjectMemoryResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory", projectJourneyJSON(t, map[string]any{
		"title":       "Live mirror invariant",
		"body":        "Representative project coordination state should mirror into Cairnline.",
		"source_kind": "handoff",
		"source_id":   handoff.Data.ID,
	}))
	if memoryEntry.Data.ID == "" || memoryEntry.Data.SourceID != handoff.Data.ID {
		t.Fatalf("memory = %+v, want generated memory linked to handoff", memoryEntry.Data)
	}
	candidate := mustRequestJSONStatus[ProjectMemoryCandidateResponse](client, http.StatusCreated, http.MethodPost, "/hecate/v1/projects/"+projectID+"/memory/candidates", projectJourneyJSON(t, map[string]any{
		"title":                 "Live mirror candidate",
		"body":                  "Representative candidate state should mirror into Cairnline.",
		"suggested_kind":        "coordination_note",
		"suggested_trust_label": memory.TrustLabelGenerated,
		"suggested_source_kind": "handoff",
		"suggested_source_id":   handoff.Data.ID,
	}))
	if candidate.Data.ID == "" || candidate.Data.Status != memory.CandidateStatusPending || candidate.Data.SuggestedSourceID != handoff.Data.ID {
		t.Fatalf("candidate = %+v, want pending generated candidate linked to handoff", candidate.Data)
	}

	response := mustRequestJSONStatus[ProjectCairnlineSyncResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/cairnline/mirror-parity", "")
	if response.Object != "project_cairnline_mirror_parity" {
		t.Fatalf("object = %q, want project_cairnline_mirror_parity", response.Object)
	}
	if !response.Data.DatabaseExists || !response.Data.Match {
		t.Fatalf("mirror parity = %+v, want exact live mirror match after representative journey", response.Data)
	}
	if len(response.Data.Differences) != 0 || len(response.Data.IDDifferences) != 0 || len(response.Data.ContentDifferences) != 0 {
		t.Fatalf("mirror parity differences = %+v id %+v content %+v, want none", response.Data.Differences, response.Data.IDDifferences, response.Data.ContentDifferences)
	}
	if response.Data.Hecate.Projects != 1 || response.Data.Cairnline.Projects != 1 ||
		response.Data.Hecate.Roots != 1 || response.Data.Cairnline.Roots != 1 ||
		response.Data.Hecate.ContextSources != 1 || response.Data.Cairnline.ContextSources != 1 ||
		response.Data.Hecate.Skills != 1 || response.Data.Cairnline.Skills != 1 ||
		response.Data.Hecate.WorkItems != 1 || response.Data.Cairnline.WorkItems != 1 ||
		response.Data.Hecate.Assignments != 1 || response.Data.Cairnline.Assignments != 1 ||
		response.Data.Hecate.Artifacts != 2 || response.Data.Cairnline.Artifacts != 2 ||
		response.Data.Hecate.Handoffs != 1 || response.Data.Cairnline.Handoffs != 1 ||
		response.Data.Hecate.MemoryEntries != 1 || response.Data.Cairnline.MemoryEntries != 1 ||
		response.Data.Hecate.MemoryCandidates != 1 || response.Data.Cairnline.MemoryCandidates != 1 {
		t.Fatalf("mirror counts = hecate %+v cairnline %+v, want representative graph parity", response.Data.Hecate, response.Data.Cairnline)
	}
	if response.Data.Hecate.AgentProfiles == 0 || response.Data.Cairnline.AgentProfiles != response.Data.Hecate.AgentProfiles ||
		response.Data.Hecate.ExecutionProfiles == 0 || response.Data.Cairnline.ExecutionProfiles != response.Data.Hecate.ExecutionProfiles ||
		response.Data.Hecate.Roles == 0 || response.Data.Cairnline.Roles != response.Data.Hecate.Roles {
		t.Fatalf("profile/role mirror counts = hecate %+v cairnline %+v, want built-in and custom coordination metadata parity", response.Data.Hecate, response.Data.Cairnline)
	}

	readModel := mustRequestJSONStatus[ProjectCairnlineReadModelResponse](client, http.StatusOK, http.MethodGet, "/hecate/v1/projects/"+projectID+"/cairnline/embedded-read-model", "")
	if readModel.Object != "project_cairnline_embedded_read_model" || readModel.Data.ProjectID != projectID {
		t.Fatalf("embedded read model envelope = %+v, want direct embedded Cairnline read model for project", readModel)
	}
	if readModel.Data.ReadSource != "embedded_cairnline" || readModel.Data.DatabasePath != handler.cairnlineEmbeddedDatabasePath() || !filepath.IsAbs(readModel.Data.DatabasePath) {
		t.Fatalf("embedded read model source = %q path %q, want embedded Cairnline database path", readModel.Data.ReadSource, readModel.Data.DatabasePath)
	}
	if readModel.Data.ContextSourceCount != 1 || readModel.Data.SkillCount != 1 || readModel.Data.WorkItemCount != 1 || readModel.Data.AssignmentCount != 1 || readModel.Data.ArtifactCount != 2 || readModel.Data.HandoffCount != 1 || readModel.Data.MemoryEntryCount != 1 || readModel.Data.MemoryCandidateCount != 1 || readModel.Data.LaunchPacketCount != 1 {
		t.Fatalf("embedded read model counts = %+v, want representative live mirror graph", readModel.Data)
	}
	if readModel.Data.LaunchPacketWarningCount != 0 || len(readModel.Data.LaunchPacketErrors) != 0 {
		t.Fatalf("embedded launch packet summary = warnings %d errors %+v, want clean portable packet coverage", readModel.Data.LaunchPacketWarningCount, readModel.Data.LaunchPacketErrors)
	}
	if readModel.Data.Operations.Status != cairnline.ProjectOperationsStatusAttention || readModel.Data.Operations.Counts.BlockedAssignments != 1 || readModel.Data.Operations.Counts.PendingMemoryCandidates != 1 || readModel.Data.Operations.Counts.OpenHandoffs != 1 {
		t.Fatalf("embedded operations = %+v, want blocked assignment, pending memory, and open handoff from live mirror", readModel.Data.Operations)
	}
	if readModel.Data.Activity.Counts.Assignments != 1 || readModel.Data.Activity.Counts.Blocked != 1 || readModel.Data.Activity.Counts.Queued != 1 || len(readModel.Data.Activity.Buckets.Blocked) != 1 || readModel.Data.Activity.Buckets.Blocked[0].AssignmentID != assignment.Data.ID {
		t.Fatalf("embedded activity = %+v, want blocked queued assignment from live mirror", readModel.Data.Activity)
	}
}

func TestProjectCairnlineSyncDifferences(t *testing.T) {
	differences := projectCairnlineSyncDifferences(ProjectCairnlineSyncCounts{
		Projects:          2,
		ExecutionProfiles: 4,
		Assignments:       1,
		LaunchPackets:     1,
	}, ProjectCairnlineSyncCounts{
		Projects:          1,
		ExecutionProfiles: 3,
		Assignments:       1,
		LaunchPackets:     0,
		LaunchWarnings:    1,
		LaunchErrors:      1,
	})
	if len(differences) != 5 {
		t.Fatalf("sync differences = %+v, want project, execution profile, and launch packet mismatches only", differences)
	}
	if !hasProjectCairnlineParityDifference(differences, "projects", 2, 1) {
		t.Fatalf("sync differences = %+v, want projects 2/1", differences)
	}
	if !hasProjectCairnlineParityDifference(differences, "execution_profiles", 4, 3) {
		t.Fatalf("sync differences = %+v, want execution_profiles 4/3", differences)
	}
	if !hasProjectCairnlineParityDifference(differences, "launch_packets", 1, 0) {
		t.Fatalf("sync differences = %+v, want launch_packets 1/0", differences)
	}
	if !hasProjectCairnlineParityDifference(differences, "launch_warnings", 0, 1) {
		t.Fatalf("sync differences = %+v, want launch_warnings 0/1", differences)
	}
	if !hasProjectCairnlineParityDifference(differences, "launch_errors", 0, 1) {
		t.Fatalf("sync differences = %+v, want launch_errors 0/1", differences)
	}
}

func TestProjectCairnlineSyncIDDifferences(t *testing.T) {
	differences := projectCairnlineSyncIDDifferences(ProjectCairnlineSyncIDSets{
		Projects:      []string{"proj_a", "proj_b"},
		WorkItems:     []string{"proj_a/work_a"},
		LaunchPackets: []string{"proj_a/asgn_a"},
	}, ProjectCairnlineSyncIDSets{
		Projects:      []string{"proj_a", "proj_c"},
		WorkItems:     []string{"proj_a/work_a"},
		LaunchPackets: nil,
	})
	if len(differences) != 2 {
		t.Fatalf("id differences = %+v, want project and launch packet mismatches only", differences)
	}
	if !hasProjectCairnlineIDDifference(differences, "projects", []string{"proj_a", "proj_b"}, []string{"proj_a", "proj_c"}) {
		t.Fatalf("id differences = %+v, want projects mismatch", differences)
	}
	if !hasProjectCairnlineIDDifference(differences, "launch_packets", []string{"proj_a/asgn_a"}, nil) {
		t.Fatalf("id differences = %+v, want launch packet mismatch", differences)
	}
}

func TestProjectCairnlineSyncContentDifferences(t *testing.T) {
	differences := projectCairnlineSyncContentDifferences(projectCairnlineContentDigests{
		"projects": map[string]string{
			"proj_a": "digest-a",
			"proj_b": "digest-b",
		},
		"launch_packets": map[string]string{
			"proj_a/asgn_a": "launch-a",
		},
		"work_items": map[string]string{
			"proj_a/work_a": "same",
		},
	}, projectCairnlineContentDigests{
		"projects": map[string]string{
			"proj_a": "digest-c",
			"proj_c": "digest-d",
		},
		"launch_packets": map[string]string{
			"proj_a/asgn_a": "launch-b",
		},
		"work_items": map[string]string{
			"proj_a/work_a": "same",
		},
	})
	if len(differences) != 2 {
		t.Fatalf("content differences = %+v, want same-id project and launch-packet content mismatches", differences)
	}
	if !hasProjectCairnlineContentDifference(differences, "projects", "proj_a", "digest-a", "digest-c") {
		t.Fatalf("content differences = %+v, want projects/proj_a digest mismatch", differences)
	}
	if !hasProjectCairnlineContentDifference(differences, "launch_packets", "proj_a/asgn_a", "launch-a", "launch-b") {
		t.Fatalf("content differences = %+v, want launch_packets/proj_a/asgn_a digest mismatch", differences)
	}
}

func TestProjectCairnlineContentDigestIgnoresVolatileTimestamps(t *testing.T) {
	first := cairnline.Project{
		ID:        "proj_digest",
		Name:      "Digest parity",
		CreatedAt: time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 27, 10, 1, 0, 0, time.UTC),
	}
	second := first
	second.CreatedAt = time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)
	second.UpdatedAt = time.Date(2026, 6, 27, 11, 1, 0, 0, time.UTC)
	if projectCairnlineContentDigest(first) != projectCairnlineContentDigest(second) {
		t.Fatalf("content digest changed across volatile timestamps")
	}
	second.Name = "Digest drift"
	if projectCairnlineContentDigest(first) == projectCairnlineContentDigest(second) {
		t.Fatalf("content digest ignored semantic project name change")
	}
}

func TestProjectCairnlineParityReport_IncludesAssistantProposalDifferences(t *testing.T) {
	report := projectCairnlineParityReport("proj_parity", ProjectCairnlineGraphParityCounts{}, ProjectActivityDataResponse{}, ProjectOperationsBriefResponse{}, ProjectOperationsBriefResponse{}, 2, ProjectCairnlineReadModelResponseItem{
		AssistantProposalCount: 1,
	})
	if report.Match {
		t.Fatalf("parity report match = true, want assistant proposal mismatch")
	}
	if report.Hecate.Assistant.Proposals != 2 || report.Cairnline.Assistant.Proposals != 1 {
		t.Fatalf("assistant parity counts = hecate %+v cairnline %+v, want 2/1", report.Hecate.Assistant, report.Cairnline.Assistant)
	}
	if len(report.Differences) != 1 || report.Differences[0].Path != "assistant.proposals" || report.Differences[0].Hecate != 2 || report.Differences[0].Cairnline != 1 {
		t.Fatalf("assistant parity differences = %+v, want assistant.proposals 2/1", report.Differences)
	}
}

func TestProjectCairnlineParityReport_IncludesGraphCountDifferences(t *testing.T) {
	report := projectCairnlineParityReport("proj_parity", ProjectCairnlineGraphParityCounts{
		Roots:             1,
		ContextSources:    2,
		ExecutionProfiles: 4,
		Artifacts:         3,
	}, ProjectActivityDataResponse{}, ProjectOperationsBriefResponse{}, ProjectOperationsBriefResponse{}, 0, ProjectCairnlineReadModelResponseItem{
		RootCount:             1,
		ContextSourceCount:    1,
		ExecutionProfileCount: 3,
		ArtifactCount:         2,
	})
	if report.Match {
		t.Fatalf("parity report match = true, want graph mismatch")
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "graph.context_sources", 2, 1) {
		t.Fatalf("parity differences = %+v, want graph.context_sources 2/1", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "graph.execution_profiles", 4, 3) {
		t.Fatalf("parity differences = %+v, want graph.execution_profiles 4/3", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "graph.artifacts", 3, 2) {
		t.Fatalf("parity differences = %+v, want graph.artifacts 3/2", report.Differences)
	}
}

func TestProjectCairnlineParityReport_IncludesLaunchPacketCoverageDifferences(t *testing.T) {
	report := projectCairnlineParityReport("proj_parity", ProjectCairnlineGraphParityCounts{}, ProjectActivityDataResponse{
		Summary: ProjectActivitySummaryResponse{AssignmentCount: 2},
	}, ProjectOperationsBriefResponse{}, ProjectOperationsBriefResponse{}, 0, ProjectCairnlineReadModelResponseItem{
		LaunchPacketCount:        1,
		LaunchPacketWarningCount: 2,
		LaunchPacketErrors: []ProjectCairnlineLaunchPacketError{{
			AssignmentID: "asgn_missing",
			Error:        "assignment role was not found",
		}},
	})
	if report.Match {
		t.Fatalf("parity report match = true, want launch packet coverage mismatch")
	}
	if report.Hecate.LaunchPackets.Assignments != 2 || report.Cairnline.LaunchPackets.Assignments != 1 || report.Cairnline.LaunchPackets.Warnings != 2 || report.Cairnline.LaunchPackets.Errors != 1 {
		t.Fatalf("launch packet parity counts = hecate %+v cairnline %+v, want expected 2, available 1, warnings 2, errors 1", report.Hecate.LaunchPackets, report.Cairnline.LaunchPackets)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "launch_packets.assignments", 2, 1) {
		t.Fatalf("parity differences = %+v, want launch_packets.assignments 2/1", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "launch_packets.warnings", 0, 2) {
		t.Fatalf("parity differences = %+v, want launch_packets.warnings 0/2", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "launch_packets.errors", 0, 1) {
		t.Fatalf("parity differences = %+v, want launch_packets.errors 0/1", report.Differences)
	}
}

func TestProjectCairnlineParityReport_IncludesOperationsDifferences(t *testing.T) {
	nativeOperations := ProjectOperationsBriefResponse{
		Summary: ProjectOperationsBriefSummaryResponse{
			ItemCount:                   2,
			AvailableItemCount:          2,
			ItemLimit:                   projectOperationsBriefItemLimit,
			HighCount:                   1,
			MediumCount:                 1,
			PendingMemoryCandidateCount: 1,
		},
		Items: []ProjectOperationsBriefItemResponse{
			{Kind: "start_queued_assignment"},
			{Kind: "review_memory_candidates"},
		},
	}
	cairnlineOperations := ProjectOperationsBriefResponse{
		Summary: ProjectOperationsBriefSummaryResponse{
			ItemCount:                   1,
			AvailableItemCount:          1,
			ItemLimit:                   projectOperationsBriefItemLimit,
			HighCount:                   1,
			PendingMemoryCandidateCount: 0,
		},
		Items: []ProjectOperationsBriefItemResponse{
			{Kind: "start_queued_assignment"},
		},
	}
	report := projectCairnlineParityReport("proj_parity", ProjectCairnlineGraphParityCounts{}, ProjectActivityDataResponse{}, nativeOperations, cairnlineOperations, 0, ProjectCairnlineReadModelResponseItem{})
	if report.Match {
		t.Fatalf("parity report match = true, want operations mismatch")
	}
	if report.Hecate.Operations.ItemCount != 2 || report.Cairnline.Operations.ItemCount != 1 || report.Hecate.Operations.KindCounts["review_memory_candidates"] != 1 || report.Cairnline.Operations.KindCounts["review_memory_candidates"] != 0 {
		t.Fatalf("operations parity counts = hecate %+v cairnline %+v, want rendered brief counts and kind counts", report.Hecate.Operations, report.Cairnline.Operations)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "operations.item_count", 2, 1) {
		t.Fatalf("parity differences = %+v, want operations.item_count 2/1", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "operations.pending_memory_candidates", 1, 0) {
		t.Fatalf("parity differences = %+v, want operations.pending_memory_candidates 1/0", report.Differences)
	}
	if !hasProjectCairnlineParityDifference(report.Differences, "operations.kind_counts.review_memory_candidates", 1, 0) {
		t.Fatalf("parity differences = %+v, want operations.kind_counts.review_memory_candidates 1/0", report.Differences)
	}
}

func hasProjectCairnlineParityDifference(items []ProjectCairnlineParityDifference, path string, hecate, cairnline int) bool {
	for _, item := range items {
		if item.Path == path && item.Hecate == hecate && item.Cairnline == cairnline {
			return true
		}
	}
	return false
}

func hasProjectCairnlineIDDifference(items []ProjectCairnlineIDDifference, path string, hecate, cairnline []string) bool {
	for _, item := range items {
		if item.Path == path && equalStringSlices(item.Hecate, hecate) && equalStringSlices(item.Cairnline, cairnline) {
			return true
		}
	}
	return false
}

func hasProjectCairnlineContentDifference(items []ProjectCairnlineContentDifference, path, id, hecate, cairnline string) bool {
	for _, item := range items {
		if item.Path == path && item.ID == id && item.Hecate == hecate && item.Cairnline == cairnline {
			return true
		}
	}
	return false
}

func mustRawProjectCairnlinePatch(t *testing.T, value any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal proposal patch: %v", err)
	}
	return payload
}

func TestProjectCairnlineExportAPI_MissingProjectDoesNotCreateExportDir(t *testing.T) {
	dataDir := t.TempDir()
	handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: dataDir}}, quietLogger(), nil, nil, nil, nil)
	handler.SetProjectStore(projects.NewMemoryStore())
	handler.SetProjectWorkStore(projectwork.NewMemoryStore())
	handler.SetProjectSkillStore(projectskills.NewMemoryStore())
	handler.SetMemoryStore(memory.NewMemoryStore())
	handler.SetAgentProfileStore(agentprofiles.NewMemoryStore())
	server := NewServer(quietLogger(), handler)
	client := newAPITestClient(t, server)

	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/projects/proj_missing/cairnline/parity-report", "")
	client.mustRequestStatus(http.StatusNotFound, http.MethodGet, "/hecate/v1/projects/proj_missing/cairnline/read-model", "")
	client.mustRequestStatus(http.StatusNotFound, http.MethodPost, "/hecate/v1/projects/proj_missing/cairnline/export", "")
	if _, err := os.Stat(filepath.Join(dataDir, "cairnline")); !os.IsNotExist(err) {
		t.Fatalf("export dir stat error = %v, want not exist", err)
	}
}
