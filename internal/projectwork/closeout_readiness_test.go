package projectwork

import "testing"

func TestEvaluateWorkItemReadiness_ReturnsStructuredFollowThroughTargets(t *testing.T) {
	t.Parallel()

	readiness := EvaluateWorkItemReadiness(
		WorkItem{ID: "work_1", ProjectID: "proj_1", Status: WorkItemStatusReady},
		[]Assignment{
			{ID: "assignment_with_evidence", Status: AssignmentStatusCompleted},
			{ID: "assignment_missing_evidence", Status: AssignmentStatusCompleted},
		},
		[]CollaborationArtifact{
			{ID: "evidence_1", AssignmentID: "assignment_with_evidence", Kind: ArtifactKindEvidenceLink, EvidenceURL: "https://example.invalid/evidence"},
			{ID: "review_1", AssignmentID: "assignment_with_evidence", Kind: ArtifactKindReview, Title: "Release review", ReviewedAssignmentID: "assignment_with_evidence", ReviewVerdict: ReviewVerdictChangesRequested, ReviewRisk: ReviewRiskMedium, ReviewFollowUpRequired: true},
		},
		[]Handoff{
			{ID: "handoff_pending", Status: HandoffStatusPending, LinkedArtifactIDs: []string{"decoy_review"}},
			{ID: "handoff_accepted", Status: HandoffStatusAccepted, LinkedArtifactIDs: []string{"decoy_review"}},
		},
	)

	if len(readiness.OpenHandoffIDs) != 1 || readiness.OpenHandoffIDs[0] != "handoff_pending" {
		t.Fatalf("OpenHandoffIDs = %+v, want [handoff_pending]", readiness.OpenHandoffIDs)
	}
	if readiness.Ready || readiness.Status != "blocked" {
		t.Fatalf("readiness = %+v, want blocked", readiness)
	}
	if len(readiness.MissingEvidenceAssignmentIDs) != 1 || readiness.MissingEvidenceAssignmentIDs[0] != "assignment_missing_evidence" {
		t.Fatalf("MissingEvidenceAssignmentIDs = %+v, want [assignment_missing_evidence]", readiness.MissingEvidenceAssignmentIDs)
	}
	if len(readiness.ReviewFollowUps) != 1 || readiness.ReviewFollowUps[0].ArtifactID != "review_1" || readiness.ReviewFollowUps[0].ReviewedAssignmentID != "assignment_with_evidence" || readiness.ReviewFollowUps[0].ReviewVerdict != ReviewVerdictChangesRequested {
		t.Fatalf("ReviewFollowUps = %+v, want typed review_1 follow-up", readiness.ReviewFollowUps)
	}
}
