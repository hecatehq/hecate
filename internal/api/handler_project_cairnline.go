package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/cairnlinebridge"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectwork"
)

func (h *Handler) HandleSyncProjectsToCairnline(w http.ResponseWriter, r *http.Request) {
	dbPath := h.cairnlineEmbeddedDatabasePath()
	snapshots, err := cairnlinebridge.LoadSnapshots(r.Context(), h.cairnlineSnapshotSources())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// This is a replacement-readiness rehearsal, not a live backend switch.
	// Replacing the local sync DB keeps repeated operator-triggered syncs
	// deterministic while Hecate stores remain authoritative.
	if err := removeCairnlineSQLiteFiles(dbPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	service, store, err := cairnline.NewSQLiteService(r.Context(), dbPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	defer store.Close()
	if err := cairnlinebridge.SeedSnapshots(r.Context(), service, snapshots); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	item, err := projectCairnlineServiceParity(r.Context(), dbPath, true, snapshots, service)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSyncResponse{
		Object: "project_cairnline_sync",
		Data:   item,
	})
}

func (h *Handler) HandleProjectCairnlineMirrorParity(w http.ResponseWriter, r *http.Request) {
	dbPath := h.cairnlineEmbeddedDatabasePath()
	snapshots, err := cairnlinebridge.LoadSnapshots(r.Context(), h.cairnlineSnapshotSources())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		if !os.IsNotExist(err) {
			WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
			return
		}
		item := projectCairnlineMissingMirrorParity(dbPath, snapshots)
		WriteJSON(w, http.StatusOK, ProjectCairnlineSyncResponse{
			Object: "project_cairnline_mirror_parity",
			Data:   item,
		})
		return
	}
	service, store, err := cairnline.NewSQLiteService(r.Context(), dbPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	defer store.Close()
	item, err := projectCairnlineServiceParity(r.Context(), dbPath, true, snapshots, service)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineSyncResponse{
		Object: "project_cairnline_mirror_parity",
		Data:   item,
	})
}

