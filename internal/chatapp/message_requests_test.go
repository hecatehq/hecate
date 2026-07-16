package chatapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
)

type racingMessageRequestLeaseStore struct {
	chat.Store
	renewed  chan struct{}
	firstErr error

	mu         sync.Mutex
	renewCalls int
}

type configuredMessageRequestTTLStore struct {
	chat.Store
	ttl time.Duration
}

func (store configuredMessageRequestTTLStore) MessageRequestLeaseTTL() time.Duration {
	return store.ttl
}

func (store *racingMessageRequestLeaseStore) MessageRequestLeaseTTL() time.Duration {
	return 30 * time.Millisecond
}

func (store *racingMessageRequestLeaseStore) RenewMessageRequest(ctx context.Context, req chat.RenewMessageRequestRequest) error {
	store.mu.Lock()
	store.renewCalls++
	first := store.renewCalls == 1
	store.mu.Unlock()
	if !first {
		return store.Store.RenewMessageRequest(ctx, req)
	}
	select {
	case store.renewed <- struct{}{}:
	default:
	}
	// Force stop to cancel the in-flight heartbeat before the store reports
	// its result, making the return-versus-stop race deterministic.
	<-ctx.Done()
	if store.firstErr != nil {
		return store.firstErr
	}
	return ctx.Err()
}

func TestMessageRequestReservationFailsClosedWhenHeartbeatLosesOwnership(t *testing.T) {
	base := chat.NewMemoryStore()
	if _, err := base.Create(t.Context(), chat.Session{ID: "chat_lease_lost", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	store := &racingMessageRequestLeaseStore{
		Store:    base,
		renewed:  make(chan struct{}, 1),
		firstErr: chat.ErrMessageRequestLeaseInvalid,
	}
	app := New(Options{Store: base, Messages: store})
	if app.messageRequestRenewEvery > store.MessageRequestLeaseTTL()/3 {
		t.Fatalf("renew interval = %s, want at most one third of %s", app.messageRequestRenewEvery, store.MessageRequestLeaseTTL())
	}
	fingerprint := chat.MessageRequestFingerprint{1}
	result, err := app.BeginMessageRequest(t.Context(), BeginMessageRequestCommand{
		SessionID:       "chat_lease_lost",
		ClientRequestID: "queued-lease-lost",
		Fingerprint:     fingerprint,
	})
	if err != nil {
		t.Fatalf("BeginMessageRequest: %v", err)
	}
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("message request heartbeat did not run")
	}

	_, err = app.AppendUserMessage(t.Context(), AppendUserMessageCommand{
		SessionID:   "chat_lease_lost",
		Reservation: result.Reservation,
		Message:     chat.Message{ID: "msg_must_not_commit", Role: "user", Content: "do not append"},
	})
	if !errors.Is(err, chat.ErrMessageRequestLeaseInvalid) {
		t.Fatalf("AppendUserMessage error = %v, want invalid lease", err)
	}
	if releaseErr := app.ReleaseMessageRequest(t.Context(), result.Reservation); !errors.Is(releaseErr, chat.ErrMessageRequestLeaseInvalid) {
		t.Fatalf("ReleaseMessageRequest error = %v, want recorded invalid lease", releaseErr)
	}
	persisted, ok, err := base.Get(t.Context(), "chat_lease_lost")
	if err != nil || !ok {
		t.Fatalf("Get: found=%v err=%v", ok, err)
	}
	if len(persisted.Messages) != 0 {
		t.Fatalf("messages = %+v, want no commit after lease loss", persisted.Messages)
	}
}

func TestMessageRequestReservationSuppressesHeartbeatCancellationOnRelease(t *testing.T) {
	base := chat.NewMemoryStore()
	if _, err := base.Create(t.Context(), chat.Session{ID: "chat_lease_release", AgentID: chat.DefaultAgentID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	store := &racingMessageRequestLeaseStore{Store: base, renewed: make(chan struct{}, 1)}
	app := New(Options{Store: base, Messages: store})
	result, err := app.BeginMessageRequest(t.Context(), BeginMessageRequestCommand{
		SessionID:       "chat_lease_release",
		ClientRequestID: "queued-release",
		Fingerprint:     chat.MessageRequestFingerprint{2},
	})
	if err != nil {
		t.Fatalf("BeginMessageRequest: %v", err)
	}
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("message request heartbeat did not run")
	}
	if err := app.ReleaseMessageRequest(t.Context(), result.Reservation); err != nil {
		t.Fatalf("ReleaseMessageRequest: %v", err)
	}
}

func TestApplicationNormalizesMessageRequestLeaseTTLBeforeSchedulingHeartbeat(t *testing.T) {
	base := chat.NewMemoryStore()
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "nonpositive uses store default", ttl: 0, want: chat.MessageRequestLeaseStaleAfter / 3},
		{name: "too small uses scheduler floor", ttl: time.Nanosecond, want: minimumMessageRequestLeaseTTL / 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := New(Options{
				Store:    base,
				Messages: configuredMessageRequestTTLStore{Store: base, ttl: test.ttl},
			})
			if app.messageRequestRenewEvery != test.want {
				t.Fatalf("renew interval = %s, want %s", app.messageRequestRenewEvery, test.want)
			}
		})
	}
}
