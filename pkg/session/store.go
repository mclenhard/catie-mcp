package session

import (
	"sync"
	"time"
)

// Store manages mappings between session IDs and target URLs
type Store struct {
	sessions map[string]sessionInfo
	mu       sync.RWMutex
}

type sessionInfo struct {
	target   string
	lastUsed time.Time
}

// NewStore creates a new session store
func NewStore() *Store {
	store := &Store{
		sessions: make(map[string]sessionInfo),
	}

	// Start a background goroutine to clean up expired sessions
	go store.cleanupLoop()

	return store
}

// Get retrieves the target URL for a session ID
func (s *Store) Get(sessionID string) (string, bool) {
	s.mu.RLock()
	info, exists := s.sessions[sessionID]
	s.mu.RUnlock()

	if exists {
		// Update the last used time
		s.mu.Lock()
		info.lastUsed = time.Now()
		s.sessions[sessionID] = info
		s.mu.Unlock()
	}

	return info.target, exists
}

// Set stores a mapping between a session ID and a target URL
func (s *Store) Set(sessionID, target string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[sessionID] = sessionInfo{
		target:   target,
		lastUsed: time.Now(),
	}
}

// Remove deletes a session mapping
func (s *Store) Remove(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
}

// cleanupLoop periodically removes expired sessions
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes sessions that haven't been used for a while
func (s *Store) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	expireTime := time.Now().Add(-24 * time.Hour) // Sessions expire after 24 hours of inactivity

	for id, info := range s.sessions {
		if info.lastUsed.Before(expireTime) {
			delete(s.sessions, id)
		}
	}
}
