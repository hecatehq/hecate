package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/chat"
	"github.com/hecatehq/hecate/internal/config"
	"github.com/hecatehq/hecate/internal/projects"
)

type deferredChatCreateStore struct {
	chat.Store

	mu          sync.Mutex
	deferred    bool
	started     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newDeferredChatCreateStore(store chat.Store) *deferredChatCreateStore {
	return &deferredChatCreateStore{
		Store:   store,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *deferredChatCreateStore) Create(ctx context.Context, session chat.Session) (chat.Session, error) {
	s.mu.Lock()
	deferThisCreate := !s.deferred
	if deferThisCreate {
		s.deferred = true
		close(s.started)
	}
	s.mu.Unlock()
	if deferThisCreate {
		select {
		case <-ctx.Done():
			return chat.Session{}, ctx.Err()
		case <-s.release:
		}
	}
	return s.Store.Create(ctx, session)
}

func (s *deferredChatCreateStore) unblock() {
	s.releaseOnce.Do(func() { close(s.release) })
}

type stateMutationErrorResponse struct {
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
}

func TestStateMutationGateRejectsSnapshotsAcrossDestructiveMutation(t *testing.T) {
	t.Parallel()
	var gate stateMutationGate
	before := gate.snapshot()
	release, err := gate.beginDestructive(t.Context())
	if err != nil {
		t.Fatalf("beginDestructive: %v", err)
	}
	during := gate.snapshot()
	if releaseCreate, err := gate.beginChatCreate(during); !errors.Is(err, errDestructiveStateMutationInProgress) {
		if releaseCreate != nil {
			releaseCreate()
		}
		t.Fatalf("beginChatCreate(active mutation) error = %v, want destructive mutation conflict", err)
	}
	if secondRelease, err := gate.beginDestructive(t.Context()); !errors.Is(err, errDestructiveStateMutationInProgress) {
		if secondRelease != nil {
			secondRelease()
		}
		t.Fatalf("second beginDestructive error = %v, want deterministic conflict", err)
	}
	release()

	for name, epoch := range map[string]uint64{"before": before, "during": during} {
		t.Run(name, func(t *testing.T) {
			if releaseCreate, err := gate.beginChatCreate(epoch); !errors.Is(err, errDestructiveStateMutationInProgress) {
				if releaseCreate != nil {
					releaseCreate()
				}
				t.Fatalf("beginChatCreate(%s epoch) error = %v, want destructive mutation conflict", name, err)
			}
		})
	}
	current := gate.snapshot()
	releaseCreate, err := gate.beginChatCreate(current)
	if err != nil {
		t.Fatalf("beginChatCreate(current epoch): %v", err)
	}
	releaseCreate()
}

func TestChatCreateSerializesWithProjectDeleteAcrossAuthorities(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture func(*testing.T) (*Handler, http.Handler)
	}{
		{
			name: "Hecate project authority",
			fixture: func(t *testing.T) (*Handler, http.Handler) {
				handler := NewHandler(config.Config{Server: config.ServerConfig{DataDir: t.TempDir()}}, quietLogger(), nil, nil, nil, nil)
				handler.SetProjectStore(projects.NewMemoryStore())
				return handler, NewServer(quietLogger(), handler)
			},
		},
		{
			name: "embedded Cairnline project authority",
			fixture: func(t *testing.T) (*Handler, http.Handler) {
				return newProjectsCairnlineIdentityAuthorityTestServer(t)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, server := tc.fixture(t)
			createdProject := performRequest(t, server, http.MethodPost, "/hecate/v1/projects", `{"name":"Coordinated delete"}`)
			if createdProject.Code != http.StatusCreated {
				t.Fatalf("create project status = %d body=%s, want 201", createdProject.Code, createdProject.Body.String())
			}
			var project ProjectResponse
			if err := json.Unmarshal(createdProject.Body.Bytes(), &project); err != nil {
				t.Fatalf("decode project: %v", err)
			}

			baseChatStore := chat.NewMemoryStore()
			deferredStore := newDeferredChatCreateStore(baseChatStore)
			handler.SetAgentChatStore(deferredStore)
			defer deferredStore.unblock()
			initialEpoch := handler.stateMutationGate.snapshot()
			chatBody := fmt.Sprintf(`{"agent_id":"hecate","project_id":%q}`, project.Data.ID)
			createDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				createDone <- performRequest(t, server, http.MethodPost, "/hecate/v1/chat/sessions", chatBody)
			}()
			waitForStateMutationSignal(t, deferredStore.started, "deferred chat create")

			deleteDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				deleteDone <- performRequest(t, server, http.MethodDelete, "/hecate/v1/projects/"+project.Data.ID, "")
			}()
			waitForStateMutationEpochAdvance(t, &handler.stateMutationGate, initialEpoch)

			lateCreate := performRequest(t, server, http.MethodPost, "/hecate/v1/chat/sessions", chatBody)
			assertStateMutationChatCreateConflict(t, lateCreate)
			select {
			case deleted := <-deleteDone:
				t.Fatalf("project delete completed before reserved chat create: status=%d body=%s", deleted.Code, deleted.Body.String())
			case <-time.After(50 * time.Millisecond):
			}

			deferredStore.unblock()
			createdChat := waitForStateMutationRecorder(t, createDone, "chat create")
			if createdChat.Code != http.StatusOK {
				t.Fatalf("chat create status = %d body=%s, want 200", createdChat.Code, createdChat.Body.String())
			}
			deletedProject := waitForStateMutationRecorder(t, deleteDone, "project delete")
			if deletedProject.Code != http.StatusOK {
				t.Fatalf("project delete status = %d body=%s, want 200", deletedProject.Code, deletedProject.Body.String())
			}
			sessions, err := baseChatStore.List(t.Context())
			if err != nil {
				t.Fatalf("List chats: %v", err)
			}
			if len(sessions) != 0 {
				t.Fatalf("chats after project delete = %+v, want no project-delete survivor", sessions)
			}
		})
	}
}

func waitForStateMutationSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func waitForStateMutationEpochAdvance(t *testing.T, gate *stateMutationGate, previous uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gate.snapshot() != previous {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for destructive mutation admission")
}

func waitForStateMutationRecorder(t *testing.T, result <-chan *httptest.ResponseRecorder, operation string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case recorder := <-result:
		return recorder
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		return nil
	}
}

func assertStateMutationChatCreateConflict(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("late chat create status = %d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
	var response stateMutationErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode late chat create error: %v", err)
	}
	if response.Error.Type != errCodeSessionCreateConflict {
		t.Fatalf("late chat create error type = %q, want %q", response.Error.Type, errCodeSessionCreateConflict)
	}
}
