package projectassistant

import (
	"errors"
	"slices"
	"testing"
)

func TestProposalProjectIDsCollectsEveryDistinctExplicitScope(t *testing.T) {
	t.Parallel()
	proposal := Proposal{Actions: []Action{
		{
			Kind:   ActionCreateWorkItem,
			Target: map[string]string{"project_id": "project_a"},
			Patch:  []byte(`{"project_id":"project_a","title":"A"}`),
		},
		{
			Kind:  ActionCreateMemoryCandidate,
			Patch: []byte(`{"project_id":"project_b","title":"B","body":"body"}`),
		},
		{
			Kind:  ActionCreateProject,
			Patch: []byte(`{"id":"project_c","name":"C"}`),
		},
	}}

	if got, want := ProposalProjectIDs(proposal), []string{"project_a", "project_b", "project_c"}; !slices.Equal(got, want) {
		t.Fatalf("ProposalProjectIDs() = %v, want %v", got, want)
	}
}

func TestProposalRecordProjectIDDerivesAndValidatesDurableScope(t *testing.T) {
	t.Parallel()
	proposal := Proposal{Actions: []Action{{
		Kind:   ActionCreateWorkItem,
		Target: map[string]string{"project_id": "project_a"},
		Patch:  []byte(`{"project_id":"project_a","title":"A"}`),
	}}}
	if projectID, err := ProposalRecordProjectID(proposal, ""); err != nil || projectID != "project_a" {
		t.Fatalf("ProposalRecordProjectID(derived) = %q, %v, want project_a", projectID, err)
	}
	if _, err := ProposalRecordProjectID(proposal, "project_b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ProposalRecordProjectID(mismatch) error = %v, want ErrInvalid", err)
	}

	proposal.Actions[0].Patch = []byte(`{"project_id":"project_b","title":"B"}`)
	if err := ValidateProposalActions(proposal); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateProposalActions(conflicting scopes) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryProposalStoreRejectsConflictingDurableProjectScope(t *testing.T) {
	t.Parallel()
	store := NewMemoryProposalStore()
	record := ProposalRecord{
		ID:        "proposal_scope_store",
		ProjectID: "project_b",
		Proposal: Proposal{
			ID: "proposal_scope_store",
			Actions: []Action{{
				Kind:   ActionCreateWorkItem,
				Target: map[string]string{"project_id": "project_a"},
				Patch:  []byte(`{"project_id":"project_a","title":"A"}`),
			}},
		},
	}
	if _, err := store.UpsertProposal(t.Context(), record); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpsertProposal(conflicting project scope) error = %v, want ErrInvalid", err)
	}
	if _, ok, err := store.GetProposal(t.Context(), record.ID); err != nil || ok {
		t.Fatalf("GetProposal() after rejected upsert ok=%t err=%v, want absent", ok, err)
	}

	record.ProjectID = ""
	written, err := store.UpsertProposal(t.Context(), record)
	if err != nil {
		t.Fatalf("UpsertProposal(derived project scope): %v", err)
	}
	if written.ProjectID != "project_a" {
		t.Fatalf("derived project_id = %q, want project_a", written.ProjectID)
	}
}
