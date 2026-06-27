package cairnlinebridge

import (
	"context"
	"errors"

	"github.com/hecatehq/cairnline"
	"github.com/hecatehq/hecate/internal/memory"
)

func UpsertMemoryEntry(ctx context.Context, service *cairnline.Service, entry memory.Entry) (cairnline.MemoryEntry, error) {
	if service == nil {
		return cairnline.MemoryEntry{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := MemoryEntry(entry)
	if _, err := service.GetMemoryEntry(ctx, item.ProjectID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.MemoryEntry{}, err
		}
		created, err := service.CreateMemoryEntry(ctx, item)
		if err != nil {
			return cairnline.MemoryEntry{}, err
		}
		if created.Enabled == item.Enabled {
			return created, nil
		}
		return service.UpdateMemoryEntry(ctx, item)
	}
	return service.UpdateMemoryEntry(ctx, item)
}

func DeleteMemoryEntry(ctx context.Context, service *cairnline.Service, projectID, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	return service.DeleteMemoryEntry(ctx, projectID, id)
}

func UpsertMemoryCandidate(ctx context.Context, service *cairnline.Service, candidate memory.Candidate) (cairnline.MemoryCandidate, error) {
	if service == nil {
		return cairnline.MemoryCandidate{}, errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	item := MemoryCandidate(candidate)
	if _, err := service.GetMemoryCandidate(ctx, item.ProjectID, item.ID); err != nil {
		if !errors.Is(err, cairnline.ErrNotFound) {
			return cairnline.MemoryCandidate{}, err
		}
		created, err := service.CreateMemoryCandidate(ctx, item)
		if err != nil {
			return cairnline.MemoryCandidate{}, err
		}
		if memoryCandidateStateMatches(created, item) {
			return created, nil
		}
		return service.UpdateMemoryCandidate(ctx, item)
	}
	return service.UpdateMemoryCandidate(ctx, item)
}

func DeleteMemoryCandidate(ctx context.Context, service *cairnline.Service, projectID, id string) error {
	if service == nil {
		return errors.Join(ErrSourceNotConfigured, errors.New("cairnline service is required"))
	}
	return service.DeleteMemoryCandidate(ctx, projectID, id)
}

func memoryCandidateStateMatches(left, right cairnline.MemoryCandidate) bool {
	return left.Status == right.Status &&
		left.StatusReason == right.StatusReason &&
		left.PromotedMemoryID == right.PromotedMemoryID
}
