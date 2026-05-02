package chatstate

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hecate/agent-runtime/pkg/types"
)

type Filter struct {
	Tenant string
	Limit  int
	Offset int
}

// Store persists chat sessions, the flat message stream that makes up
// each conversation, and the provider calls that produced the assistant
// half of those messages. AppendExchange is the canonical write path:
// it adds new messages and the provider call that triggered them in a
// single atomic transaction, assigning monotonic per-session Sequence
// values to the messages.
type Store interface {
	Backend() string
	CreateSession(ctx context.Context, session types.ChatSession) (types.ChatSession, error)
	GetSession(ctx context.Context, id string) (types.ChatSession, bool, error)
	ListSessions(ctx context.Context, filter Filter) ([]types.ChatSession, error)
	AppendExchange(ctx context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error)
	DeleteSession(ctx context.Context, id string) error
	UpdateSession(ctx context.Context, id string, title string) (types.ChatSession, error)
	UpdateSessionSystemPrompt(ctx context.Context, id string, prompt string) (types.ChatSession, error)
}

type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]types.ChatSession
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: make(map[string]types.ChatSession)}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) CreateSession(_ context.Context, session types.ChatSession) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return types.ChatSession{}, fmt.Errorf("session id is required")
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	session.UpdatedAt = session.CreatedAt
	session.Messages = append([]types.ChatSessionMessage(nil), session.Messages...)
	session.ProviderCalls = append([]types.ChatProviderCall(nil), session.ProviderCalls...)
	s.sessions[session.ID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) GetSession(_ context.Context, id string) (types.ChatSession, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, false, nil
	}
	return cloneSession(session), true, nil
}

func (s *MemoryStore) ListSessions(_ context.Context, filter Filter) ([]types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]types.ChatSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		if filter.Tenant != "" && session.Tenant != filter.Tenant {
			continue
		}
		cloned := cloneSession(session)
		// List view doesn't carry message bodies — clients that need
		// them call GetSession. We DO keep ProviderCalls so the list
		// summary can show last-call metadata (model, cost, request_id).
		cloned.Messages = nil
		items = append(items, cloned)
	}
	sortSessionsDesc(items)
	if filter.Offset > 0 {
		if filter.Offset >= len(items) {
			return nil, nil
		}
		items = items[filter.Offset:]
	}
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) AppendExchange(_ context.Context, sessionID string, messages []types.ChatSessionMessage, call types.ChatProviderCall) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", sessionID)
	}
	now := time.Now().UTC()
	if call.CreatedAt.IsZero() {
		call.CreatedAt = now
	}
	// Sequence values are assigned monotonically per session. Existing
	// messages occupy [0, len(Messages)); the new batch picks up from
	// there.
	nextSeq := len(session.Messages)
	for i := range messages {
		if messages[i].CreatedAt.IsZero() {
			messages[i].CreatedAt = now
		}
		messages[i].Sequence = nextSeq + i
	}
	session.Messages = append(session.Messages, messages...)
	session.ProviderCalls = append(session.ProviderCalls, call)
	session.UpdatedAt = call.CreatedAt
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) DeleteSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return fmt.Errorf("chat session %q not found", id)
	}
	delete(s.sessions, id)
	return nil
}

func (s *MemoryStore) UpdateSession(_ context.Context, id string, title string) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", id)
	}
	session.Title = title
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	return cloneSession(session), nil
}

func (s *MemoryStore) UpdateSessionSystemPrompt(_ context.Context, id string, prompt string) (types.ChatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return types.ChatSession{}, fmt.Errorf("chat session %q not found", id)
	}
	session.SystemPrompt = prompt
	session.UpdatedAt = time.Now().UTC()
	s.sessions[id] = session
	return cloneSession(session), nil
}

func cloneSession(session types.ChatSession) types.ChatSession {
	cloned := session
	cloned.Messages = append([]types.ChatSessionMessage(nil), session.Messages...)
	cloned.ProviderCalls = append([]types.ChatProviderCall(nil), session.ProviderCalls...)
	return cloned
}

func sortSessionsDesc(items []types.ChatSession) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}
