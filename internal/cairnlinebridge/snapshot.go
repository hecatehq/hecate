package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projectassistant"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

var ErrSourceNotConfigured = errors.New("cairnline bridge source not configured")

type SnapshotSources struct {
	Projects         projects.Store
	Skills           projectskills.Store
	Work             projectwork.Store
	Memory           memory.Store
	MemoryCandidates memory.CandidateStore
	Proposals        projectassistant.ProposalStore
}

func LoadSnapshots(ctx context.Context, sources SnapshotSources) ([]Snapshot, error) {
	if sources.Projects == nil {
		return nil, errors.Join(ErrSourceNotConfigured, errors.New("projects store is required"))
	}
	projects, err := sources.Projects.List(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(projects))
	for _, project := range projects {
		snapshot, err := LoadSnapshot(ctx, sources, project.ID)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func SeedSnapshots(ctx context.Context, service *cairnline.Service, snapshots []Snapshot) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	_, err := service.ImportSnapshot(ctx, CairnlineSnapshot(snapshots))
	return err
}

func CairnlineSnapshot(snapshots []Snapshot) cairnline.Snapshot {
	out := cairnline.Snapshot{
		Version: cairnline.SnapshotVersion,
	}
	rolesByID := make(map[string]projectwork.AgentRoleProfile)
	for _, snapshot := range snapshots {
		out.Projects = append(out.Projects, Project(snapshot.Project))
		for _, skill := range snapshot.Skills {
			out.ProjectSkills = append(out.ProjectSkills, ProjectSkill(skill))
		}
		for _, role := range snapshot.Roles {
			rolesByID[role.ID] = role
			out.Roles = append(out.Roles, Role(role))
		}
		for _, item := range snapshot.WorkItems {
			out.WorkItems = append(out.WorkItems, WorkItem(item))
		}
		for _, assignment := range snapshot.Assignments {
			role := rolesByID[assignment.RoleID]
			out.Assignments = append(out.Assignments, Assignment(assignment, role))
		}
		for _, artifact := range snapshot.Artifacts {
			if item, ok := Artifact(artifact); ok {
				out.Artifacts = append(out.Artifacts, item)
				continue
			}
			if item, ok := Evidence(artifact); ok {
				out.Evidence = append(out.Evidence, item)
				continue
			}
			if item, ok := Review(artifact); ok {
				out.Reviews = append(out.Reviews, item)
			}
		}
		for _, handoff := range snapshot.Handoffs {
			out.Handoffs = append(out.Handoffs, Handoff(handoff))
		}
		for _, entry := range snapshot.MemoryEntries {
			out.MemoryEntries = append(out.MemoryEntries, MemoryEntry(entry))
		}
		for _, candidate := range snapshot.MemoryCandidates {
			out.MemoryCandidates = append(out.MemoryCandidates, MemoryCandidate(candidate))
		}
		for _, proposal := range snapshot.AssistantProposals {
			item, ok := AssistantProposalRecord(proposal)
			if !ok {
				continue
			}
			out.AssistantProposals = append(out.AssistantProposals, item)
		}
	}
	return out
}

// CairnlineSnapshotForProject narrows an exported portable snapshot to one
// project graph. Cairnline imports are upserts, so rollback callers must not
// carry unrelated rows that may have changed after the export.
func CairnlineSnapshotForProject(snapshot cairnline.Snapshot, projectID string) cairnline.Snapshot {
	projectID = strings.TrimSpace(projectID)
	return cairnline.Snapshot{
		Version:            snapshot.Version,
		ExportedAt:         snapshot.ExportedAt,
		Projects:           snapshotRowsForProject(snapshot.Projects, projectID, func(item cairnline.Project) string { return item.ID }),
		ProjectSkills:      snapshotRowsForProject(snapshot.ProjectSkills, projectID, func(item cairnline.ProjectSkill) string { return item.ProjectID }),
		Roles:              snapshotRowsForProject(snapshot.Roles, projectID, func(item cairnline.Role) string { return item.ProjectID }),
		WorkItems:          snapshotRowsForProject(snapshot.WorkItems, projectID, func(item cairnline.WorkItem) string { return item.ProjectID }),
		Assignments:        snapshotRowsForProject(snapshot.Assignments, projectID, func(item cairnline.Assignment) string { return item.ProjectID }),
		Artifacts:          snapshotRowsForProject(snapshot.Artifacts, projectID, func(item cairnline.Artifact) string { return item.ProjectID }),
		Evidence:           snapshotRowsForProject(snapshot.Evidence, projectID, func(item cairnline.Evidence) string { return item.ProjectID }),
		Reviews:            snapshotRowsForProject(snapshot.Reviews, projectID, func(item cairnline.Review) string { return item.ProjectID }),
		Handoffs:           snapshotRowsForProject(snapshot.Handoffs, projectID, func(item cairnline.Handoff) string { return item.ProjectID }),
		MemoryEntries:      snapshotRowsForProject(snapshot.MemoryEntries, projectID, func(item cairnline.MemoryEntry) string { return item.ProjectID }),
		MemoryCandidates:   snapshotRowsForProject(snapshot.MemoryCandidates, projectID, func(item cairnline.MemoryCandidate) string { return item.ProjectID }),
		AssistantProposals: snapshotRowsForProject(snapshot.AssistantProposals, projectID, func(item cairnline.AssistantProposalRecord) string { return item.ProjectID }),
	}
}

func snapshotRowsForProject[T any](items []T, projectID string, projectIDFor func(T) string) []T {
	var filtered []T
	for _, item := range items {
		if projectIDFor(item) == projectID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func SeedProjectFromStores(ctx context.Context, service *cairnline.Service, sources SnapshotSources, projectID string) (Snapshot, error) {
	if service == nil {
		return Snapshot{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	snapshot, err := LoadSnapshot(ctx, sources, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := SeedSnapshots(ctx, service, []Snapshot{snapshot}); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func ProjectOperationsBriefFromStores(ctx context.Context, sources SnapshotSources, projectID string) (cairnline.ProjectOperationsBrief, Snapshot, error) {
	service := cairnline.NewMemoryService()
	snapshot, err := SeedProjectFromStores(ctx, service, sources, projectID)
	if err != nil {
		return cairnline.ProjectOperationsBrief{}, Snapshot{}, err
	}
	brief, err := service.ProjectOperationsBrief(ctx, snapshot.Project.ID)
	if err != nil {
		return cairnline.ProjectOperationsBrief{}, Snapshot{}, err
	}
	return brief, snapshot, nil
}

func ProjectActivityFromStores(ctx context.Context, sources SnapshotSources, projectID string) (cairnline.ProjectActivity, Snapshot, error) {
	service := cairnline.NewMemoryService()
	snapshot, err := SeedProjectFromStores(ctx, service, sources, projectID)
	if err != nil {
		return cairnline.ProjectActivity{}, Snapshot{}, err
	}
	activity, err := service.ProjectActivity(ctx, snapshot.Project.ID)
	if err != nil {
		return cairnline.ProjectActivity{}, Snapshot{}, err
	}
	return activity, snapshot, nil
}

func LoadSnapshot(ctx context.Context, sources SnapshotSources, projectID string) (Snapshot, error) {
	projectID = strings.TrimSpace(projectID)
	if sources.Projects == nil {
		return Snapshot{}, errors.Join(ErrSourceNotConfigured, errors.New("projects store is required"))
	}
	if sources.Work == nil {
		return Snapshot{}, errors.Join(ErrSourceNotConfigured, errors.New("project work store is required"))
	}
	project, ok, err := sources.Projects.Get(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		return Snapshot{}, projects.ErrNotFound
	}
	var skills []projectskills.Skill
	if sources.Skills != nil {
		skills, err = sources.Skills.List(ctx, projectID)
		if err != nil {
			return Snapshot{}, err
		}
	}
	roles, err := sources.Work.ListRoles(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	workItems, err := sources.Work.ListWorkItems(ctx, projectID)
	if err != nil {
		return Snapshot{}, err
	}
	assignments, err := sources.Work.ListAssignments(ctx, projectwork.AssignmentFilter{ProjectID: projectID})
	if err != nil {
		return Snapshot{}, err
	}
	artifacts, err := sources.Work.ListArtifacts(ctx, projectwork.ArtifactFilter{ProjectID: projectID})
	if err != nil {
		return Snapshot{}, err
	}
	handoffs, err := sources.Work.ListHandoffs(ctx, projectwork.HandoffFilter{ProjectID: projectID})
	if err != nil {
		return Snapshot{}, err
	}
	var memoryCandidates []memory.Candidate
	var memoryEntries []memory.Entry
	if sources.Memory != nil {
		memoryEntries, err = sources.Memory.List(ctx, memory.Filter{
			ProjectID:       projectID,
			IncludeDisabled: true,
		})
		if err != nil {
			return Snapshot{}, err
		}
	}
	if sources.MemoryCandidates != nil {
		memoryCandidates, err = sources.MemoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
			ProjectID: projectID,
		})
		if err != nil {
			return Snapshot{}, err
		}
	}
	var assistantProposals []projectassistant.ProposalRecord
	if sources.Proposals != nil {
		assistantProposals, err = sources.Proposals.ListProposals(ctx, projectID)
		if err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{
		Project:            project,
		Skills:             skills,
		Roles:              roles,
		WorkItems:          workItems,
		Assignments:        assignments,
		Artifacts:          artifacts,
		Handoffs:           handoffs,
		MemoryEntries:      memoryEntries,
		MemoryCandidates:   memoryCandidates,
		AssistantProposals: assistantProposals,
	}, nil
}
