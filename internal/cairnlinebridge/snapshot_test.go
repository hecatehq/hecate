package cairnlinebridge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hecatehq/cairnline"
)

func TestCairnlineSnapshotForProjectFiltersPortableGraph(t *testing.T) {
	t.Parallel()
	exportedAt := time.Date(2026, time.July, 13, 10, 30, 0, 0, time.UTC)
	snapshot := cairnline.Snapshot{
		Version:            cairnline.SnapshotVersion,
		ExportedAt:         exportedAt,
		Projects:           []cairnline.Project{{ID: "target"}, {ID: "other"}},
		ProjectSkills:      []cairnline.ProjectSkill{{ID: "skill_target", ProjectID: "target"}, {ID: "skill_other", ProjectID: "other"}},
		Roles:              []cairnline.Role{{ID: "role_target", ProjectID: "target"}, {ID: "role_other", ProjectID: "other"}},
		WorkItems:          []cairnline.WorkItem{{ID: "work_target", ProjectID: "target"}, {ID: "work_other", ProjectID: "other"}},
		Assignments:        []cairnline.Assignment{{ID: "assignment_target", ProjectID: "target"}, {ID: "assignment_other", ProjectID: "other"}},
		Artifacts:          []cairnline.Artifact{{ID: "artifact_target", ProjectID: "target"}, {ID: "artifact_other", ProjectID: "other"}},
		Evidence:           []cairnline.Evidence{{ID: "evidence_target", ProjectID: "target"}, {ID: "evidence_other", ProjectID: "other"}},
		Reviews:            []cairnline.Review{{ID: "review_target", ProjectID: "target"}, {ID: "review_other", ProjectID: "other"}},
		Handoffs:           []cairnline.Handoff{{ID: "handoff_target", ProjectID: "target"}, {ID: "handoff_other", ProjectID: "other"}},
		MemoryEntries:      []cairnline.MemoryEntry{{ID: "memory_target", ProjectID: "target"}, {ID: "memory_other", ProjectID: "other"}},
		MemoryCandidates:   []cairnline.MemoryCandidate{{ID: "candidate_target", ProjectID: "target"}, {ID: "candidate_other", ProjectID: "other"}},
		AssistantProposals: []cairnline.AssistantProposalRecord{{ID: "proposal_target", ProjectID: "target"}, {ID: "proposal_other", ProjectID: "other"}},
	}
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal source snapshot: %v", err)
	}

	got := CairnlineSnapshotForProject(snapshot, " target ")

	if got.Version != snapshot.Version || !got.ExportedAt.Equal(exportedAt) {
		t.Fatalf("snapshot header = version %d exported_at %s, want version %d exported_at %s", got.Version, got.ExportedAt, snapshot.Version, exportedAt)
	}
	checks := []struct {
		name      string
		length    int
		projectID func() string
	}{
		{name: "projects", length: len(got.Projects), projectID: func() string { return got.Projects[0].ID }},
		{name: "project skills", length: len(got.ProjectSkills), projectID: func() string { return got.ProjectSkills[0].ProjectID }},
		{name: "roles", length: len(got.Roles), projectID: func() string { return got.Roles[0].ProjectID }},
		{name: "work items", length: len(got.WorkItems), projectID: func() string { return got.WorkItems[0].ProjectID }},
		{name: "assignments", length: len(got.Assignments), projectID: func() string { return got.Assignments[0].ProjectID }},
		{name: "artifacts", length: len(got.Artifacts), projectID: func() string { return got.Artifacts[0].ProjectID }},
		{name: "evidence", length: len(got.Evidence), projectID: func() string { return got.Evidence[0].ProjectID }},
		{name: "reviews", length: len(got.Reviews), projectID: func() string { return got.Reviews[0].ProjectID }},
		{name: "handoffs", length: len(got.Handoffs), projectID: func() string { return got.Handoffs[0].ProjectID }},
		{name: "memory entries", length: len(got.MemoryEntries), projectID: func() string { return got.MemoryEntries[0].ProjectID }},
		{name: "memory candidates", length: len(got.MemoryCandidates), projectID: func() string { return got.MemoryCandidates[0].ProjectID }},
		{name: "assistant proposals", length: len(got.AssistantProposals), projectID: func() string { return got.AssistantProposals[0].ProjectID }},
	}
	for _, check := range checks {
		if check.length != 1 {
			t.Errorf("%s length = %d, want one target row", check.name, check.length)
			continue
		}
		if projectID := check.projectID(); projectID != "target" {
			t.Errorf("%s project_id = %q, want target", check.name, projectID)
		}
	}
	after, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal source snapshot after filter: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("CairnlineSnapshotForProject mutated its input")
	}
}
