package acp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Session is the in-memory state for one ACP session. A session
// corresponds 1:1 to a Hecate task; each session/prompt creates a new
// run. Sessions don't survive bridge restart.
type Session struct {
	ID              string
	HecateTaskID    string
	CurrentRunID    string
	Model           string
	CWD             string
	AlwaysAllowKeys map[string]bool
}

// SessionStore is the in-memory map of active sessions.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]*Session)}
}

// Create allocates a new session with a random ID and stores it.
func (s *SessionStore) Create(model, cwd string) *Session {
	id := newSessionID()
	sess := &Session{
		ID:              id,
		Model:           model,
		CWD:             cwd,
		AlwaysAllowKeys: make(map[string]bool),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess
}

// UpdateRun records the Hecate task/run currently backing this ACP session.
func (s *SessionStore) UpdateRun(id, taskID, runID string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return nil, false
	}
	sess.HecateTaskID = taskID
	sess.CurrentRunID = runID
	return cloneSession(sess), true
}

// Get returns the session by ID, or nil if not found.
func (s *SessionStore) Get(id string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSession(s.sessions[id])
}

// Delete removes a session. Idempotent.
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Len returns the number of active sessions.
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("acp: crypto/rand.Read failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func cloneSession(sess *Session) *Session {
	if sess == nil {
		return nil
	}
	copy := *sess
	copy.AlwaysAllowKeys = make(map[string]bool, len(sess.AlwaysAllowKeys))
	for key, value := range sess.AlwaysAllowKeys {
		copy.AlwaysAllowKeys[key] = value
	}
	return &copy
}
