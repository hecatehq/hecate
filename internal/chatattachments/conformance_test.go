package chatattachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type StoreFactory func(t *testing.T) Store

func RunConformanceTests(t *testing.T, name string, factory StoreFactory) {
	t.Helper()
	t.Run(name+"/Lifecycle", func(t *testing.T) {
		t.Parallel()
		runStoreLifecycle(t, factory(t))
	})
	t.Run(name+"/SessionOwnership", func(t *testing.T) {
		t.Parallel()
		runStoreSessionOwnership(t, factory(t))
	})
	t.Run(name+"/ImmutableCopies", func(t *testing.T) {
		t.Parallel()
		runStoreImmutableCopies(t, factory(t))
	})
	t.Run(name+"/DeleteBySessionID", func(t *testing.T) {
		t.Parallel()
		runStoreDeleteBySessionID(t, factory(t))
	})
	t.Run(name+"/ListSessionIDs", func(t *testing.T) {
		t.Parallel()
		runStoreListSessionIDs(t, factory(t))
	})
	t.Run(name+"/ClaimLifecycle", func(t *testing.T) {
		t.Parallel()
		runStoreClaimLifecycle(t, factory(t))
	})
	t.Run(name+"/ClaimDeleteAtomic", func(t *testing.T) {
		t.Parallel()
		runStoreClaimDeleteAtomic(t, factory(t))
	})
	t.Run(name+"/ClaimFencing", func(t *testing.T) {
		t.Parallel()
		runStoreClaimFencing(t, factory(t))
	})
	t.Run(name+"/ConcurrentDraftQuota", func(t *testing.T) {
		t.Parallel()
		runStoreConcurrentDraftQuota(t, factory(t))
	})
	t.Run(name+"/StoredSessionQuota", func(t *testing.T) {
		t.Parallel()
		runStoreSessionQuota(t, factory(t))
	})
	t.Run(name+"/StoredTotalQuota", func(t *testing.T) {
		t.Parallel()
		runStoreTotalQuota(t, factory(t))
	})
	t.Run(name+"/ConcurrentStoredTotalQuota", func(t *testing.T) {
		t.Parallel()
		runStoreConcurrentTotalQuota(t, factory(t))
	})
	t.Run(name+"/StaleDraftReclamation", func(t *testing.T) {
		t.Parallel()
		runStoreStaleDraftReclamation(t, factory(t))
	})
	t.Run(name+"/RejectsSizeMetadataMismatch", func(t *testing.T) {
		t.Parallel()
		runStoreRejectsSizeMetadataMismatch(t, factory(t))
	})
}

func runStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	createdAt := time.Now().Add(-time.Hour).In(time.FixedZone("test", 2*60*60)).Truncate(time.Microsecond)
	input := StoredAttachment{
		Attachment: Attachment{
			ID:        "attachment_b",
			SessionID: "session_1",
			Filename:  "diagram.png",
			MediaType: "image/png",
			SizeBytes: 7,
			SHA256:    "sha-b",
			CreatedAt: createdAt,
		},
		Data: []byte("pngdata"),
	}
	created, err := store.Create(ctx, input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.CreatedAt.Location() != time.UTC || !created.CreatedAt.Equal(createdAt) {
		t.Fatalf("Create CreatedAt = %v, want UTC %v", created.CreatedAt, createdAt.UTC())
	}
	if !bytes.Equal(created.Data, input.Data) {
		t.Fatalf("Create Data = %q, want %q", created.Data, input.Data)
	}

	if _, err := store.Create(ctx, input); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate Create error = %v, want ErrAlreadyExists", err)
	}

	got, ok, err := store.Get(ctx, input.SessionID, input.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Attachment != created.Attachment || !bytes.Equal(got.Data, created.Data) {
		t.Fatalf("Get = %#v, want %#v", got, created)
	}

	earlier := StoredAttachment{
		Attachment: Attachment{
			ID:        "attachment_a",
			SessionID: input.SessionID,
			Filename:  "photo.jpg",
			MediaType: "image/jpeg",
			SizeBytes: 3,
			SHA256:    "sha-a",
			CreatedAt: createdAt.Add(-time.Minute),
		},
		Data: []byte("jpg"),
	}
	if _, err := store.Create(ctx, earlier); err != nil {
		t.Fatalf("Create earlier: %v", err)
	}
	items, err := store.List(ctx, input.SessionID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ID != earlier.ID || items[1].ID != input.ID {
		t.Fatalf("List = %#v, want attachments in creation order", items)
	}

	if err := store.DeleteDraft(ctx, input.SessionID, input.ID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}
	if err := store.DeleteDraft(ctx, input.SessionID, input.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteDraft missing error = %v, want ErrNotFound", err)
	}
	if _, ok, err := store.Get(ctx, input.SessionID, input.ID); err != nil || ok {
		t.Fatalf("Get after delete: ok=%v err=%v", ok, err)
	}
}

func runStoreSessionOwnership(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	for _, sessionID := range []string{"session_a", "session_b"} {
		if _, err := store.Create(ctx, StoredAttachment{
			Attachment: Attachment{
				ID:        "shared_id",
				SessionID: sessionID,
				Filename:  sessionID + ".png",
				MediaType: "image/png",
				SizeBytes: int64(len(sessionID)),
				SHA256:    "sha-" + sessionID,
			},
			Data: []byte(sessionID),
		}); err != nil {
			t.Fatalf("Create(%s): %v", sessionID, err)
		}
	}

	got, ok, err := store.Get(ctx, "session_a", "shared_id")
	if err != nil || !ok {
		t.Fatalf("Get session_a: ok=%v err=%v", ok, err)
	}
	if string(got.Data) != "session_a" {
		t.Fatalf("Get session_a Data = %q, want session_a", got.Data)
	}
	if err := store.DeleteDraft(ctx, "session_a", "shared_id"); err != nil {
		t.Fatalf("DeleteDraft session_a: %v", err)
	}
	got, ok, err = store.Get(ctx, "session_b", "shared_id")
	if err != nil || !ok || string(got.Data) != "session_b" {
		t.Fatalf("Get session_b after scoped delete = %#v ok=%v err=%v", got, ok, err)
	}
}

func runStoreImmutableCopies(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	input := StoredAttachment{
		Attachment: Attachment{
			ID:        "attachment_copy",
			SessionID: "session_copy",
			Filename:  "copy.webp",
			MediaType: "image/webp",
			SizeBytes: 4,
			SHA256:    "sha-copy",
		},
		Data: []byte("copy"),
	}
	created, err := store.Create(ctx, input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	input.Data[0] = 'X'
	created.Data[1] = 'Y'

	got, ok, err := store.Get(ctx, input.SessionID, input.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if string(got.Data) != "copy" {
		t.Fatalf("stored Data = %q, want immutable copy", got.Data)
	}
	got.Data[2] = 'Z'
	gotAgain, ok, err := store.Get(ctx, input.SessionID, input.ID)
	if err != nil || !ok {
		t.Fatalf("Get again: ok=%v err=%v", ok, err)
	}
	if string(gotAgain.Data) != "copy" {
		t.Fatalf("stored Data after returned mutation = %q, want immutable copy", gotAgain.Data)
	}
}

func runStoreDeleteBySessionID(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	for _, attachment := range []StoredAttachment{
		{
			Attachment: Attachment{ID: "one", SessionID: "session_delete", MediaType: "image/png", SizeBytes: 3},
			Data:       []byte("one"),
		},
		{
			Attachment: Attachment{ID: "two", SessionID: "session_delete", MediaType: "image/png", SizeBytes: 3},
			Data:       []byte("two"),
		},
		{
			Attachment: Attachment{ID: "keep", SessionID: "session_keep", MediaType: "image/png", SizeBytes: 4},
			Data:       []byte("keep"),
		},
	} {
		if _, err := store.Create(ctx, attachment); err != nil {
			t.Fatalf("Create(%s): %v", attachment.ID, err)
		}
	}
	if err := store.DeleteBySessionID(ctx, "session_delete"); err != nil {
		t.Fatalf("DeleteBySessionID: %v", err)
	}
	if err := store.DeleteBySessionID(ctx, "session_delete"); err != nil {
		t.Fatalf("DeleteBySessionID idempotent: %v", err)
	}
	items, err := store.List(ctx, "session_delete")
	if err != nil || len(items) != 0 {
		t.Fatalf("List deleted session = %#v err=%v", items, err)
	}
	if _, ok, err := store.Get(ctx, "session_keep", "keep"); err != nil || !ok {
		t.Fatalf("Get retained attachment: ok=%v err=%v", ok, err)
	}
}

func runStoreListSessionIDs(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	for _, sessionID := range []string{"session_b", "session_a", "session_b"} {
		id := fmt.Sprintf("attachment_%d", len(mustListAttachments(t, store, ctx, sessionID)))
		if _, err := store.Create(ctx, StoredAttachment{
			Attachment: Attachment{ID: id, SessionID: sessionID, SizeBytes: 1},
			Data:       []byte("x"),
		}); err != nil {
			t.Fatalf("Create(%s/%s): %v", sessionID, id, err)
		}
	}
	ids, err := store.ListSessionIDs(ctx)
	if err != nil {
		t.Fatalf("ListSessionIDs: %v", err)
	}
	if fmt.Sprint(ids) != "[session_a session_b]" {
		t.Fatalf("ListSessionIDs = %v, want sorted unique ids", ids)
	}
	if err := store.DeleteBySessionID(ctx, "session_a"); err != nil {
		t.Fatalf("DeleteBySessionID: %v", err)
	}
	ids, err = store.ListSessionIDs(ctx)
	if err != nil || fmt.Sprint(ids) != "[session_b]" {
		t.Fatalf("ListSessionIDs after delete = %v, err=%v", ids, err)
	}
}

func mustListAttachments(t *testing.T, store Store, ctx context.Context, sessionID string) []Attachment {
	t.Helper()
	items, err := store.List(ctx, sessionID)
	if err != nil {
		t.Fatalf("List(%s): %v", sessionID, err)
	}
	return items
}

func runStoreClaimLifecycle(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	create := func(id string) {
		t.Helper()
		if _, err := store.Create(ctx, StoredAttachment{
			Attachment: Attachment{ID: id, SessionID: "session_claim", SizeBytes: 1},
			Data:       []byte{id[0]},
		}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}

	create("release")
	releaseRef := ClaimRef{SessionID: "session_claim", MessageID: "message_release", AttachmentIDs: []string{"release"}}
	claimed, err := store.Claim(ctx, releaseRef)
	if err != nil || len(claimed) != 1 || claimed[0].ID != "release" {
		t.Fatalf("Claim(release) = %#v, err=%v", claimed, err)
	}
	if err := store.DeleteDraft(ctx, "session_claim", "release"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("DeleteDraft(claimed) error = %v, want ErrNotDraft", err)
	}
	pending, err := store.ListPendingClaims(ctx)
	if err != nil || len(pending) != 1 || pending[0].Ref.MessageID != releaseRef.MessageID || len(pending[0].Attachments) != 1 {
		t.Fatalf("ListPendingClaims() = %#v, err=%v", pending, err)
	}
	if err := store.ResolveClaim(ctx, releaseRef, ClaimReleased); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := store.ResolveClaim(ctx, releaseRef, ClaimReleased); err != nil {
		t.Fatalf("Release idempotent retry: %v", err)
	}
	if err := store.DeleteDraft(ctx, "session_claim", "release"); err != nil {
		t.Fatalf("DeleteDraft(released): %v", err)
	}

	create("linked")
	linkedRef := ClaimRef{SessionID: "session_claim", MessageID: "message_linked", AttachmentIDs: []string{"linked"}}
	if _, err := store.Claim(ctx, linkedRef); err != nil {
		t.Fatalf("Claim(linked): %v", err)
	}
	if err := store.ResolveClaim(ctx, linkedRef, ClaimLinked); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := store.ResolveClaim(ctx, linkedRef, ClaimLinked); err != nil {
		t.Fatalf("Link idempotent retry: %v", err)
	}
	if err := store.DeleteDraft(ctx, "session_claim", "linked"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("DeleteDraft(linked) error = %v, want ErrNotDraft", err)
	}
}

func runStoreClaimDeleteAtomic(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "race", SessionID: "session_race", SizeBytes: 1},
		Data:       []byte("x"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	start := make(chan struct{})
	claimResult := make(chan error, 1)
	deleteResult := make(chan error, 1)
	go func() {
		<-start
		_, err := store.Claim(ctx, ClaimRef{SessionID: "session_race", MessageID: "message_race", AttachmentIDs: []string{"race"}})
		claimResult <- err
	}()
	go func() {
		<-start
		deleteResult <- store.DeleteDraft(ctx, "session_race", "race")
	}()
	close(start)
	claimErr, deleteErr := <-claimResult, <-deleteResult
	if claimErr == nil && errors.Is(deleteErr, ErrNotDraft) {
		return
	}
	if deleteErr == nil && errors.Is(claimErr, ErrNotFound) {
		return
	}
	t.Fatalf("claim/delete outcome = claim %v, delete %v; want one atomic winner", claimErr, deleteErr)
}

func runStoreClaimFencing(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if _, err := store.Create(ctx, StoredAttachment{
			Attachment: Attachment{ID: id, SessionID: "session_fence", SizeBytes: 1},
			Data:       []byte(id),
		}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}
	first := ClaimRef{SessionID: "session_fence", MessageID: "message_a", AttachmentIDs: []string{"a", "b"}}
	if _, err := store.Claim(ctx, first); err != nil {
		t.Fatalf("Claim(first): %v", err)
	}
	if err := store.ResolveClaim(ctx, first, ClaimReleased); err != nil {
		t.Fatalf("ResolveClaim(first released): %v", err)
	}
	newer := ClaimRef{SessionID: "session_fence", MessageID: "message_b", AttachmentIDs: []string{"b"}}
	if _, err := store.Claim(ctx, newer); err != nil {
		t.Fatalf("Claim(newer): %v", err)
	}
	if err := store.ResolveClaim(ctx, first, ClaimReleased); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale ResolveClaim error = %v, want ErrClaimLost", err)
	}
	if err := store.DeleteDraft(ctx, "session_fence", "a"); err != nil {
		t.Fatalf("DeleteDraft(a) after atomic stale resolution: %v", err)
	}
	if err := store.DeleteDraft(ctx, "session_fence", "b"); !errors.Is(err, ErrNotDraft) {
		t.Fatalf("DeleteDraft(b) = %v, want newer claim preserved", err)
	}
}

func runStoreConcurrentDraftQuota(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	total := MaxDraftAttachmentsPerSession * 2
	start := make(chan struct{})
	results := make(chan error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := store.Create(ctx, StoredAttachment{
				Attachment: Attachment{ID: fmt.Sprintf("draft_%02d", i), SessionID: "session_quota", SizeBytes: 1},
				Data:       []byte{byte(i)},
			})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	var created, rejected int
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrDraftQuota):
			rejected++
		default:
			t.Fatalf("Create concurrent error = %v", err)
		}
	}
	if created != MaxDraftAttachmentsPerSession || rejected != total-MaxDraftAttachmentsPerSession {
		t.Fatalf("quota outcomes = %d created, %d rejected", created, rejected)
	}
}

type storedSessionLimitSetter interface {
	setMaxStoredBytesPerSession(int64)
}

type storedTotalLimitSetter interface {
	setMaxStoredBytesTotal(int64)
}

func runStoreSessionQuota(t *testing.T, store Store) {
	t.Helper()
	limiter, ok := store.(storedSessionLimitSetter)
	if !ok {
		t.Fatalf("store %T does not expose the package-private test limit", store)
	}
	limiter.setMaxStoredBytesPerSession(4)
	ctx := context.Background()
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "within", SessionID: "session_stored_quota", SizeBytes: 3},
		Data:       []byte("abc"),
	}); err != nil {
		t.Fatalf("Create(within): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "over", SessionID: "session_stored_quota", SizeBytes: 2},
		Data:       []byte("de"),
	}); !errors.Is(err, ErrSessionQuota) {
		t.Fatalf("Create(over) error = %v, want ErrSessionQuota", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "other", SessionID: "session_other_quota", SizeBytes: 4},
		Data:       []byte("wxyz"),
	}); err != nil {
		t.Fatalf("Create(other session): %v", err)
	}
}

