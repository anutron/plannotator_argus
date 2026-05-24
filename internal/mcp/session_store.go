package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SessionStatus is the lifecycle state of a Plannotator subprocess driven
// by an MCP verb tool.
type SessionStatus string

const (
	StatusPending   SessionStatus = "pending"
	StatusComplete  SessionStatus = "complete"
	StatusFailed    SessionStatus = "failed"
	StatusCancelled SessionStatus = "cancelled"
)

// ErrUnknownSession is returned when a session_id is not (or no longer)
// in the store.
var ErrUnknownSession = errors.New("unknown or expired session")

// Session is the in-memory record of one verb invocation.
type Session struct {
	ID          string
	Verb        string
	URL         string
	Status      SessionStatus
	Result      json.RawMessage
	Error       string
	StartedAt   time.Time
	CompletedAt time.Time

	done chan struct{} // closed when Status != Pending
}

// SessionStore is a concurrent-safe map of session_id → *Session with a
// long-poll primitive and TTL-based garbage collection.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	now      func() time.Time
}

// NewSessionStore returns an empty store.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		now:      time.Now,
	}
}

// Create inserts a new pending session, returns the *Session.
func (s *SessionStore) Create(verb string) *Session {
	sess := &Session{
		ID:        uuid.NewString(),
		Verb:      verb,
		Status:    StatusPending,
		StartedAt: s.now(),
		done:      make(chan struct{}),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	return sess
}

// SetURL records the browser URL Plannotator opened for this session.
// Idempotent — safe to call multiple times.
func (s *SessionStore) SetURL(id, url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		sess.URL = url
	}
}

// MarkComplete transitions a session to Complete with the given result.
// No-op if the session is already terminal.
func (s *SessionStore) MarkComplete(id string, result json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || sess.Status != StatusPending {
		return
	}
	sess.Status = StatusComplete
	sess.Result = result
	sess.CompletedAt = s.now()
	close(sess.done)
}

// MarkFailed transitions a session to Failed.
func (s *SessionStore) MarkFailed(id, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || sess.Status != StatusPending {
		return
	}
	sess.Status = StatusFailed
	sess.Error = errMsg
	sess.CompletedAt = s.now()
	close(sess.done)
}

// Get returns a snapshot of the session by id, or ErrUnknownSession.
func (s *SessionStore) Get(id string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrUnknownSession
	}
	return *sess, nil
}

// WaitForResolution blocks until the session is no longer Pending, until
// the deadline elapses, or until ctx is cancelled. Returns the session
// snapshot at the moment the wait ended.
func (s *SessionStore) WaitForResolution(ctx context.Context, id string, timeout time.Duration) (Session, error) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	if !ok {
		s.mu.RUnlock()
		return Session{}, ErrUnknownSession
	}
	// Snapshot status and done channel under the lock before checking.
	pending := sess.Status == StatusPending
	done := sess.done
	if !pending || timeout <= 0 {
		snap := *sess
		s.mu.RUnlock()
		return snap, nil
	}
	s.mu.RUnlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var ctxErr error
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
		ctxErr = ctx.Err()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if cur, ok := s.sessions[id]; ok {
		return *cur, ctxErr
	}
	return Session{}, ErrUnknownSession
}

// GC removes sessions whose CompletedAt is older than ttl.
// In-flight sessions (Status == Pending) are never GC'd.
func (s *SessionStore) GC(ttl time.Duration) int {
	cutoff := s.now().Add(-ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, sess := range s.sessions {
		if sess.Status == StatusPending {
			continue
		}
		if sess.CompletedAt.Before(cutoff) {
			delete(s.sessions, id)
			removed++
		}
	}
	return removed
}

// Len returns the number of sessions in the store.
func (s *SessionStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
