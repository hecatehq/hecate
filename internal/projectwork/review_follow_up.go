package projectwork

import "strings"

func ReviewArtifactRequiresFollowUp(artifact CollaborationArtifact) bool {
	if artifact.Kind != ArtifactKindReview {
		return false
	}
	return artifact.ReviewFollowUpRequired ||
		artifact.ReviewVerdict == ReviewVerdictBlocked ||
		artifact.ReviewVerdict == ReviewVerdictChangesRequested
}

func ReviewArtifactNeedsFollowUpPath(artifact CollaborationArtifact, handoffs []Handoff) bool {
	if !ReviewArtifactRequiresFollowUp(artifact) {
		return false
	}
	return !ReviewArtifactHasLinkedFollowUpPath(artifact.ID, handoffs)
}

func ReviewArtifactHasLinkedFollowUpPath(artifactID string, handoffs []Handoff) bool {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return false
	}
	for _, handoff := range handoffs {
		for _, linkedID := range handoff.LinkedArtifactIDs {
			if strings.TrimSpace(linkedID) != artifactID {
				continue
			}
			if handoff.Status == HandoffStatusPending ||
				handoff.Status == HandoffStatusDismissed ||
				handoff.Status == HandoffStatusSuperseded ||
				strings.TrimSpace(handoff.TargetAssignmentID) != "" {
				return true
			}
		}
	}
	return false
}