func runStoreTotalQuota(t *testing.T, store Store) {
	t.Helper()
	limiter, ok := store.(storedTotalLimitSetter)
	if !ok {
		t.Fatalf("store %T does not expose the package-private total test limit", store)
	}
	limiter.setMaxStoredBytesTotal(4)
	ctx := context.Background()
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "within", SessionID: "session_total_a", SizeBytes: 3},
		Data:       []byte("abc"),
	}); err != nil {
		t.Fatalf("Create(within): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "over", SessionID: "session_total_b", SizeBytes: 2},
		Data:       []byte("de"),
	}); !errors.Is(err, ErrTotalQuota) {
		t.Fatalf("Create(over) error = %v, want ErrTotalQuota", err)
	}
	if err := store.DeleteBySessionID(ctx, "session_total_a"); err != nil {
		t.Fatalf("DeleteBySessionID: %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "after_delete", SessionID: "session_total_b", SizeBytes: 4},
		Data:       []byte("wxyz"),
	}); err != nil {
		t.Fatalf("Create(after delete): %v", err)
	}
}

func runStoreConcurrentTotalQuota(t *testing.T, store Store) {
	t.Helper()
	limiter, ok := store.(storedTotalLimitSetter)
	if !ok {
		t.Fatalf("store %T does not expose the package-private total test limit", store)
	}
	const limit = 8
	limiter.setMaxStoredBytesTotal(limit)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, limit*2)
	var wg sync.WaitGroup
	for i := 0; i < limit*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := store.Create(ctx, StoredAttachment{
				Attachment: Attachment{
					ID:        "attachment",
					SessionID: fmt.Sprintf("session_total_%02d", i),
					SizeBytes: 1,
				},
				Data: []byte{byte(i)},
			})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	var created, rejected int
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrTotalQuota):
			rejected++
		default:
			t.Fatalf("Create concurrent total quota error = %v", err)
		}
	}
	if created != limit || rejected != limit {
		t.Fatalf("total quota outcomes = %d created, %d rejected; want %d each", created, rejected, limit)
	}
}

