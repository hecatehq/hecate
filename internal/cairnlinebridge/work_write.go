package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/agentprofiles"
	"github.com/hecatehq/hecate/internal/projectwork"
)

// UpsertRole mirrors a Hecate project role into Cairnline without making the
// Cairnline record authoritative for live Hecate routes.
func UpsertRole(ctx context.Context, service *cairnline.Service, role projectwork.AgentRoleProfile) (cairnline.Role, error) {
	if service == nil {
		return cairnline.Role{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Role(role)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Role{}, errors.Join(cairnline.ErrInvalid, errors.New("role id is required"))
	}
	if executionProfile, ok := RoleExecutionProfile(role); ok {
		if err := upsertExecutionProfile(ctx, service, executionProfile); err != nil {
			return cairnline.Role{}, err
		}
	}
	staleExecutionProfileID := ""
	if item.DefaultExecutionProfileID == "" {
		staleExecutionProfileID = roleExecutionProfileIDValue(role)
	}
	written, err := service.UpdateRole(ctx, item)
	if err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Role{}, err
		}
		written, err = service.CreateRole(ctx, item)
		if err != nil {
			return cairnline.Role{}, err
		}
	}
	if staleExecutionProfileID != "" {
		if err := service.DeleteExecutionProfile(ctx, staleExecutionProfileID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Role{}, err
		}
	}
	return written, nil
}

func DeleteRole(ctx context.Context, service *cairnline.Service, role projectwork.AgentRoleProfile) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	projectID := strings.TrimSpace(role.ProjectID)
	roleID := strings.TrimSpace(role.ID)
	if projectID == "" || roleID == "" {
		return errors.Join(cairnline.ErrInvalid, errors.New("role project_id and id are required"))
	}
	if err := service.DeleteRole(ctx, projectID, roleID); err != nil {
		return err
	}
	if executionProfileID := roleExecutionProfileIDValue(role); executionProfileID != "" {
		if err := service.DeleteExecutionProfile(ctx, executionProfileID); err != nil && !errors.Is(err, cairnline.ErrNotFound) {
			return err
		}
	}
	return nil
}

func UpsertWorkItem(ctx context.Context, service *cairnline.Service, item projectwork.WorkItem) (cairnline.WorkItem, error) {
	if service == nil {
		return cairnline.WorkItem{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	mapped := WorkItem(item)
	if strings.TrimSpace(mapped.ID) == "" {
		return cairnline.WorkItem{}, errors.Join(cairnline.ErrInvalid, errors.New("work item id is required"))
	}
	if _, err := service.GetWorkItem(ctx, mapped.ProjectID, mapped.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.WorkItem{}, err
		}
		return service.CreateWorkItem(ctx, mapped)
	}
	return service.UpdateWorkItem(ctx, mapped)
}

func DeleteWorkItem(ctx context.Context, service *cairnline.Service, projectID, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	return service.DeleteWorkItem(ctx, projectID, id)
}

// UpsertAssignment creates or updates a Cairnline assignment and then syncs
// Hecate's assignment lifecycle metadata. Existing rows are first updated with
// their current Cairnline status so metadata parity does not bypass claim
// ownership; claimed rows move back to queued through ReleaseAssignment when
// Hecate clears a pre-dispatch claim for retry.
func UpsertAssignment(ctx context.Context, service *cairnline.Service, assignment projectwork.Assignment, role projectwork.AgentRoleProfile, profile agentprofiles.Profile) (cairnline.Assignment, error) {
	if service == nil {
		return cairnline.Assignment{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Assignment(assignment, role, profile)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Assignment{}, errors.Join(cairnline.ErrInvalid, errors.New("assignment id is required"))
	}
	existing, err := service.GetAssignment(ctx, item.ProjectID, item.ID)
	if err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Assignment{}, err
		}
		if _, err := service.CreateAssignment(ctx, item); err != nil {
			return cairnline.Assignment{}, err
		}
	} else {
		metadata := item
		metadata.Status = existing.Status
		metadata.ClaimedBy = existing.ClaimedBy
		if _, err := service.UpdateAssignment(ctx, metadata); err != nil {
			return cairnline.Assignment{}, err
		}
	}
	if err := syncAssignmentStatus(ctx, service, existing, item); err != nil {
		return cairnline.Assignment{}, err
	}
	return service.GetAssignment(ctx, item.ProjectID, item.ID)
}

func DeleteAssignment(ctx context.Context, service *cairnline.Service, projectID, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	return service.DeleteAssignment(ctx, projectID, id)
}
