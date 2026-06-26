package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/memory"
	"github.com/hecatehq/hecate/internal/projects"
	"github.com/hecatehq/hecate/internal/projectskills"
	"github.com/hecatehq/hecate/internal/projectwork"
)

var ErrSourceNotConfigured = errors.New("cairnline bridge source not configured")

type SnapshotSources struct {
	Projects         projects.Store
	AgentProfiles    agentprofiles.Store
	Skills           projectskills.Store
	Work             projectwork.Store
	MemoryCandidates memory.CandidateStore
}

func LoadSnapshot(ctx context.Context, sources SnapshotSources, projectID string) (Snapshot, error) {
	projectID = strings.TrimSpace(projectID)
	if sources.Projects == nil {
		return Snapshot{}, errors.Join(ErrSourceNotConfigured, errors.New("projects store is required"))
	}
	if sources.AgentProfiles == nil {
		return Snapshot{}, errors.Join(ErrSourceNotConfigured, errors.New("agent profiles store is required"))
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
	profiles, err := sources.AgentProfiles.List(ctx)
	if err != nil {
		return Snapshot{}, err
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
	if sources.MemoryCandidates != nil {
		memoryCandidates, err = sources.MemoryCandidates.ListCandidates(ctx, memory.CandidateFilter{
			ProjectID: projectID,
			Status:    memory.CandidateStatusPending,
		})
		if err != nil {
			return Snapshot{}, err
		}
	}
	return Snapshot{
		Project:          project,
		AgentProfiles:    profiles,
		Skills:           skills,
		Roles:            roles,
		WorkItems:        workItems,
		Assignments:      assignments,
		Artifacts:        artifacts,
		Handoffs:         handoffs,
		MemoryCandidates: memoryCandidates,
	}, nil
}
