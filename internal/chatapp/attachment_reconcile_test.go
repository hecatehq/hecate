package chatapp

import (
	"context"
	"errors"
	"testing"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/chatattachments"
)

func TestReconcileChatAttachmentsResolvesTranscriptOutcomes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()

	linkedSession := mustCreateReconcileSession(t, ctx, sessions, "chat_linked")
	releasedSession := mustCreateReconcileSession(t, ctx, sessions, "chat_released")
	linked := mustCreateReconcileAttachment(t, ctx, attachments, linkedSession.ID, "att_linked")
	released := mustCreateReconcileAttachment(t, ctx, attachments, releasedSession.ID, "att_released")
	linkedRef := chatattachments.ClaimRef{SessionID: linkedSession.ID, MessageID: "msg_linked", AttachmentIDs: []string{linked.ID}}
	releasedRef := chatattachments.ClaimRef{SessionID: releasedSession.ID, MessageID: "msg_absent", AttachmentIDs: []string{released.ID}}
	if _, err := attachments.Claim(ctx, linkedRef); err != nil {
		t.Fatalf("Claim(linked): %v", err)
	}
	if _, err := attachments.Claim(ctx, releasedRef); err != nil {
		t.Fatalf("Claim(released): %v", err)
	}
	if _, err := sessions.AppendMessage(ctx, linkedSession.ID, chat.Message{
		ID:          linkedRef.MessageID,
		Role:        "user",
		Attachments: []chat.MessageAttachment{reconcileMessageAttachment(linked)},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	stats, err := ReconcileChatAttachments(ctx, sessions, attachments)
	if err != nil {
		t.Fatalf("ReconcileChatAttachments: %v", err)
	}
	if stats.LinkedClaims != 1 || stats.ReleasedClaims != 1 || stats.DeletedSessions != 0 || stats.ConflictedClaims != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if err := attachments.DeleteDraft(ctx, linkedSession.ID, linked.ID); !errors.Is(err, chatattachments.ErrNotDraft) {
		t.Fatalf("DeleteDraft(linked) error = %v, want ErrNotDraft", err)
	}
	if err := attachments.DeleteDraft(ctx, releasedSession.ID, released.ID); err != nil {
		t.Fatalf("DeleteDraft(released): %v", err)
	}
	pending, err := attachments.ListPendingClaims(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}
}

func TestReconcileChatAttachmentsKeepsConflictingClaimFenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	session := mustCreateReconcileSession(t, ctx, sessions, "chat_conflict")
	attachment := mustCreateReconcileAttachment(t, ctx, attachments, session.ID, "att_conflict")
	ref := chatattachments.ClaimRef{SessionID: session.ID, MessageID: "msg_conflict", AttachmentIDs: []string{attachment.ID}}
	if _, err := attachments.Claim(ctx, ref); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := sessions.AppendMessage(ctx, session.ID, chat.Message{
		ID:   ref.MessageID,
		Role: "user",
		Attachments: []chat.MessageAttachment{{
			ID:        attachment.ID,
			Filename:  "tampered.png",
			MediaType: attachment.MediaType,
			SizeBytes: attachment.SizeBytes,
			SHA256:    attachment.SHA256,
			CreatedAt: attachment.CreatedAt,
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	stats, err := ReconcileChatAttachments(ctx, sessions, attachments)
	if err != nil {
		t.Fatalf("ReconcileChatAttachments: %v", err)
	}
	if stats.ConflictedClaims != 1 || stats.LinkedClaims != 0 || stats.ReleasedClaims != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if err := attachments.DeleteDraft(ctx, session.ID, attachment.ID); !errors.Is(err, chatattachments.ErrNotDraft) {
		t.Fatalf("DeleteDraft(conflicted) error = %v, want ErrNotDraft", err)
	}
	pending, err := attachments.ListPendingClaims(ctx)
	if err != nil || len(pending) != 1 || pending[0].Ref.MessageID != ref.MessageID {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}
}

func TestReconcileChatAttachmentsSweepsEveryOrphanLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	const sessionID = "chat_deleted_before_attachment_cleanup"

	draft := mustCreateReconcileAttachment(t, ctx, attachments, sessionID, "att_draft")
	linked := mustCreateReconcileAttachment(t, ctx, attachments, sessionID, "att_linked")
	claimed := mustCreateReconcileAttachment(t, ctx, attachments, sessionID, "att_claimed")
	linkedRef := chatattachments.ClaimRef{SessionID: sessionID, MessageID: "msg_linked", AttachmentIDs: []string{linked.ID}}
	if _, err := attachments.Claim(ctx, linkedRef); err != nil {
		t.Fatalf("Claim(linked): %v", err)
	}
	if err := attachments.ResolveClaim(ctx, linkedRef, chatattachments.ClaimLinked); err != nil {
		t.Fatalf("ResolveClaim(linked): %v", err)
	}
	if _, err := attachments.Claim(ctx, chatattachments.ClaimRef{
		SessionID: sessionID, MessageID: "msg_claimed", AttachmentIDs: []string{claimed.ID},
	}); err != nil {
		t.Fatalf("Claim(claimed): %v", err)
	}

	stats, err := ReconcileChatAttachments(ctx, sessions, attachments)
	if err != nil {
		t.Fatalf("ReconcileChatAttachments: %v", err)
	}
	if stats.DeletedSessions != 1 {
		t.Fatalf("stats = %+v, want one deleted attachment session", stats)
	}
	for _, id := range []string{draft.ID, linked.ID, claimed.ID} {
		if _, ok, err := attachments.Get(ctx, sessionID, id); err != nil || ok {
			t.Fatalf("Get(%s) after sweep = ok %v, err %v", id, ok, err)
		}
	}
}

func TestReconcileChatAttachmentsKeepsBodiesForLiveSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	session := mustCreateReconcileSession(t, ctx, sessions, "chat_live")
	attachment := mustCreateReconcileAttachment(t, ctx, attachments, session.ID, "att_live")

	stats, err := ReconcileChatAttachments(ctx, sessions, attachments)
	if err != nil {
		t.Fatalf("ReconcileChatAttachments: %v", err)
	}
	if stats != (AttachmentReconcileStats{}) {
		t.Fatalf("stats = %+v, want no changes", stats)
	}
	if _, ok, err := attachments.Get(ctx, session.ID, attachment.ID); err != nil || !ok {
		t.Fatalf("Get(live) = ok %v, err %v", ok, err)
	}
}

func TestApplicationSweepOrphanedAttachmentsDoesNotResolveLivePendingClaims(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessions := chat.NewMemoryStore()
	attachments := chatattachments.NewMemoryStore()
	liveSession := mustCreateReconcileSession(t, ctx, sessions, "chat_live_sweep")
	live := mustCreateReconcileAttachment(t, ctx, attachments, liveSession.ID, "att_live_sweep")
	orphan := mustCreateReconcileAttachment(t, ctx, attachments, "chat_orphan_sweep", "att_orphan_sweep")
	liveRef := chatattachments.ClaimRef{SessionID: liveSession.ID, MessageID: "msg_live_sweep", AttachmentIDs: []string{live.ID}}
	orphanRef := chatattachments.ClaimRef{SessionID: orphan.SessionID, MessageID: "msg_orphan_sweep", AttachmentIDs: []string{orphan.ID}}
	if _, err := attachments.Claim(ctx, liveRef); err != nil {
		t.Fatalf("Claim(live): %v", err)
	}
	if _, err := attachments.Claim(ctx, orphanRef); err != nil {
		t.Fatalf("Claim(orphan): %v", err)
	}

	app := New(Options{Store: sessions, Attachments: attachments})
	if err := app.SweepOrphanedAttachments(ctx); err != nil {
		t.Fatalf("SweepOrphanedAttachments: %v", err)
	}
	if _, ok, err := attachments.Get(ctx, liveSession.ID, live.ID); err != nil || !ok {
		t.Fatalf("Get(live) after sweep = ok %v, err %v", ok, err)
	}
	if _, ok, err := attachments.Get(ctx, orphan.SessionID, orphan.ID); err != nil || ok {
		t.Fatalf("Get(orphan) after sweep = ok %v, err %v", ok, err)
	}
	pending, err := attachments.ListPendingClaims(ctx)
	if err != nil {
		t.Fatalf("ListPendingClaims: %v", err)
	}
	if len(pending) != 1 || pending[0].Ref.SessionID != liveSession.ID || pending[0].Ref.MessageID != liveRef.MessageID {
		t.Fatalf("pending claims after sweep = %#v, want live claim unchanged", pending)
	}
}

type cancelAwareAttachmentStore struct {
	chatattachments.Store
}

func (s cancelAwareAttachmentStore) DeleteBySessionID(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.DeleteBySessionID(ctx, sessionID)
}

type cancelAfterDeleteSessionStore struct {
	SessionStore
	cancel context.CancelFunc
}

func (s cancelAfterDeleteSessionStore) Delete(ctx context.Context, sessionID string) error {
	if err := s.SessionStore.Delete(ctx, sessionID); err != nil {
		return err
	}
	s.cancel()
	return nil
}

func TestApplicationDeleteSessionFinishesAttachmentCleanupAfterRequestCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	baseSessions := chat.NewMemoryStore()
	sessions := cancelAfterDeleteSessionStore{SessionStore: baseSessions, cancel: cancel}
	baseAttachments := chatattachments.NewMemoryStore()
	attachments := cancelAwareAttachmentStore{Store: baseAttachments}
	app := New(Options{Store: sessions, Attachments: attachments})
	session := mustCreateReconcileSession(t, context.Background(), baseSessions, "chat_cancelled_delete")
	attachment := mustCreateReconcileAttachment(t, context.Background(), baseAttachments, session.ID, "att_delete")

	if err := app.DeleteSession(ctx, DeleteSessionCommand{SessionID: session.ID}); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("delete fixture did not cancel request context after transcript commit")
	}
	if _, ok, err := baseAttachments.Get(context.Background(), session.ID, attachment.ID); err != nil || ok {
		t.Fatalf("Get after cancelled delete = ok %v, err %v", ok, err)
	}
}

