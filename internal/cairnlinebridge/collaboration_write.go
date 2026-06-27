package cairnlinebridge

import (
	"context"
	"errors"
	"strings"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/projectwork"
)

// RecordArtifact creates a generic Cairnline artifact when it is missing. The
// Hecate and Cairnline public contracts are both create/list for artifacts, so
// this helper intentionally returns the existing row instead of pretending
// artifact metadata can be updated.
func RecordArtifact(ctx context.Context, service *cairnline.Service, artifact projectwork.CollaborationArtifact) (cairnline.Artifact, error) {
	if service == nil {
		return cairnline.Artifact{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item, ok := Artifact(artifact)
	if !ok {
		return cairnline.Artifact{}, errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not a generic artifact"))
	}
	if _, err := service.GetArtifact(ctx, item.ProjectID, item.WorkItemID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Artifact{}, err
		}
		return service.CreateArtifact(ctx, item)
	}
	return service.GetArtifact(ctx, item.ProjectID, item.WorkItemID, item.ID)
}

func RecordEvidence(ctx context.Context, service *cairnline.Service, artifact projectwork.CollaborationArtifact) (cairnline.Evidence, error) {
	if service == nil {
		return cairnline.Evidence{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item, ok := Evidence(artifact)
	if !ok {
		return cairnline.Evidence{}, errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not evidence"))
	}
	if _, err := service.GetEvidence(ctx, item.ProjectID, item.WorkItemID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Evidence{}, err
		}
		return service.CreateEvidence(ctx, item)
	}
	return service.GetEvidence(ctx, item.ProjectID, item.WorkItemID, item.ID)
}

func RecordReview(ctx context.Context, service *cairnline.Service, artifact projectwork.CollaborationArtifact) (cairnline.Review, error) {
	if service == nil {
		return cairnline.Review{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item, ok := Review(artifact)
	if !ok {
		return cairnline.Review{}, errors.Join(cairnline.ErrInvalid, errors.New("artifact kind is not review"))
	}
	if _, err := service.GetReview(ctx, item.ProjectID, item.WorkItemID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Review{}, err
		}
		return service.CreateReview(ctx, item)
	}
	return service.GetReview(ctx, item.ProjectID, item.WorkItemID, item.ID)
}

func UpsertHandoff(ctx context.Context, service *cairnline.Service, handoff projectwork.Handoff) (cairnline.Handoff, error) {
	if service == nil {
		return cairnline.Handoff{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := Handoff(handoff)
	if strings.TrimSpace(item.ID) == "" {
		return cairnline.Handoff{}, errors.Join(cairnline.ErrInvalid, errors.New("handoff id is required"))
	}
	if _, err := service.GetHandoff(ctx, item.ProjectID, item.WorkItemID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.Handoff{}, err
		}
		return service.CreateHandoff(ctx, item)
	}
	return service.UpdateHandoff(ctx, item)
}

func DeleteHandoff(ctx context.Context, service *cairnline.Service, projectID, workItemID, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	return service.DeleteHandoff(ctx, projectID, workItemID, id)
}