func runStoreStaleDraftReclamation(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	old := time.Now().UTC().Add(-DraftTTL - time.Minute)
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "stale", SessionID: "session_stale", SizeBytes: 1, CreatedAt: old},
		Data:       []byte("s"),
	}); err != nil {
		t.Fatalf("Create(stale): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "fresh", SessionID: "session_stale", SizeBytes: 1},
		Data:       []byte("f"),
	}); err != nil {
		t.Fatalf("Create(fresh): %v", err)
	}
	if _, ok, err := store.Get(ctx, "session_stale", "stale"); err != nil || ok {
		t.Fatalf("Get(stale) = ok %v, err %v; want reclaimed", ok, err)
	}

	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "abandoned", SessionID: "session_abandoned", SizeBytes: 1, CreatedAt: old},
		Data:       []byte("a"),
	}); err != nil {
		t.Fatalf("Create(abandoned): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "global_trigger", SessionID: "session_trigger", SizeBytes: 1},
		Data:       []byte("t"),
	}); err != nil {
		t.Fatalf("Create(global trigger): %v", err)
	}
	if _, ok, err := store.Get(ctx, "session_abandoned", "abandoned"); err != nil || ok {
		t.Fatalf("Get(abandoned) = ok %v, err %v; want global stale reclaim", ok, err)
	}

	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "linked_old", SessionID: "session_stale", SizeBytes: 1, CreatedAt: old},
		Data:       []byte("l"),
	}); err != nil {
		t.Fatalf("Create(linked_old): %v", err)
	}
	linkedRef := ClaimRef{SessionID: "session_stale", MessageID: "message_linked_old", AttachmentIDs: []string{"linked_old"}}
	if _, err := store.Claim(ctx, linkedRef); err != nil {
		t.Fatalf("Claim(linked_old): %v", err)
	}
	if err := store.ResolveClaim(ctx, linkedRef, ClaimLinked); err != nil {
		t.Fatalf("Link(linked_old): %v", err)
	}
	if _, err := store.Create(ctx, StoredAttachment{
		Attachment: Attachment{ID: "trigger", SessionID: "session_stale", SizeBytes: 1},
		Data:       []byte("t"),
	}); err != nil {
		t.Fatalf("Create(trigger): %v", err)
	}
	if _, ok, err := store.Get(ctx, "session_stale", "linked_old"); err != nil || !ok {
		t.Fatalf("Get(linked_old) = ok %v, err %v; linked bodies must not expire", ok, err)
	}
}

func runStoreRejectsSizeMetadataMismatch(t *testing.T, store Store) {
	t.Helper()
	_, err := store.Create(context.Background(), StoredAttachment{
		Attachment: Attachment{ID: "mismatch", SessionID: "session_mismatch", SizeBytes: 0},
		Data:       []byte("not empty"),
	})
	if !errors.Is(err, ErrInvalidMetadata) {
		t.Fatalf("Create mismatch error = %v, want ErrInvalidMetadata", err)
	}
}
