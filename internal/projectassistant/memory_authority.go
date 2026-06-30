package projectassistant

import (
	"context"

	"github.com/hecatehq/hecate/internal/memory"
)

type MemoryCandidateAuthority interface {
	CreateMemoryCandidate(ctx context.Context, projectID string, cmd MemoryCandidateCommand) (memory.Candidate, error)
}

type MemoryCandidateCommand struct {
	ID                  string
	Title               string
	Body                string
	SuggestedKind       string
	SuggestedTrustLabel string
	SuggestedSourceKind string
	SuggestedSourceID   string
	SourceRefs          []memory.CandidateSourceRef
}

func memoryCandidateAuthorityForStores(stores Stores) MemoryCandidateAuthority {
	if stores.MemoryCandidateAuthority != nil {
		return stores.MemoryCandidateAuthority
	}
	if stores.MemoryCandidates == nil {
		return nil
	}
	return storeMemoryCandidateAuthority{store: stores.MemoryCandidates}
}

type storeMemoryCandidateAuthority struct {
	store memory.CandidateStore
}

func (authority storeMemoryCandidateAuthority) CreateMemoryCandidate(ctx context.Context, projectID string, cmd MemoryCandidateCommand) (memory.Candidate, error) {
	return authority.store.CreateCandidate(ctx, memory.Candidate{
		ID:                  cmd.ID,
		ProjectID:           projectID,
		Title:               cmd.Title,
		Body:                cmd.Body,
		SuggestedKind:       cmd.SuggestedKind,
		SuggestedTrustLabel: cmd.SuggestedTrustLabel,
		SuggestedSourceKind: cmd.SuggestedSourceKind,
		SuggestedSourceID:   cmd.SuggestedSourceID,
		SourceRefs:          append([]memory.CandidateSourceRef(nil), cmd.SourceRefs...),
		Status:              memory.CandidateStatusPending,
	})
}