func (h *Handler) HandleExportProjectToCairnline(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	dbPath, err := h.cairnlineExportPath(projectID)
	if err != nil {
		WriteError(w, http.StatusBadRequest, errCodeInvalidRequest, err.Error())
		return
	}
	snapshot, err := cairnlinebridge.LoadSnapshot(r.Context(), h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	// This local-only experiment owns its project export file. Replacing it
	// keeps repeated operator-triggered exports deterministic while avoiding a
	// live-backend switch or any mutation of Hecate's authoritative stores.
	if err := removeCairnlineSQLiteFiles(dbPath); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	service, store, err := cairnline.NewSQLiteService(r.Context(), dbPath)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	defer store.Close()
	if err := cairnlinebridge.Seed(r.Context(), service, snapshot); err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	graph, err := projectCairnlineServiceGraphCounts(r.Context(), service, snapshot.Project.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	assistantProposals, err := service.ListAssistantProposals(r.Context(), snapshot.Project.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineExportResponse{
		Object: "project_cairnline_export",
		Data: ProjectCairnlineExportResponseItem{
			ProjectID:              snapshot.Project.ID,
			DatabasePath:           dbPath,
			RootCount:              graph.Roots,
			ContextSourceCount:     graph.ContextSources,
			AgentProfileCount:      graph.AgentProfiles,
			ExecutionProfileCount:  graph.ExecutionProfiles,
			SkillCount:             graph.Skills,
			RoleCount:              graph.Roles,
			WorkItemCount:          graph.WorkItems,
			AssignmentCount:        graph.Assignments,
			ArtifactCount:          graph.Artifacts,
			HandoffCount:           graph.Handoffs,
			MemoryEntryCount:       graph.MemoryEntries,
			MemoryCandidateCount:   graph.MemoryCandidates,
			AssistantProposalCount: len(assistantProposals),
		},
	})
}

func (h *Handler) HandleProjectCairnlineReadModel(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	readModel, err := h.projectCairnlineReadModel(r.Context(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineReadModelResponse{
		Object: "project_cairnline_read_model",
		Data:   readModel,
	})
}

func (h *Handler) HandleProjectCairnlineEmbeddedReadModel(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	readModel, err := h.projectCairnlineEmbeddedReadModel(r.Context(), projectID)
	if errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "embedded Cairnline read model not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineReadModelResponse{
		Object: "project_cairnline_embedded_read_model",
		Data:   readModel,
	})
}

func (h *Handler) HandleProjectCairnlineParityReport(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	snapshot, err := cairnlinebridge.LoadSnapshot(r.Context(), h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	service, err := projectCairnlineServiceFromSnapshot(r.Context(), snapshot)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	readModel, err := projectCairnlineReadModelFromService(r.Context(), service, snapshot.Project.ID, "snapshot_seeded_memory", "")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	nativeWorkItems, err := h.renderNativeProjectWorkItems(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	cairnlineWorkItems, err := h.renderCairnlineProjectWorkItemsFromService(r.Context(), service, snapshot)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	nativeCollaboration, err := h.nativeProjectCollaborationParityCounts(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	cairnlineCollaboration, err := cairnlineProjectCollaborationParityCountsFromService(r.Context(), service, snapshot.Project.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	nativeActivity, err := h.renderNativeProjectActivity(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	nativeOperations, err := h.renderNativeProjectOperationsBriefByID(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	cairnlineOperations, err := h.renderCairnlineProjectOperationsBrief(r.Context(), snapshot.Project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	nativeAssistantProposals, err := h.nativeProjectAssistantProposalCount(r.Context(), projectID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	report := projectCairnlineParityReport(projectID, projectCairnlineSnapshotGraphCounts(snapshot), nativeWorkItems, nativeCollaboration, nativeActivity, nativeOperations, cairnlineWorkItems, cairnlineCollaboration, cairnlineOperations, nativeAssistantProposals, readModel)
	WriteJSON(w, http.StatusOK, ProjectCairnlineParityReportResponse{
		Object: "project_cairnline_parity_report",
		Data:   report,
	})
}

func (h *Handler) HandleProjectCairnlineEmbeddedParityReport(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.PathValue("id"))
	report, err := h.projectCairnlineEmbeddedParityReport(r.Context(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "project not found")
		return
	}
	if errors.Is(err, cairnline.ErrNotFound) {
		WriteError(w, http.StatusNotFound, errCodeNotFound, "embedded Cairnline parity report not found")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, errCodeGatewayError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ProjectCairnlineParityReportResponse{
		Object: "project_cairnline_embedded_parity_report",
		Data:   report,
	})
}

func (h *Handler) projectCairnlineReadModel(ctx context.Context, projectID string) (ProjectCairnlineReadModelResponseItem, error) {
	snapshot, err := cairnlinebridge.LoadSnapshot(ctx, h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	return projectCairnlineReadModelFromSnapshot(ctx, snapshot)
}

func (h *Handler) projectCairnlineEmbeddedReadModel(ctx context.Context, projectID string) (ProjectCairnlineReadModelResponseItem, error) {
	dbPath, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	defer store.Close()
	return projectCairnlineReadModelFromService(ctx, service, projectID, "embedded_cairnline", dbPath)
}

func (h *Handler) projectCairnlineEmbeddedParityReport(ctx context.Context, projectID string) (ProjectCairnlineParityReportResponseItem, error) {
	snapshot, err := cairnlinebridge.LoadSnapshot(ctx, h.cairnlineSnapshotSources(), projectID)
	if errors.Is(err, projects.ErrNotFound) {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	dbPath, service, store, err := h.openCairnlineEmbeddedService(ctx)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	defer store.Close()
	readModel, err := projectCairnlineReadModelFromService(ctx, service, projectID, "embedded_cairnline", dbPath)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	nativeWorkItems, err := h.renderNativeProjectWorkItems(ctx, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	cairnlineWorkItems, err := h.renderCairnlineProjectWorkItemsFromService(ctx, service, snapshot)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	nativeCollaboration, err := h.nativeProjectCollaborationParityCounts(ctx, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	cairnlineCollaboration, err := cairnlineProjectCollaborationParityCountsFromService(ctx, service, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	nativeActivity, err := h.renderNativeProjectActivity(ctx, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	nativeOperations, err := h.renderNativeProjectOperationsBriefByID(ctx, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	cairnlineOperations, err := h.renderCairnlineProjectOperationsBriefFromService(ctx, snapshot.Project, service, snapshot)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	nativeAssistantProposals, err := h.nativeProjectAssistantProposalCount(ctx, projectID)
	if err != nil {
		return ProjectCairnlineParityReportResponseItem{}, err
	}
	return projectCairnlineParityReport(projectID, projectCairnlineSnapshotGraphCounts(snapshot), nativeWorkItems, nativeCollaboration, nativeActivity, nativeOperations, cairnlineWorkItems, cairnlineCollaboration, cairnlineOperations, nativeAssistantProposals, readModel), nil
}

func (h *Handler) openCairnlineEmbeddedService(ctx context.Context) (string, *cairnline.Service, *cairnline.SQLiteStore, error) {
	dbPath := h.cairnlineEmbeddedDatabasePath()
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil, errors.Join(cairnline.ErrNotFound, errors.New("embedded Cairnline mirror database not found"))
		}
		return "", nil, nil, err
	}
	service, store, err := cairnline.NewSQLiteService(ctx, dbPath)
	if err != nil {
		return "", nil, nil, err
	}
	return dbPath, service, store, nil
}

func projectCairnlineReadModelFromSnapshot(ctx context.Context, snapshot cairnlinebridge.Snapshot) (ProjectCairnlineReadModelResponseItem, error) {
	service, err := projectCairnlineServiceFromSnapshot(ctx, snapshot)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	return projectCairnlineReadModelFromService(ctx, service, snapshot.Project.ID, "snapshot_seeded_memory", "")
}

func projectCairnlineServiceFromSnapshot(ctx context.Context, snapshot cairnlinebridge.Snapshot) (*cairnline.Service, error) {
	service := cairnline.NewMemoryService()
	if err := cairnlinebridge.Seed(ctx, service, snapshot); err != nil {
		return nil, err
	}
	return service, nil
}

func projectCairnlineReadModelFromService(ctx context.Context, service *cairnline.Service, projectID, readSource, dbPath string) (ProjectCairnlineReadModelResponseItem, error) {
	project, err := service.GetProject(ctx, projectID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	graph, err := projectCairnlineServiceGraphCounts(ctx, service, project.ID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	assistantProposals, err := service.ListAssistantProposals(ctx, project.ID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	operations, err := service.ProjectOperationsBrief(ctx, project.ID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	activity, err := service.ProjectActivity(ctx, project.ID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	launchPackets, err := projectCairnlineServiceLaunchPacketSummary(ctx, service, project.ID)
	if err != nil {
		return ProjectCairnlineReadModelResponseItem{}, err
	}
	return ProjectCairnlineReadModelResponseItem{
		ProjectID:                project.ID,
		ReadSource:               readSource,
		DatabasePath:             dbPath,
		RootCount:                graph.Roots,
		ContextSourceCount:       graph.ContextSources,
		AgentProfileCount:        graph.AgentProfiles,
		ExecutionProfileCount:    graph.ExecutionProfiles,
		SkillCount:               graph.Skills,
		RoleCount:                graph.Roles,
		WorkItemCount:            graph.WorkItems,
		AssignmentCount:          graph.Assignments,
		ArtifactCount:            graph.Artifacts,
		HandoffCount:             graph.Handoffs,
		MemoryEntryCount:         graph.MemoryEntries,
		MemoryCandidateCount:     graph.MemoryCandidates,
		AssistantProposalCount:   len(assistantProposals),
		LaunchPacketCount:        launchPackets.Count,
		LaunchPacketWarningCount: launchPackets.WarningCount,
		LaunchPacketErrors:       launchPackets.Errors,
		Operations:               operations,
		Activity:                 activity,
	}, nil
}

func projectCairnlineServiceParity(ctx context.Context, dbPath string, databaseExists bool, snapshots []cairnlinebridge.Snapshot, service *cairnline.Service) (ProjectCairnlineSyncResponseItem, error) {
	hecateCounts := projectCairnlineSnapshotAllCounts(snapshots)
	cairnlineCounts, err := projectCairnlineServiceAllCounts(ctx, service)
	if err != nil {
		return ProjectCairnlineSyncResponseItem{}, err
	}
	differences := projectCairnlineSyncDifferences(hecateCounts, cairnlineCounts)
	hecateIDs := projectCairnlineSnapshotAllIDSets(snapshots)
	cairnlineIDs, err := projectCairnlineServiceAllIDSets(ctx, service)
	if err != nil {
		return ProjectCairnlineSyncResponseItem{}, err
	}
	idDifferences := projectCairnlineSyncIDDifferences(hecateIDs, cairnlineIDs)
	hecateContent, err := projectCairnlineSnapshotAllContentDigests(ctx, snapshots)
	if err != nil {
		return ProjectCairnlineSyncResponseItem{}, err
	}
	cairnlineContent, err := projectCairnlineServiceAllContentDigests(ctx, service)
	if err != nil {
		return ProjectCairnlineSyncResponseItem{}, err
	}
	contentDifferences := projectCairnlineSyncContentDifferences(hecateContent, cairnlineContent)
	return ProjectCairnlineSyncResponseItem{
		DatabasePath:       dbPath,
		DatabaseExists:     databaseExists,
		Match:              len(differences) == 0 && len(idDifferences) == 0 && len(contentDifferences) == 0,
		Differences:        differences,
		IDDifferences:      idDifferences,
		ContentDifferences: contentDifferences,
		Hecate:             hecateCounts,
		Cairnline:          cairnlineCounts,
		Authoritative:      false,
	}, nil
}

func projectCairnlineMissingMirrorParity(dbPath string, snapshots []cairnlinebridge.Snapshot) ProjectCairnlineSyncResponseItem {
	hecateCounts := projectCairnlineSnapshotAllCounts(snapshots)
	cairnlineCounts := ProjectCairnlineSyncCounts{}
	hecateIDs := projectCairnlineSnapshotAllIDSets(snapshots)
	cairnlineIDs := ProjectCairnlineSyncIDSets{}
	differences := projectCairnlineSyncDifferences(hecateCounts, cairnlineCounts)
	idDifferences := projectCairnlineSyncIDDifferences(hecateIDs, cairnlineIDs)
	return ProjectCairnlineSyncResponseItem{
		DatabasePath:       dbPath,
		DatabaseExists:     false,
		Match:              false,
		Differences:        differences,
		IDDifferences:      idDifferences,
		ContentDifferences: nil,
		Hecate:             hecateCounts,
		Cairnline:          cairnlineCounts,
		Authoritative:      false,
	}
}

func projectCairnlineSnapshotGraphCounts(snapshot cairnlinebridge.Snapshot) ProjectCairnlineGraphParityCounts {
	return ProjectCairnlineGraphParityCounts{
		Roots:             len(snapshot.Project.Roots),
		ContextSources:    len(snapshot.Project.ContextSources),
		AgentProfiles:     len(snapshot.AgentProfiles),
		ExecutionProfiles: cairnlinebridge.SnapshotExecutionProfileCount(snapshot),
		Skills:            len(snapshot.Skills),
		Roles:             len(snapshot.Roles),
		WorkItems:         len(snapshot.WorkItems),
		Assignments:       len(snapshot.Assignments),
		Artifacts:         len(snapshot.Artifacts),
		Handoffs:          len(snapshot.Handoffs),
		MemoryEntries:     len(snapshot.MemoryEntries),
		MemoryCandidates:  len(snapshot.MemoryCandidates),
	}
}

func projectCairnlineServiceGraphCounts(ctx context.Context, service *cairnline.Service, projectID string) (ProjectCairnlineGraphParityCounts, error) {
	var counts ProjectCairnlineGraphParityCounts
	projects, err := service.ListProjects(ctx)
	if err != nil {
		return counts, err
	}
	var found bool
	for _, project := range projects {
		if project.ID != projectID {
			continue
		}
		found = true
		counts.Roots = len(project.Roots)
		counts.ContextSources = len(project.ContextSources)
		break
	}
	if !found {
		return counts, cairnline.ErrNotFound
	}
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return counts, err
	}
	counts.AgentProfiles = len(profiles)
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return counts, err
	}
	counts.ExecutionProfiles = len(executionProfiles)
	skills, err := service.ListProjectSkills(ctx, projectID)
	if err != nil {
		return counts, err
	}
	counts.Skills = len(skills)
	roles, err := service.ListRoles(ctx, projectID)
	if err != nil {
		return counts, err
	}
	counts.Roles = len(roles)
	workItems, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return counts, err
	}
	counts.WorkItems = len(workItems)
	assignments, err := service.ListAssignments(ctx, projectID)
	if err != nil {
		return counts, err
	}
	counts.Assignments = len(assignments)
	for _, workItem := range workItems {
		artifacts, err := service.ListArtifacts(ctx, projectID, workItem.ID)
		if err != nil {
			return counts, err
		}
		evidence, err := service.ListEvidence(ctx, projectID, workItem.ID)
		if err != nil {
			return counts, err
		}
		reviews, err := service.ListReviews(ctx, projectID, workItem.ID)
		if err != nil {
			return counts, err
		}
		handoffs, err := service.ListHandoffs(ctx, projectID, workItem.ID)
		if err != nil {
			return counts, err
		}
		counts.Artifacts += len(artifacts) + len(evidence) + len(reviews)
		counts.Handoffs += len(handoffs)
	}
	memoryEntries, err := service.ListMemoryEntries(ctx, projectID, true)
	if err != nil {
		return counts, err
	}
	counts.MemoryEntries = len(memoryEntries)
	memoryCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
		ProjectID:       projectID,
		IncludeResolved: true,
	})
	if err != nil {
		return counts, err
	}
	counts.MemoryCandidates = len(memoryCandidates)
	return counts, nil
}

func projectCairnlineSnapshotAllCounts(snapshots []cairnlinebridge.Snapshot) ProjectCairnlineSyncCounts {
	var counts ProjectCairnlineSyncCounts
	profileIDs := map[string]struct{}{}
	executionProfileIDs := map[string]struct{}{}
	addProfileID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		profileIDs[id] = struct{}{}
	}
	addExecutionProfileID := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		executionProfileIDs[id] = struct{}{}
	}
	for _, snapshot := range snapshots {
		counts.Projects++
		counts.Roots += len(snapshot.Project.Roots)
		counts.ContextSources += len(snapshot.Project.ContextSources)
		counts.Skills += len(snapshot.Skills)
		counts.Roles += len(snapshot.Roles)
		counts.WorkItems += len(snapshot.WorkItems)
		counts.Assignments += len(snapshot.Assignments)
		counts.LaunchPackets += len(snapshot.Assignments)
		counts.Artifacts += len(snapshot.Artifacts)
		counts.Handoffs += len(snapshot.Handoffs)
		counts.MemoryEntries += len(snapshot.MemoryEntries)
		counts.MemoryCandidates += len(snapshot.MemoryCandidates)
		counts.AssistantProposals += len(snapshot.AssistantProposals)
		if executionProfile, ok := cairnlinebridge.ProjectExecutionProfile(snapshot.Project); ok {
			addExecutionProfileID(executionProfile.ID)
		}
		for _, profile := range snapshot.AgentProfiles {
			addProfileID(profile.ID)
			addExecutionProfileID(cairnlinebridge.ExecutionProfile(profile).ID)
		}
		for _, role := range snapshot.Roles {
			executionProfile, ok := cairnlinebridge.RoleExecutionProfile(role)
			if !ok {
				continue
			}
			addExecutionProfileID(executionProfile.ID)
		}
	}
	counts.AgentProfiles = len(profileIDs)
	counts.ExecutionProfiles = len(executionProfileIDs)
	return counts
}

func projectCairnlineServiceAllCounts(ctx context.Context, service *cairnline.Service) (ProjectCairnlineSyncCounts, error) {
	var counts ProjectCairnlineSyncCounts
	projects, err := service.ListProjects(ctx)
	if err != nil {
		return counts, err
	}
	counts.Projects = len(projects)
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return counts, err
	}
	counts.AgentProfiles = len(profiles)
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return counts, err
	}
	counts.ExecutionProfiles = len(executionProfiles)
	for _, project := range projects {
		counts.Roots += len(project.Roots)
		counts.ContextSources += len(project.ContextSources)
		skills, err := service.ListProjectSkills(ctx, project.ID)
		if err != nil {
			return counts, err
		}
		counts.Skills += len(skills)
		roles, err := service.ListRoles(ctx, project.ID)
		if err != nil {
			return counts, err
		}
		counts.Roles += len(roles)
		workItems, err := service.ListWorkItems(ctx, project.ID)
		if err != nil {
			return counts, err
		}
		counts.WorkItems += len(workItems)
		assignments, err := service.ListAssignments(ctx, project.ID)
		if err != nil {
			return counts, err
		}
		counts.Assignments += len(assignments)
		for _, assignment := range assignments {
			packet, err := service.AssignmentLaunchPacket(ctx, project.ID, assignment.ID)
			if err != nil {
				counts.LaunchErrors++
				continue
			}
			counts.LaunchPackets++
			counts.LaunchWarnings += len(packet.Warnings)
		}
		for _, workItem := range workItems {
			artifacts, err := service.ListArtifacts(ctx, project.ID, workItem.ID)
			if err != nil {
				return counts, err
			}
			evidence, err := service.ListEvidence(ctx, project.ID, workItem.ID)
			if err != nil {
				return counts, err
			}
			reviews, err := service.ListReviews(ctx, project.ID, workItem.ID)
			if err != nil {
				return counts, err
			}
			handoffs, err := service.ListHandoffs(ctx, project.ID, workItem.ID)
			if err != nil {
				return counts, err
			}
			counts.Artifacts += len(artifacts) + len(evidence) + len(reviews)
			counts.Handoffs += len(handoffs)
		}
		memoryEntries, err := service.ListMemoryEntries(ctx, project.ID, true)
		if err != nil {
			return counts, err
		}
		counts.MemoryEntries += len(memoryEntries)
		memoryCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
			ProjectID:       project.ID,
			IncludeResolved: true,
		})
		if err != nil {
			return counts, err
		}
		counts.MemoryCandidates += len(memoryCandidates)
		assistantProposals, err := service.ListAssistantProposals(ctx, project.ID)
		if err != nil {
			return counts, err
		}
		counts.AssistantProposals += len(assistantProposals)
	}
	return counts, nil
}

func projectCairnlineSnapshotAllIDSets(snapshots []cairnlinebridge.Snapshot) ProjectCairnlineSyncIDSets {
	var ids ProjectCairnlineSyncIDSets
	seen := map[string]map[string]struct{}{}
	for _, snapshot := range snapshots {
		projectID := strings.TrimSpace(snapshot.Project.ID)
		addProjectCairnlineSyncID(seen, &ids.Projects, "projects", projectID)
		for _, root := range snapshot.Project.Roots {
			addProjectCairnlineSyncID(seen, &ids.Roots, "roots", scopedCairnlineID(projectID, root.ID))
		}
		for _, source := range snapshot.Project.ContextSources {
			addProjectCairnlineSyncID(seen, &ids.ContextSources, "context_sources", scopedCairnlineID(projectID, source.ID))
		}
		if executionProfile, ok := cairnlinebridge.ProjectExecutionProfile(snapshot.Project); ok {
			addProjectCairnlineSyncID(seen, &ids.ExecutionProfiles, "execution_profiles", executionProfile.ID)
		}
		for _, profile := range snapshot.AgentProfiles {
			addProjectCairnlineSyncID(seen, &ids.AgentProfiles, "agent_profiles", profile.ID)
			addProjectCairnlineSyncID(seen, &ids.ExecutionProfiles, "execution_profiles", cairnlinebridge.ExecutionProfile(profile).ID)
		}
		for _, skill := range snapshot.Skills {
			addProjectCairnlineSyncID(seen, &ids.Skills, "skills", scopedCairnlineID(projectID, skill.ID))
		}
		for _, role := range snapshot.Roles {
			addProjectCairnlineSyncID(seen, &ids.Roles, "roles", scopedCairnlineID(projectID, role.ID))
			if executionProfile, ok := cairnlinebridge.RoleExecutionProfile(role); ok {
				addProjectCairnlineSyncID(seen, &ids.ExecutionProfiles, "execution_profiles", executionProfile.ID)
			}
		}
		for _, item := range snapshot.WorkItems {
			addProjectCairnlineSyncID(seen, &ids.WorkItems, "work_items", scopedCairnlineID(projectID, item.ID))
		}
		for _, assignment := range snapshot.Assignments {
			assignmentID := scopedCairnlineID(projectID, assignment.ID)
			addProjectCairnlineSyncID(seen, &ids.Assignments, "assignments", assignmentID)
			addProjectCairnlineSyncID(seen, &ids.LaunchPackets, "launch_packets", assignmentID)
		}
		for _, artifact := range snapshot.Artifacts {
			addProjectCairnlineSyncID(seen, &ids.Artifacts, "artifacts", scopedCairnlineID(projectID, artifact.WorkItemID, artifact.ID))
		}
		for _, handoff := range snapshot.Handoffs {
			addProjectCairnlineSyncID(seen, &ids.Handoffs, "handoffs", scopedCairnlineID(projectID, handoff.WorkItemID, handoff.ID))
		}
		for _, entry := range snapshot.MemoryEntries {
			addProjectCairnlineSyncID(seen, &ids.MemoryEntries, "memory_entries", scopedCairnlineID(projectID, entry.ID))
		}
		for _, candidate := range snapshot.MemoryCandidates {
			addProjectCairnlineSyncID(seen, &ids.MemoryCandidates, "memory_candidates", scopedCairnlineID(projectID, candidate.ID))
		}
		for _, proposal := range snapshot.AssistantProposals {
			addProjectCairnlineSyncID(seen, &ids.AssistantProposals, "assistant_proposals", scopedCairnlineID(projectID, proposal.ID))
		}
	}
	sortProjectCairnlineSyncIDSets(&ids)
	return ids
}

func projectCairnlineServiceAllIDSets(ctx context.Context, service *cairnline.Service) (ProjectCairnlineSyncIDSets, error) {
	var ids ProjectCairnlineSyncIDSets
	seen := map[string]map[string]struct{}{}
	projects, err := service.ListProjects(ctx)
	if err != nil {
		return ids, err
	}
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return ids, err
	}
	for _, profile := range profiles {
		addProjectCairnlineSyncID(seen, &ids.AgentProfiles, "agent_profiles", profile.ID)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return ids, err
	}
	for _, profile := range executionProfiles {
		addProjectCairnlineSyncID(seen, &ids.ExecutionProfiles, "execution_profiles", profile.ID)
	}
	for _, project := range projects {
		projectID := strings.TrimSpace(project.ID)
		addProjectCairnlineSyncID(seen, &ids.Projects, "projects", projectID)
		for _, root := range project.Roots {
			addProjectCairnlineSyncID(seen, &ids.Roots, "roots", scopedCairnlineID(projectID, root.ID))
		}
		for _, source := range project.ContextSources {
			addProjectCairnlineSyncID(seen, &ids.ContextSources, "context_sources", scopedCairnlineID(projectID, source.ID))
		}
		skills, err := service.ListProjectSkills(ctx, projectID)
		if err != nil {
			return ids, err
		}
		for _, skill := range skills {
			addProjectCairnlineSyncID(seen, &ids.Skills, "skills", scopedCairnlineID(projectID, skill.ID))
		}
		roles, err := service.ListRoles(ctx, projectID)
		if err != nil {
			return ids, err
		}
		for _, role := range roles {
			addProjectCairnlineSyncID(seen, &ids.Roles, "roles", scopedCairnlineID(projectID, role.ID))
		}
		workItems, err := service.ListWorkItems(ctx, projectID)
		if err != nil {
			return ids, err
		}
		for _, workItem := range workItems {
			workItemID := strings.TrimSpace(workItem.ID)
			addProjectCairnlineSyncID(seen, &ids.WorkItems, "work_items", scopedCairnlineID(projectID, workItemID))
			artifacts, err := service.ListArtifacts(ctx, projectID, workItemID)
			if err != nil {
				return ids, err
			}
			for _, artifact := range artifacts {
				addProjectCairnlineSyncID(seen, &ids.Artifacts, "artifacts", scopedCairnlineID(projectID, workItemID, artifact.ID))
			}
			evidence, err := service.ListEvidence(ctx, projectID, workItemID)
			if err != nil {
				return ids, err
			}
			for _, item := range evidence {
				addProjectCairnlineSyncID(seen, &ids.Artifacts, "artifacts", scopedCairnlineID(projectID, workItemID, item.ID))
			}
			reviews, err := service.ListReviews(ctx, projectID, workItemID)
			if err != nil {
				return ids, err
			}
			for _, item := range reviews {
				addProjectCairnlineSyncID(seen, &ids.Artifacts, "artifacts", scopedCairnlineID(projectID, workItemID, item.ID))
			}
			handoffs, err := service.ListHandoffs(ctx, projectID, workItemID)
			if err != nil {
				return ids, err
			}
			for _, handoff := range handoffs {
				addProjectCairnlineSyncID(seen, &ids.Handoffs, "handoffs", scopedCairnlineID(projectID, workItemID, handoff.ID))
			}
		}
		assignments, err := service.ListAssignments(ctx, projectID)
		if err != nil {
			return ids, err
		}
		for _, assignment := range assignments {
			assignmentID := scopedCairnlineID(projectID, assignment.ID)
			addProjectCairnlineSyncID(seen, &ids.Assignments, "assignments", assignmentID)
			if _, err := service.AssignmentLaunchPacket(ctx, projectID, assignment.ID); err == nil {
				addProjectCairnlineSyncID(seen, &ids.LaunchPackets, "launch_packets", assignmentID)
			}
		}
		memoryEntries, err := service.ListMemoryEntries(ctx, projectID, true)
		if err != nil {
			return ids, err
		}
		for _, entry := range memoryEntries {
			addProjectCairnlineSyncID(seen, &ids.MemoryEntries, "memory_entries", scopedCairnlineID(projectID, entry.ID))
		}
		memoryCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
			ProjectID:       projectID,
			IncludeResolved: true,
		})
		if err != nil {
			return ids, err
		}
		for _, candidate := range memoryCandidates {
			addProjectCairnlineSyncID(seen, &ids.MemoryCandidates, "memory_candidates", scopedCairnlineID(projectID, candidate.ID))
		}
		assistantProposals, err := service.ListAssistantProposals(ctx, projectID)
		if err != nil {
			return ids, err
		}
		for _, proposal := range assistantProposals {
			addProjectCairnlineSyncID(seen, &ids.AssistantProposals, "assistant_proposals", scopedCairnlineID(projectID, proposal.ID))
		}
	}
	sortProjectCairnlineSyncIDSets(&ids)
	return ids, nil
}

type projectCairnlineContentDigests map[string]map[string]string

func projectCairnlineSnapshotAllContentDigests(ctx context.Context, snapshots []cairnlinebridge.Snapshot) (projectCairnlineContentDigests, error) {
	digests := projectCairnlineContentDigests{}
	service := cairnline.NewMemoryService()
	if err := cairnlinebridge.SeedSnapshots(ctx, service, snapshots); err != nil {
		return digests, err
	}
	executionProfileDigests := map[string]struct{}{}
	addExecutionProfile := func(profile cairnline.ExecutionProfile) {
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			return
		}
		if _, ok := executionProfileDigests[id]; ok {
			return
		}
		executionProfileDigests[id] = struct{}{}
		digests.add("execution_profiles", id, profile)
	}
	profileByID := map[string]agentprofiles.Profile{}
	for _, snapshot := range snapshots {
		for _, profile := range snapshot.AgentProfiles {
			profileByID[profile.ID] = profile
		}
	}
	for _, snapshot := range snapshots {
		projectID := strings.TrimSpace(snapshot.Project.ID)
		project := cairnlinebridge.Project(snapshot.Project)
		digests.add("projects", projectID, project)
		for _, root := range project.Roots {
			digests.add("roots", scopedCairnlineID(projectID, root.ID), root)
		}
		for _, source := range project.ContextSources {
			digests.add("context_sources", scopedCairnlineID(projectID, source.ID), source)
		}
		if executionProfile, ok := cairnlinebridge.ProjectExecutionProfile(snapshot.Project); ok {
			addExecutionProfile(executionProfile)
		}
		for _, profile := range snapshot.AgentProfiles {
			agentProfile := cairnlinebridge.AgentProfile(profile)
			digests.add("agent_profiles", agentProfile.ID, agentProfile)
			addExecutionProfile(cairnlinebridge.ExecutionProfile(profile))
		}
		for _, skill := range snapshot.Skills {
			item := cairnlinebridge.ProjectSkill(skill)
			digests.add("skills", scopedCairnlineID(projectID, item.ID), item)
		}
		roleByID := map[string]projectwork.AgentRoleProfile{}
		for _, role := range snapshot.Roles {
			roleByID[role.ID] = role
			item := cairnlinebridge.Role(role)
			digests.add("roles", scopedCairnlineID(projectID, item.ID), item)
			if executionProfile, ok := cairnlinebridge.RoleExecutionProfile(role); ok {
				addExecutionProfile(executionProfile)
			}
		}
		for _, item := range snapshot.WorkItems {
			workItem := cairnlinebridge.WorkItem(item)
			digests.add("work_items", scopedCairnlineID(projectID, workItem.ID), workItem)
		}
		for _, assignment := range snapshot.Assignments {
			role := roleByID[assignment.RoleID]
			profile := profileByID[role.DefaultAgentProfile]
			item := cairnlinebridge.Assignment(assignment, role, profile)
			digests.add("assignments", scopedCairnlineID(projectID, item.ID), item)
			if packet, err := service.AssignmentLaunchPacket(ctx, projectID, item.ID); err == nil {
				digests.add("launch_packets", scopedCairnlineID(projectID, item.ID), projectCairnlineStableLaunchPacket(packet))
			}
		}
		for _, artifact := range snapshot.Artifacts {
			if item, ok := cairnlinebridge.Artifact(artifact); ok {
				digests.add("artifacts", scopedCairnlineID(projectID, item.WorkItemID, item.ID), item)
				continue
			}
			if item, ok := cairnlinebridge.Evidence(artifact); ok {
				digests.add("evidence", scopedCairnlineID(projectID, item.WorkItemID, item.ID), item)
				continue
			}
			if item, ok := cairnlinebridge.Review(artifact); ok {
				digests.add("reviews", scopedCairnlineID(projectID, item.WorkItemID, item.ID), item)
			}
		}
		for _, handoff := range snapshot.Handoffs {
			item := cairnlinebridge.Handoff(handoff)
			digests.add("handoffs", scopedCairnlineID(projectID, item.WorkItemID, item.ID), item)
		}
		for _, entry := range snapshot.MemoryEntries {
			item := cairnlinebridge.MemoryEntry(entry)
			digests.add("memory_entries", scopedCairnlineID(projectID, item.ID), item)
		}
		for _, candidate := range snapshot.MemoryCandidates {
			item := cairnlinebridge.MemoryCandidate(candidate)
			digests.add("memory_candidates", scopedCairnlineID(projectID, item.ID), item)
		}
		for _, proposal := range snapshot.AssistantProposals {
			item, ok := cairnlinebridge.AssistantProposalRecord(proposal)
			if !ok {
				continue
			}
			digests.add("assistant_proposals", scopedCairnlineID(projectID, item.ID), item)
		}
	}
	return digests, nil
}

func projectCairnlineServiceAllContentDigests(ctx context.Context, service *cairnline.Service) (projectCairnlineContentDigests, error) {
	digests := projectCairnlineContentDigests{}
	projects, err := service.ListProjects(ctx)
	if err != nil {
		return digests, err
	}
	profiles, err := service.ListAgentProfiles(ctx)
	if err != nil {
		return digests, err
	}
	for _, profile := range profiles {
		digests.add("agent_profiles", profile.ID, profile)
	}
	executionProfiles, err := service.ListExecutionProfiles(ctx)
	if err != nil {
		return digests, err
	}
	for _, profile := range executionProfiles {
		digests.add("execution_profiles", profile.ID, profile)
	}
	for _, project := range projects {
		projectID := strings.TrimSpace(project.ID)
		digests.add("projects", projectID, project)
		for _, root := range project.Roots {
			digests.add("roots", scopedCairnlineID(projectID, root.ID), root)
		}
		for _, source := range project.ContextSources {
			digests.add("context_sources", scopedCairnlineID(projectID, source.ID), source)
		}
		skills, err := service.ListProjectSkills(ctx, projectID)
		if err != nil {
			return digests, err
		}
		for _, skill := range skills {
			digests.add("skills", scopedCairnlineID(projectID, skill.ID), skill)
		}
		roles, err := service.ListRoles(ctx, projectID)
		if err != nil {
			return digests, err
		}
		for _, role := range roles {
			digests.add("roles", scopedCairnlineID(projectID, role.ID), role)
		}
		workItems, err := service.ListWorkItems(ctx, projectID)
		if err != nil {
			return digests, err
		}
		for _, workItem := range workItems {
			workItemID := strings.TrimSpace(workItem.ID)
			digests.add("work_items", scopedCairnlineID(projectID, workItemID), workItem)
			artifacts, err := service.ListArtifacts(ctx, projectID, workItemID)
			if err != nil {
				return digests, err
			}
			for _, artifact := range artifacts {
				digests.add("artifacts", scopedCairnlineID(projectID, workItemID, artifact.ID), artifact)
			}
			evidence, err := service.ListEvidence(ctx, projectID, workItemID)
			if err != nil {
				return digests, err
			}
			for _, item := range evidence {
				digests.add("evidence", scopedCairnlineID(projectID, workItemID, item.ID), item)
			}
			reviews, err := service.ListReviews(ctx, projectID, workItemID)
			if err != nil {
				return digests, err
			}
			for _, item := range reviews {
				digests.add("reviews", scopedCairnlineID(projectID, workItemID, item.ID), item)
			}
			handoffs, err := service.ListHandoffs(ctx, projectID, workItemID)
			if err != nil {
				return digests, err
			}
			for _, handoff := range handoffs {
				digests.add("handoffs", scopedCairnlineID(projectID, workItemID, handoff.ID), handoff)
			}
		}
		assignments, err := service.ListAssignments(ctx, projectID)
		if err != nil {
			return digests, err
		}
		for _, assignment := range assignments {
			digests.add("assignments", scopedCairnlineID(projectID, assignment.ID), assignment)
			if packet, err := service.AssignmentLaunchPacket(ctx, projectID, assignment.ID); err == nil {
				digests.add("launch_packets", scopedCairnlineID(projectID, assignment.ID), projectCairnlineStableLaunchPacket(packet))
			}
		}
		memoryEntries, err := service.ListMemoryEntries(ctx, projectID, true)
		if err != nil {
			return digests, err
		}
		for _, entry := range memoryEntries {
			digests.add("memory_entries", scopedCairnlineID(projectID, entry.ID), entry)
		}
		memoryCandidates, err := service.ListMemoryCandidates(ctx, cairnline.MemoryCandidateFilter{
			ProjectID:       projectID,
			IncludeResolved: true,
		})
		if err != nil {
			return digests, err
		}
		for _, candidate := range memoryCandidates {
			digests.add("memory_candidates", scopedCairnlineID(projectID, candidate.ID), candidate)
		}
		assistantProposals, err := service.ListAssistantProposals(ctx, projectID)
		if err != nil {
			return digests, err
		}
		for _, proposal := range assistantProposals {
			digests.add("assistant_proposals", scopedCairnlineID(projectID, proposal.ID), proposal)
		}
	}
	return digests, nil
}

func projectCairnlineStableLaunchPacket(packet cairnline.AssignmentLaunchPacket) cairnline.AssignmentLaunchPacket {
	// Launch-packet coverage and build errors are reported by sync counts and
	// ID sets; content digests only compare packets that build on both sides.
	packet.ID = ""
	return packet
}

func projectCairnlineSyncContentDifferences(hecate, cairnline projectCairnlineContentDigests) []ProjectCairnlineContentDifference {
	var differences []ProjectCairnlineContentDifference
	paths := unionProjectCairnlineContentDigestPaths(hecate, cairnline)
	for _, path := range paths {
		left := hecate[path]
		right := cairnline[path]
		ids := intersectionProjectCairnlineContentDigestIDs(left, right)
		for _, id := range ids {
			if left[id] == right[id] {
				continue
			}
			differences = append(differences, ProjectCairnlineContentDifference{
				Path:      path,
				ID:        id,
				Hecate:    left[id],
				Cairnline: right[id],
			})
		}
	}
	return differences
}

func unionProjectCairnlineContentDigestPaths(left, right projectCairnlineContentDigests) []string {
	seen := map[string]struct{}{}
	for path := range left {
		seen[path] = struct{}{}
	}
	for path := range right {
		seen[path] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func intersectionProjectCairnlineContentDigestIDs(left, right map[string]string) []string {
	if len(left) == 0 || len(right) == 0 {
		return nil
	}
	ids := make([]string, 0, len(left))
	for id := range left {
		if _, ok := right[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (digests projectCairnlineContentDigests) add(path, id string, value any) {
	path = strings.TrimSpace(path)
	id = strings.TrimSpace(id)
	if path == "" || id == "" {
		return
	}
	if digests[path] == nil {
		digests[path] = map[string]string{}
	}
	digests[path][id] = projectCairnlineContentDigest(value)
}

func projectCairnlineContentDigest(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte("{}")
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err == nil {
		stripVolatileProjectCairnlineDigestFields(decoded)
		if canonicalPayload, err := json.Marshal(decoded); err == nil {
			payload = canonicalPayload
		}
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func stripVolatileProjectCairnlineDigestFields(value any) {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"created_at", "updated_at", "discovered_at", "applied_at"} {
			delete(item, key)
		}
		for _, child := range item {
			stripVolatileProjectCairnlineDigestFields(child)
		}
	case []any:
		for _, child := range item {
			stripVolatileProjectCairnlineDigestFields(child)
		}
	}
}

func projectCairnlineSyncIDDifferences(hecate, cairnline ProjectCairnlineSyncIDSets) []ProjectCairnlineIDDifference {
	var differences []ProjectCairnlineIDDifference
	differences = appendProjectCairnlineIDDifference(differences, "projects", hecate.Projects, cairnline.Projects)
	differences = appendProjectCairnlineIDDifference(differences, "roots", hecate.Roots, cairnline.Roots)
	differences = appendProjectCairnlineIDDifference(differences, "context_sources", hecate.ContextSources, cairnline.ContextSources)
	differences = appendProjectCairnlineIDDifference(differences, "agent_profiles", hecate.AgentProfiles, cairnline.AgentProfiles)
	differences = appendProjectCairnlineIDDifference(differences, "execution_profiles", hecate.ExecutionProfiles, cairnline.ExecutionProfiles)
	differences = appendProjectCairnlineIDDifference(differences, "skills", hecate.Skills, cairnline.Skills)
	differences = appendProjectCairnlineIDDifference(differences, "roles", hecate.Roles, cairnline.Roles)
	differences = appendProjectCairnlineIDDifference(differences, "work_items", hecate.WorkItems, cairnline.WorkItems)
	differences = appendProjectCairnlineIDDifference(differences, "assignments", hecate.Assignments, cairnline.Assignments)
	differences = appendProjectCairnlineIDDifference(differences, "artifacts", hecate.Artifacts, cairnline.Artifacts)
	differences = appendProjectCairnlineIDDifference(differences, "handoffs", hecate.Handoffs, cairnline.Handoffs)
	differences = appendProjectCairnlineIDDifference(differences, "memory_entries", hecate.MemoryEntries, cairnline.MemoryEntries)
	differences = appendProjectCairnlineIDDifference(differences, "memory_candidates", hecate.MemoryCandidates, cairnline.MemoryCandidates)
	differences = appendProjectCairnlineIDDifference(differences, "assistant_proposals", hecate.AssistantProposals, cairnline.AssistantProposals)
	differences = appendProjectCairnlineIDDifference(differences, "launch_packets", hecate.LaunchPackets, cairnline.LaunchPackets)
	return differences
}

func appendProjectCairnlineIDDifference(differences []ProjectCairnlineIDDifference, path string, hecate, cairnline []string) []ProjectCairnlineIDDifference {
	if equalStringSlices(hecate, cairnline) {
		return differences
	}
	return append(differences, ProjectCairnlineIDDifference{
		Path:      path,
		Hecate:    append([]string(nil), hecate...),
		Cairnline: append([]string(nil), cairnline...),
	})
}

func addProjectCairnlineSyncID(seen map[string]map[string]struct{}, list *[]string, path, id string) {
	id = strings.TrimSpace(id)
	if id == "" || list == nil {
		return
	}
	if seen[path] == nil {
		seen[path] = map[string]struct{}{}
	}
	if _, ok := seen[path][id]; ok {
		return
	}
	seen[path][id] = struct{}{}
	*list = append(*list, id)
}

func sortProjectCairnlineSyncIDSets(ids *ProjectCairnlineSyncIDSets) {
	if ids == nil {
		return
	}
	sort.Strings(ids.Projects)
	sort.Strings(ids.Roots)
	sort.Strings(ids.ContextSources)
	sort.Strings(ids.AgentProfiles)
	sort.Strings(ids.ExecutionProfiles)
	sort.Strings(ids.Skills)
	sort.Strings(ids.Roles)
	sort.Strings(ids.WorkItems)
	sort.Strings(ids.Assignments)
	sort.Strings(ids.Artifacts)
	sort.Strings(ids.Handoffs)
	sort.Strings(ids.MemoryEntries)
	sort.Strings(ids.MemoryCandidates)
	sort.Strings(ids.AssistantProposals)
	sort.Strings(ids.LaunchPackets)
}

func scopedCairnlineID(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return ""
		}
		out = append(out, part)
	}
	return strings.Join(out, "/")
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func projectCairnlineSyncDifferences(hecate, cairnline ProjectCairnlineSyncCounts) []ProjectCairnlineParityDifference {
	var differences []ProjectCairnlineParityDifference
	differences = appendProjectCairnlineParityDifference(differences, "projects", hecate.Projects, cairnline.Projects)
	differences = appendProjectCairnlineParityDifference(differences, "roots", hecate.Roots, cairnline.Roots)
	differences = appendProjectCairnlineParityDifference(differences, "context_sources", hecate.ContextSources, cairnline.ContextSources)
	differences = appendProjectCairnlineParityDifference(differences, "agent_profiles", hecate.AgentProfiles, cairnline.AgentProfiles)
	differences = appendProjectCairnlineParityDifference(differences, "execution_profiles", hecate.ExecutionProfiles, cairnline.ExecutionProfiles)
	differences = appendProjectCairnlineParityDifference(differences, "skills", hecate.Skills, cairnline.Skills)
	differences = appendProjectCairnlineParityDifference(differences, "roles", hecate.Roles, cairnline.Roles)
	differences = appendProjectCairnlineParityDifference(differences, "work_items", hecate.WorkItems, cairnline.WorkItems)
	differences = appendProjectCairnlineParityDifference(differences, "assignments", hecate.Assignments, cairnline.Assignments)
	differences = appendProjectCairnlineParityDifference(differences, "artifacts", hecate.Artifacts, cairnline.Artifacts)
	differences = appendProjectCairnlineParityDifference(differences, "handoffs", hecate.Handoffs, cairnline.Handoffs)
	differences = appendProjectCairnlineParityDifference(differences, "memory_entries", hecate.MemoryEntries, cairnline.MemoryEntries)
	differences = appendProjectCairnlineParityDifference(differences, "memory_candidates", hecate.MemoryCandidates, cairnline.MemoryCandidates)
	differences = appendProjectCairnlineParityDifference(differences, "assistant_proposals", hecate.AssistantProposals, cairnline.AssistantProposals)
	differences = appendProjectCairnlineParityDifference(differences, "launch_packets", hecate.LaunchPackets, cairnline.LaunchPackets)
	differences = appendProjectCairnlineParityDifference(differences, "launch_warnings", hecate.LaunchWarnings, cairnline.LaunchWarnings)
	differences = appendProjectCairnlineParityDifference(differences, "launch_errors", hecate.LaunchErrors, cairnline.LaunchErrors)
	return differences
}

type projectCairnlineLaunchPacketSummaryData struct {
	Count        int
	WarningCount int
	Errors       []ProjectCairnlineLaunchPacketError
}

func projectCairnlineServiceLaunchPacketSummary(ctx context.Context, service *cairnline.Service, projectID string) (projectCairnlineLaunchPacketSummaryData, error) {
	var summary projectCairnlineLaunchPacketSummaryData
	if service == nil {
		return summary, errors.New("cairnline service is not configured")
	}
	assignments, err := service.ListAssignments(ctx, projectID)
	if err != nil {
		return summary, err
	}
	for _, assignment := range assignments {
		packet, err := service.AssignmentLaunchPacket(ctx, projectID, assignment.ID)
		if err != nil {
			summary.Errors = append(summary.Errors, ProjectCairnlineLaunchPacketError{
				AssignmentID: assignment.ID,
				Error:        err.Error(),
			})
			continue
		}
		summary.Count++
		summary.WarningCount += len(packet.Warnings)
	}
	return summary, nil
}

func (h *Handler) nativeProjectAssistantProposalCount(ctx context.Context, projectID string) (int, error) {
	if h.projectAssistantProposals == nil {
		return 0, nil
	}
	proposals, err := h.projectAssistantProposals.ListProposals(ctx, projectID)
	if err != nil {
		return 0, err
	}
	return len(proposals), nil
}

func projectCairnlineParityReport(projectID string, nativeGraph ProjectCairnlineGraphParityCounts, nativeWorkItems []ProjectWorkItemResponse, nativeCollaboration ProjectCairnlineCollaborationParityCounts, nativeActivity ProjectActivityDataResponse, nativeOperations ProjectOperationsBriefResponse, cairnlineWorkItems []ProjectWorkItemResponse, cairnlineCollaboration ProjectCairnlineCollaborationParityCounts, cairnlineOperations ProjectOperationsBriefResponse, nativeAssistantProposals int, readModel ProjectCairnlineReadModelResponseItem) ProjectCairnlineParityReportResponseItem {
	nativeCollaboration = normalizeProjectCairnlineCollaborationParityCounts(nativeCollaboration)
	cairnlineCollaboration = normalizeProjectCairnlineCollaborationParityCounts(cairnlineCollaboration)
	hecate := ProjectCairnlineParitySnapshot{
		Graph:         nativeGraph,
		WorkItems:     projectCairnlineWorkItemParityCounts(nativeWorkItems),
		Collaboration: nativeCollaboration,
		Activity: ProjectCairnlineActivityParityCounts{
			WorkItems:   nativeActivity.Summary.WorkItemCount,
			Assignments: nativeActivity.Summary.AssignmentCount,
			Active:      nativeActivity.Summary.ActiveCount,
			Blocked:     nativeActivity.Summary.BlockedCount,
			Completed:   nativeActivity.Summary.CompletedCount,
			Recent:      nativeActivity.Summary.RecentCount,
		},
		Operations: projectCairnlineOperationsParityCounts(nativeOperations),
		Assistant: ProjectCairnlineAssistantParityCounts{
			Proposals: nativeAssistantProposals,
		},
		LaunchPackets: ProjectCairnlineLaunchPacketParityCounts{
			Assignments: nativeActivity.Summary.AssignmentCount,
			Warnings:    0,
			Errors:      0,
		},
	}
	cairnline := ProjectCairnlineParitySnapshot{
		Graph: ProjectCairnlineGraphParityCounts{
			Roots:             readModel.RootCount,
			ContextSources:    readModel.ContextSourceCount,
			AgentProfiles:     readModel.AgentProfileCount,
			ExecutionProfiles: readModel.ExecutionProfileCount,
			Skills:            readModel.SkillCount,
			Roles:             readModel.RoleCount,
			WorkItems:         readModel.WorkItemCount,
			Assignments:       readModel.AssignmentCount,
			Artifacts:         readModel.ArtifactCount,
			Handoffs:          readModel.HandoffCount,
			MemoryEntries:     readModel.MemoryEntryCount,
			MemoryCandidates:  readModel.MemoryCandidateCount,
		},
		WorkItems:     projectCairnlineWorkItemParityCounts(cairnlineWorkItems),
		Collaboration: cairnlineCollaboration,
		Activity: ProjectCairnlineActivityParityCounts{
			WorkItems:   readModel.WorkItemCount,
			Assignments: readModel.Activity.Counts.Assignments,
			Active:      readModel.Activity.Counts.Active,
			Blocked:     readModel.Activity.Counts.Blocked,
			Completed:   readModel.Activity.Counts.Completed,
			Recent:      len(readModel.Activity.Buckets.Recent),
		},
		Operations: projectCairnlineOperationsParityCounts(cairnlineOperations),
		Assistant: ProjectCairnlineAssistantParityCounts{
			Proposals: readModel.AssistantProposalCount,
		},
		LaunchPackets: ProjectCairnlineLaunchPacketParityCounts{
			Assignments: readModel.LaunchPacketCount,
			Warnings:    readModel.LaunchPacketWarningCount,
			Errors:      len(readModel.LaunchPacketErrors),
		},
	}
	differences := projectCairnlineParityDifferences(hecate, cairnline)
	return ProjectCairnlineParityReportResponseItem{
		ProjectID:    projectID,
		ReadSource:   readModel.ReadSource,
		DatabasePath: readModel.DatabasePath,
		Match:        len(differences) == 0,
		Differences:  differences,
		Hecate:       hecate,
		Cairnline:    cairnline,
	}
}

func projectCairnlineWorkItemParityCounts(items []ProjectWorkItemResponse) ProjectCairnlineWorkItemParityCounts {
	counts := ProjectCairnlineWorkItemParityCounts{
		Items: len(items),
	}
	for _, item := range items {
		if len(item.Assignments) == 0 {
			counts.UnassignedItems++
			continue
		}
		counts.EmbeddedAssignments += len(item.Assignments)
	}
	return counts
}

func normalizeProjectCairnlineCollaborationParityCounts(counts ProjectCairnlineCollaborationParityCounts) ProjectCairnlineCollaborationParityCounts {
	if counts.ArtifactKindCounts == nil {
		counts.ArtifactKindCounts = map[string]int{}
	}
	if counts.HandoffStatusCounts == nil {
		counts.HandoffStatusCounts = map[string]int{}
	}
	return counts
}

func (h *Handler) nativeProjectCollaborationParityCounts(ctx context.Context, projectID string) (ProjectCairnlineCollaborationParityCounts, error) {
	artifacts, err := h.projectWork.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return ProjectCairnlineCollaborationParityCounts{}, err
	}
	handoffs, err := h.projectWork.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return ProjectCairnlineCollaborationParityCounts{}, err
	}
	renderedArtifacts := make([]ProjectWorkArtifactResponse, 0, len(artifacts))
	for _, artifact := range artifacts {
		renderedArtifacts = append(renderedArtifacts, renderProjectWorkArtifact(artifact))
	}
	renderedHandoffs := make([]ProjectHandoffResponse, 0, len(handoffs))
	for _, handoff := range handoffs {
		renderedHandoffs = append(renderedHandoffs, renderProjectHandoff(handoff))
	}
	return projectCairnlineCollaborationParityCounts(renderedArtifacts, renderedHandoffs), nil
}

func cairnlineProjectCollaborationParityCountsFromService(ctx context.Context, service *cairnline.Service, projectID string) (ProjectCairnlineCollaborationParityCounts, error) {
	workItems, err := service.ListWorkItems(ctx, projectID)
	if err != nil {
		return ProjectCairnlineCollaborationParityCounts{}, err
	}
	var renderedArtifacts []ProjectWorkArtifactResponse
	for _, workItem := range workItems {
		artifacts, err := cairnlineProjectWorkArtifacts(ctx, service, projectID, workItem.ID)
		if err != nil {
			return ProjectCairnlineCollaborationParityCounts{}, err
		}
		for _, artifact := range artifacts {
			renderedArtifacts = append(renderedArtifacts, renderProjectWorkArtifact(artifact))
		}
	}
	handoffs, err := cairnlineProjectHandoffs(ctx, service, projectID, "", "")
	if err != nil {
		return ProjectCairnlineCollaborationParityCounts{}, err
	}
	renderedHandoffs := make([]ProjectHandoffResponse, 0, len(handoffs))
	for _, handoff := range handoffs {
		renderedHandoffs = append(renderedHandoffs, renderProjectHandoff(handoff))
	}
	return projectCairnlineCollaborationParityCounts(renderedArtifacts, renderedHandoffs), nil
}

func projectCairnlineCollaborationParityCounts(artifacts []ProjectWorkArtifactResponse, handoffs []ProjectHandoffResponse) ProjectCairnlineCollaborationParityCounts {
	counts := ProjectCairnlineCollaborationParityCounts{
		Artifacts:           len(artifacts),
		Handoffs:            len(handoffs),
		ArtifactKindCounts:  map[string]int{},
		HandoffStatusCounts: map[string]int{},
	}
	for _, item := range artifacts {
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			continue
		}
		counts.ArtifactKindCounts[kind]++
	}
	for _, item := range handoffs {
		status := strings.TrimSpace(item.Status)
		if status == "" {
			continue
		}
		counts.HandoffStatusCounts[status]++
	}
	return counts
}

func projectCairnlineOperationsParityCounts(operations ProjectOperationsBriefResponse) ProjectCairnlineOperationsParityCounts {
	counts := ProjectCairnlineOperationsParityCounts{
		ItemCount:               operations.Summary.ItemCount,
		AvailableItemCount:      operations.Summary.AvailableItemCount,
		OmittedItemCount:        operations.Summary.OmittedItemCount,
		ItemLimit:               operations.Summary.ItemLimit,
		HighCount:               operations.Summary.HighCount,
		MediumCount:             operations.Summary.MediumCount,
		LowCount:                operations.Summary.LowCount,
		PendingMemoryCandidates: operations.Summary.PendingMemoryCandidateCount,
		OpenHandoffs:            operations.Summary.PendingHandoffCount,
		KindCounts:              map[string]int{},
	}
	for _, item := range operations.Items {
		kind := strings.TrimSpace(item.Kind)
		if kind == "" {
			continue
		}
		counts.KindCounts[kind]++
	}
	return counts
}

func projectCairnlineParityDifferences(hecate, cairnline ProjectCairnlineParitySnapshot) []ProjectCairnlineParityDifference {
	var differences []ProjectCairnlineParityDifference
	differences = appendProjectCairnlineParityDifference(differences, "graph.roots", hecate.Graph.Roots, cairnline.Graph.Roots)
	differences = appendProjectCairnlineParityDifference(differences, "graph.context_sources", hecate.Graph.ContextSources, cairnline.Graph.ContextSources)
	differences = appendProjectCairnlineParityDifference(differences, "graph.agent_profiles", hecate.Graph.AgentProfiles, cairnline.Graph.AgentProfiles)
	differences = appendProjectCairnlineParityDifference(differences, "graph.execution_profiles", hecate.Graph.ExecutionProfiles, cairnline.Graph.ExecutionProfiles)
	differences = appendProjectCairnlineParityDifference(differences, "graph.skills", hecate.Graph.Skills, cairnline.Graph.Skills)
	differences = appendProjectCairnlineParityDifference(differences, "graph.roles", hecate.Graph.Roles, cairnline.Graph.Roles)
	differences = appendProjectCairnlineParityDifference(differences, "graph.work_items", hecate.Graph.WorkItems, cairnline.Graph.WorkItems)
	differences = appendProjectCairnlineParityDifference(differences, "graph.assignments", hecate.Graph.Assignments, cairnline.Graph.Assignments)
	differences = appendProjectCairnlineParityDifference(differences, "graph.artifacts", hecate.Graph.Artifacts, cairnline.Graph.Artifacts)
	differences = appendProjectCairnlineParityDifference(differences, "graph.handoffs", hecate.Graph.Handoffs, cairnline.Graph.Handoffs)
	differences = appendProjectCairnlineParityDifference(differences, "graph.memory_entries", hecate.Graph.MemoryEntries, cairnline.Graph.MemoryEntries)
	differences = appendProjectCairnlineParityDifference(differences, "graph.memory_candidates", hecate.Graph.MemoryCandidates, cairnline.Graph.MemoryCandidates)
	differences = appendProjectCairnlineParityDifference(differences, "work_items.items", hecate.WorkItems.Items, cairnline.WorkItems.Items)
	differences = appendProjectCairnlineParityDifference(differences, "work_items.embedded_assignments", hecate.WorkItems.EmbeddedAssignments, cairnline.WorkItems.EmbeddedAssignments)
	differences = appendProjectCairnlineParityDifference(differences, "work_items.unassigned_items", hecate.WorkItems.UnassignedItems, cairnline.WorkItems.UnassignedItems)
	differences = appendProjectCairnlineParityDifference(differences, "collaboration.artifacts", hecate.Collaboration.Artifacts, cairnline.Collaboration.Artifacts)
	differences = appendProjectCairnlineParityDifference(differences, "collaboration.handoffs", hecate.Collaboration.Handoffs, cairnline.Collaboration.Handoffs)
	differences = appendProjectCairnlineParityMapDifferences(differences, "collaboration.artifact_kind_counts", hecate.Collaboration.ArtifactKindCounts, cairnline.Collaboration.ArtifactKindCounts)
	differences = appendProjectCairnlineParityMapDifferences(differences, "collaboration.handoff_status_counts", hecate.Collaboration.HandoffStatusCounts, cairnline.Collaboration.HandoffStatusCounts)
	differences = appendProjectCairnlineParityDifference(differences, "activity.work_items", hecate.Activity.WorkItems, cairnline.Activity.WorkItems)
	differences = appendProjectCairnlineParityDifference(differences, "activity.assignments", hecate.Activity.Assignments, cairnline.Activity.Assignments)
	differences = appendProjectCairnlineParityDifference(differences, "activity.active", hecate.Activity.Active, cairnline.Activity.Active)
	differences = appendProjectCairnlineParityDifference(differences, "activity.blocked", hecate.Activity.Blocked, cairnline.Activity.Blocked)
	differences = appendProjectCairnlineParityDifference(differences, "activity.completed", hecate.Activity.Completed, cairnline.Activity.Completed)
	differences = appendProjectCairnlineParityDifference(differences, "activity.recent", hecate.Activity.Recent, cairnline.Activity.Recent)
	differences = appendProjectCairnlineParityDifference(differences, "operations.item_count", hecate.Operations.ItemCount, cairnline.Operations.ItemCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.available_item_count", hecate.Operations.AvailableItemCount, cairnline.Operations.AvailableItemCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.omitted_item_count", hecate.Operations.OmittedItemCount, cairnline.Operations.OmittedItemCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.item_limit", hecate.Operations.ItemLimit, cairnline.Operations.ItemLimit)
	differences = appendProjectCairnlineParityDifference(differences, "operations.high_count", hecate.Operations.HighCount, cairnline.Operations.HighCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.medium_count", hecate.Operations.MediumCount, cairnline.Operations.MediumCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.low_count", hecate.Operations.LowCount, cairnline.Operations.LowCount)
	differences = appendProjectCairnlineParityDifference(differences, "operations.pending_memory_candidates", hecate.Operations.PendingMemoryCandidates, cairnline.Operations.PendingMemoryCandidates)
	differences = appendProjectCairnlineParityDifference(differences, "operations.open_handoffs", hecate.Operations.OpenHandoffs, cairnline.Operations.OpenHandoffs)
	differences = appendProjectCairnlineParityMapDifferences(differences, "operations.kind_counts", hecate.Operations.KindCounts, cairnline.Operations.KindCounts)
	differences = appendProjectCairnlineParityDifference(differences, "assistant.proposals", hecate.Assistant.Proposals, cairnline.Assistant.Proposals)
	differences = appendProjectCairnlineParityDifference(differences, "launch_packets.assignments", hecate.LaunchPackets.Assignments, cairnline.LaunchPackets.Assignments)
	differences = appendProjectCairnlineParityDifference(differences, "launch_packets.warnings", hecate.LaunchPackets.Warnings, cairnline.LaunchPackets.Warnings)
	differences = appendProjectCairnlineParityDifference(differences, "launch_packets.errors", hecate.LaunchPackets.Errors, cairnline.LaunchPackets.Errors)
	return differences
}

func appendProjectCairnlineParityMapDifferences(differences []ProjectCairnlineParityDifference, prefix string, hecate, cairnline map[string]int) []ProjectCairnlineParityDifference {
	keys := make(map[string]struct{}, len(hecate)+len(cairnline))
	for key := range hecate {
		keys[key] = struct{}{}
	}
	for key := range cairnline {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		differences = appendProjectCairnlineParityDifference(differences, prefix+"."+key, hecate[key], cairnline[key])
	}
	return differences
}

func appendProjectCairnlineParityDifference(differences []ProjectCairnlineParityDifference, path string, hecate, cairnline int) []ProjectCairnlineParityDifference {
	if hecate == cairnline {
		return differences
	}
	return append(differences, ProjectCairnlineParityDifference{
		Path:      path,
		Hecate:    hecate,
		Cairnline: cairnline,
	})
}

func (h *Handler) cairnlineSnapshotSources() cairnlinebridge.SnapshotSources {
	return cairnlinebridge.SnapshotSources{
		Projects:         h.projects,
		AgentProfiles:    h.agentProfiles,
		Skills:           h.projectSkills,
		Work:             h.projectWork,
		Memory:           h.memory,
		MemoryCandidates: h.memoryCandidates,
		Proposals:        h.projectAssistantProposals,
	}
}

func (h *Handler) cairnlineExportPath(projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", errors.New("project id is required")
	}
	dataDir := strings.TrimSpace(h.config.Server.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	path := filepath.Join(dataDir, "cairnline", "projects", safeCairnlineExportName(projectID)+".db")
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

func (h *Handler) cairnlineEmbeddedDatabasePath() string {
	dataDir := strings.TrimSpace(h.config.Server.DataDir)
	if dataDir == "" {
		dataDir = ".data"
	}
	path := filepath.Join(dataDir, "cairnline", "embedded", "projects.db")
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}

func safeCairnlineExportName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "project"
	}
	return out
}

func removeCairnlineSQLiteFiles(dbPath string) error {
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
