package projectwork

import "testing"

func TestReviewArtifactFollowUpPathSemantics(t *testing.T) {
	t.Parallel()
	artifact := CollaborationArtifact{
		ID:                     "artifact_review",
		Kind:                   ArtifactKindReview,
		ReviewVerdict:          ReviewVerdictChangesRequested,
		ReviewFollowUpRequired: true,
	}

	if !ReviewArtifactRequiresFollowUp(artifact) {
		t.Fatal("changes-requested review artifact should require follow-up")
	}
	if !ReviewArtifactNeedsFollowUpPath(artifact, nil) {
		t.Fatal("review artifact without linked handoff should need a follow-up path")
	}

	acceptedWithoutTarget := []Handoff{{
		ID:                "handoff_accepted",
		Status:            HandoffStatusAccepted,
		LinkedArtifactIDs: []string{artifact.ID},
	}}
	if ReviewArtifactHasLinkedFollowUpPath(artifact.ID, acceptedWithoutTarget) {
		t.Fatal("accepted handoff without target assignment should not hide review follow-up")
	}

	cases := []struct {
		name    string
		handoff Handoff
	}{
		{
			name: "pending handoff",
			handoff: Handoff{
				ID:                "handoff_pending",
				Status:            HandoffStatusPending,
				LinkedArtifactIDs: []string{artifact.ID},
			},
		},
		{
			name: "dismissed handoff",
			handoff: Handoff{
				ID:                "handoff_dismissed",
				Status:            HandoffStatusDismissed,
				LinkedArtifactIDs: []string{artifact.ID},
			},
		},
		{
			name: "superseded handoff",
			handoff: Handoff{
				ID:                "handoff_superseded",
				Status:            HandoffStatusSuperseded,
				LinkedArtifactIDs: []string{artifact.ID},
			},
		},
		{
			name: "target assignment",
			handoff: Handoff{
				ID:                 "handoff_assignment",
				Status:             HandoffStatusAccepted,
				TargetAssignmentID: "asgn_followup",
				LinkedArtifactIDs:  []string{artifact.ID},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !ReviewArtifactHasLinkedFollowUpPath(artifact.ID, []Handoff{tc.handoff}) {
				t.Fatalf("%s should count as a linked follow-up path", tc.name)
			}
			if ReviewArtifactNeedsFollowUpPath(artifact, []Handoff{tc.handoff}) {
				t.Fatalf("%s should clear missing-path readiness", tc.name)
			}
		})
	}
}
