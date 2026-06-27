package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projects"
)

// UpsertProject is the first non-authoritative Cairnline write-adapter seam for
// Hecate project identity plus embedded root/context-source state. Live Hecate
// routes still write Hecate stores; this function proves the portable service
// can accept the same mutation shape before authority moves.
func UpsertProject(ctx context.Context, service *cairnline.Service, project projects.Project) (cairnline.Project, error) {
	if service == nil {
		return cairnline.Project{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Project(project)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Project{}, errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	if executionProfile, ok := ProjectExecutionProfile(project); ok {
		if err := upsertExecutionProfile(ctx, service, executionProfile); err != nil {
			return cairnline.Project{}, err
		}
	}
	staleExecutionProfileID := ""
	if item.DefaultExecutionProfileID == "" {
		staleExecutionProfileID = projectExecutionProfileIDValue(project)
	}
	var written cairnline.Project
	if _, err := service.GetProject(ctx, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Project{}, err
		}
		created, err := service.CreateProject(ctx, item)
		if err != nil {
			return cairnline.Project{}, err
		}
		written = created
	} else {
		updated, err := service.UpdateProject(ctx, item)
		if err != nil {
			return cairnline.Project{}, err
		}
		written = updated
	}
	if staleExecutionProfileID != "" {
		if err := service.DeleteExecutionProfile(ctx, staleExecutionProfileID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Project{}, err
		}
	}
	return written, nil
}

// DeleteProject removes the portable project record and the deterministic
// project-level execution profile generated from Hecate project defaults. Other
// project-scoped execution profiles, such as role defaults, are intentionally
// left for the later role/work write-adapter slice that owns those records.
func DeleteProject(ctx context.Context, service *cairnline.Service, project projects.Project) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID := strings.TrimSpace(project.ID)
	if projectID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("project id is required"))
	}
	if err := service.DeleteProject(ctx, projectID); err != nil {
		return err
	}
	executionProfileID := projectExecutionProfileIDValue(project)
	if executionProfileID == "" {
		return nil
	}
	if err := service.DeleteExecutionProfile(ctx, executionProfileID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
		return err
	}
	return nil
}

func upsertExecutionProfile(ctx context.Context, service *cairnline.Service, profile cairnline.ExecutionProfile) error {
	if _, err := service.UpdateExecutionProfile(ctx, profile); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
		_, err = service.CreateExecutionProfile(ctx, profile)
		return err
	}
	return nil
}
