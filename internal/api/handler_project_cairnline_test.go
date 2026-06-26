package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/memory"
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

	readModel := mustRequestJSON[ProjectCairnlineReadModelResponse](client, http.MethodGet, "/hecate/v1/projects/"+projectID+"/cairnline/read-model", "")
	if readModel.Object != "project_cairnline_read_model" || readModel.Data.ProjectID != projectID {
		t.Fatalf("read model envelope = %+v, want project_cairnline_read_model for project", readModel)
	}
	if readModel.Data.WorkItemCount != 1 || readModel.Data.AssignmentCount != 1 || readModel.Data.ArtifactCount != 2 || readModel.Data.HandoffCount != 1 || readModel.Data.MemoryEntryCount != 1 || readModel.Data.MemoryCandidateCount != 1 {
		t.Fatalf("read model counts = %+v, want bridged project counts", readModel.Data)
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
	if parity.Data.Hecate.Operations.PendingMemoryCandidates != 1 || parity.Data.Cairnline.Operations.PendingMemoryCandidates != 1 || parity.Data.Hecate.Operations.OpenHandoffs != 1 || parity.Data.Cairnline.Operations.OpenHandoffs != 1 {
		t.Fatalf("parity operations counts = hecate %+v cairnline %+v, want matching memory and handoff counts", parity.Data.Hecate.Operations, parity.Data.Cairnline.Operations)
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
	if second.Data.ProjectID != projectID || second.Data.WorkItemCount != 1 || second.Data.AssignmentCount != 1 || second.Data.ArtifactCount != 2 || second.Data.HandoffCount != 1 || second.Data.MemoryEntryCount != 1 || second.Data.MemoryCandidateCount != 1 {
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
	if packet.Project.ID != projectID || packet.Assignment.RootID != project.Data.Roots[0].ID {
		t.Fatalf("packet project/assignment = %+v/%+v, want exported root-scoped assignment", packet.Project, packet.Assignment)
	}
	if len(packet.Evidence) != 1 || len(packet.Reviews) != 1 || len(packet.Handoffs) != 1 || len(packet.Memory) != 1 || len(packet.MemoryCandidates) != 1 {
		t.Fatalf("packet counts evidence=%d reviews=%d handoffs=%d memory_entries=%d memory_candidates=%d, want all one", len(packet.Evidence), len(packet.Reviews), len(packet.Handoffs), len(packet.Memory), len(packet.MemoryCandidates))
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
