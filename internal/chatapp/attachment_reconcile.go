package chatapp

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
)

type AttachmentReconcileStats struct {
	LinkedClaims     int
	ReleasedClaims   int
	DeletedSessions  int
	ConflictedClaims int
}

type AttachmentClaimReconcileResult struct {
	Resolution     chatattachments.ClaimResolution
	DeletedSession bool
	Conflict       bool
}

// ReconcileChatAttachments repairs claim outcomes left ambiguous by a process
// exit or database commit error and removes every attachment session whose
// transcript owner is gone. It must run before the API accepts requests:
// absence of a transcript row is authoritative only while no live writer can
// still append it.
func ReconcileChatAttachments(
	ctx context.Context,
	sessions SessionStore,
	attachments AttachmentStore,
) (AttachmentReconcileStats, error) {
	var stats AttachmentReconcileStats
	if sessions == nil || attachments == nil {
		return stats, nil
	}
	claims, err := attachments.ListPendingClaims(ctx)
	if err != nil {
		return stats, err
	}
	deletedSessions := make(map[string]struct{})
	for _, claim := range claims {
		if _, deleted := deletedSessions[claim.Ref.SessionID]; deleted {
			continue
		}
		result, err := ReconcileAttachmentClaim(ctx, sessions, attachments, claim)
		if err != nil {
			return stats, err
		}
		if result.DeletedSession {
			deletedSessions[claim.Ref.SessionID] = struct{}{}
			stats.DeletedSessions++
			continue
		}
		if result.Conflict {
			stats.ConflictedClaims++
			continue
		}
		if result.Resolution == chatattachments.ClaimLinked {
			stats.LinkedClaims++
		} else {
			stats.ReleasedClaims++
		}
	}
	swept, err := SweepOrphanedChatAttachments(ctx, sessions, attachments)
	if err != nil {
		return stats, err
	}
	stats.DeletedSessions += swept
	return stats, nil
}

// SweepOrphanedChatAttachments removes complete attachment sessions only when
// their authoritative transcript row is absent. It intentionally does not
// inspect or resolve pending claims: live claim ownership belongs to the chat
// turn, while this boundary is safe to run during normal request handling.
func SweepOrphanedChatAttachments(
	ctx context.Context,
	sessions SessionStore,
	attachments AttachmentStore,
) (int, error) {
	if sessions == nil || attachments == nil {
		return 0, nil
	}
	attachmentSessions, err := attachments.ListSessionIDs(ctx)
	if err != nil {
		return 0, err
	}
	deletedSessions := 0
	for _, sessionID := range attachmentSessions {
		_, ok, err := sessions.Get(ctx, sessionID)
		if err != nil {
			return deletedSessions, fmt.Errorf("read chat attachment owner session: %w", err)
		}
		if ok {
			continue
		}
		if err := attachments.DeleteBySessionID(ctx, sessionID); err != nil {
			return deletedSessions, fmt.Errorf("delete orphaned chat attachment session: %w", err)
		}
		deletedSessions++
	}
	return deletedSessions, nil
}

// ReconcileAttachmentClaim resolves one completed append attempt against the
// authoritative transcript. The caller must hold that session's run ownership
// (or call this during startup) so an absent message cannot still be appended
// concurrently after the release decision.
func ReconcileAttachmentClaim(
	ctx context.Context,
	sessions SessionStore,
	attachments AttachmentStore,
	claim chatattachments.PendingClaim,
) (AttachmentClaimReconcileResult, error) {
	var result AttachmentClaimReconcileResult
	session, ok, err := sessions.Get(ctx, claim.Ref.SessionID)
	if err != nil {
		return result, fmt.Errorf("read attachment claim session: %w", err)
	}
	if !ok {
		if err := attachments.DeleteBySessionID(ctx, claim.Ref.SessionID); err != nil {
			return result, fmt.Errorf("delete orphaned attachment claim session: %w", err)
		}
		result.DeletedSession = true
		return result, nil
	}
	resolution, conflict := attachmentClaimResolution(session, claim)
	if conflict {
		result.Conflict = true
		return result, nil
	}
	if err := attachments.ResolveClaim(ctx, claim.Ref, resolution); err != nil {
		if errors.Is(err, chatattachments.ErrClaimLost) {
			result.Conflict = true
			return result, nil
		}
		return result, fmt.Errorf("resolve pending attachment claim: %w", err)
	}
	result.Resolution = resolution
	return result, nil
}

func attachmentClaimResolution(session chat.Session, claim chatattachments.PendingClaim) (chatattachments.ClaimResolution, bool) {
	for _, message := range session.Messages {
		if message.ID != claim.Ref.MessageID {
			continue
		}
		if message.Role != "user" || !attachmentMetadataMatches(message.Attachments, claim.Attachments) {
			return "", true
		}
		return chatattachments.ClaimLinked, false
	}

	claimedIDs := make(map[string]struct{}, len(claim.Ref.AttachmentIDs))
	for _, id := range claim.Ref.AttachmentIDs {
		claimedIDs[id] = struct{}{}
	}
	for _, message := range session.Messages {
		for _, attachment := range message.Attachments {
			if _, referenced := claimedIDs[attachment.ID]; referenced {
				return "", true
			}
		}
	}
	return chatattachments.ClaimReleased, false
}

func attachmentMetadataMatches(message []chat.MessageAttachment, claimed []chatattachments.Attachment) bool {
	if len(message) != len(claimed) {
		return false
	}
	messageByID := make(map[string]chat.MessageAttachment, len(message))
	for _, attachment := range message {
		messageByID[attachment.ID] = attachment
	}
	claimedIDs := make([]string, 0, len(claimed))
	for _, attachment := range claimed {
		claimedIDs = append(claimedIDs, attachment.ID)
		stored, ok := messageByID[attachment.ID]
		if !ok || stored.Filename != attachment.Filename || stored.MediaType != attachment.MediaType ||
			stored.SizeBytes != attachment.SizeBytes || stored.SHA256 != attachment.SHA256 ||
			!stored.CreatedAt.Equal(attachment.CreatedAt) {
			return false
		}
	}
	sort.Strings(claimedIDs)
	for i := 1; i < len(claimedIDs); i++ {
		if claimedIDs[i] == claimedIDs[i-1] {
			return false
		}
	}
	return true
}
