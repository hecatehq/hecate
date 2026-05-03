package agentchat

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type Session struct {
	ID        string
	Title     string
	AdapterID string
	Workspace string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Messages  []Message
}

type Message struct {
	ID          string
	Role        string
	Content     string
	AdapterID   string
	AdapterName string
	Status      string
	ExitCode    int
	CostMode    string
	Workspace   string
	DiffStat    string
	Diff        string
	CreatedAt   time.Time
}

type Store struct {
	mu       sync.Mutex
	sessions map[string]Session
}

func NewMemoryStore() *Store {
	return &Store{sessions: make(map[string]Session)}
}

func (s *Store) Create(session Session) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return Session{}, fmt.Errorf("session id is required")
	}
	now := time.Now().UTC()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = session.CreatedAt
	if session.Status == "" {
		session.Status = "idle"
	}
	session.Messages = append([]Message(nil), session.Messages...)
	s.sessions[session.ID] = session
	return cloneSession(session), nil
}

func (s *Store) Get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	return cloneSession(session), true
}

func (s *Store) List() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		items = append(items, cloneSession(session))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *Store) AppendMessage(sessionID string, message Message) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
	}
	if message.ID == "" {
		return Session{}, fmt.Errorf("message id is required")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	session.Messages = append(session.Messages, message)
	session.UpdatedAt = message.CreatedAt
	if message.Status != "" && message.Role == "assistant" {
		session.Status = message.Status
	}
	s.sessions[sessionID] = session
	return cloneSession(session), nil
}

func (s *Store) UpdateMessage(sessionID string, messageID string, update func(*Message)) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return Session{}, fmt.Errorf("agent chat session %q not found", sessionID)
	}
	for i := range session.Messages {
		if session.Messages[i].ID != messageID {
			continue
		}
		update(&session.Messages[i])
		session.UpdatedAt = time.Now().UTC()
		if session.Messages[i].Status != "" && session.Messages[i].Role == "assistant" {
			session.Status = session.Messages[i].Status
		}
		s.sessions[sessionID] = session
		return cloneSession(session), nil
	}
	return Session{}, fmt.Errorf("agent chat message %q not found", messageID)
}

func cloneSession(session Session) Session {
	session.Messages = append([]Message(nil), session.Messages...)
	return session
}