func mustCreateReconcileSession(t *testing.T, ctx context.Context, store *chat.MemoryStore, id string) chat.Session {
	t.Helper()
	session, err := store.Create(ctx, chat.Session{ID: id, AgentID: chat.DefaultAgentID})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	return session
}

func mustCreateReconcileAttachment(t *testing.T, ctx context.Context, store chatattachments.Store, sessionID, id string) chatattachments.Attachment {
	t.Helper()
	body := []byte("image-" + id)
	// Let the store stamp CreatedAt. Fixed fixture timestamps eventually cross
	// DraftTTL, after which a later Create legitimately sweeps an earlier draft.
	created, err := store.Create(ctx, chatattachments.StoredAttachment{
		Attachment: chatattachments.Attachment{
			ID:        id,
			SessionID: sessionID,
			Filename:  id + ".png",
			MediaType: "image/png",
			SizeBytes: int64(len(body)),
			SHA256:    "sha-" + id,
		},
		Data: body,
	})
	if err != nil {
		t.Fatalf("Create attachment: %v", err)
	}
	return created.Attachment
}

func reconcileMessageAttachment(attachment chatattachments.Attachment) chat.MessageAttachment {
	return chat.MessageAttachment{
		ID:        attachment.ID,
		Filename:  attachment.Filename,
		MediaType: attachment.MediaType,
		SizeBytes: attachment.SizeBytes,
		SHA256:    attachment.SHA256,
		CreatedAt: attachment.CreatedAt,
	}
}
