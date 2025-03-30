package session

import (
	"testing"
	"time"
)

func TestStore_SetAndGet(t *testing.T) {
	store := NewStore()
	
	// Test setting and getting a session
	sessionID := "test-session-id"
	targetURL := "https://example.com"
	
	store.Set(sessionID, targetURL)
	
	got, exists := store.Get(sessionID)
	if !exists {
		t.Errorf("Get(%q) returned exists=false, want true", sessionID)
	}
	if got != targetURL {
		t.Errorf("Get(%q) = %q, want %q", sessionID, got, targetURL)
	}
}

func TestStore_Remove(t *testing.T) {
	store := NewStore()
	
	// Set up a session
	sessionID := "test-session-id"
	targetURL := "https://example.com"
	store.Set(sessionID, targetURL)
	
	// Verify it exists
	_, exists := store.Get(sessionID)
	if !exists {
		t.Fatalf("Session should exist before removal")
	}
	
	// Remove the session
	store.Remove(sessionID)
	
	// Verify it no longer exists
	_, exists = store.Get(sessionID)
	if exists {
		t.Errorf("Session should not exist after removal")
	}
}

func TestStore_Cleanup(t *testing.T) {
	store := NewStore()
	
	// Set up a session
	sessionID := "test-session-id"
	targetURL := "https://example.com"
	store.Set(sessionID, targetURL)
	
	// Manually modify the lastUsed time to be older than the expiration time
	store.mu.Lock()
	info := store.sessions[sessionID]
	info.lastUsed = time.Now().Add(-25 * time.Hour) // Older than the 24-hour expiration
	store.sessions[sessionID] = info
	store.mu.Unlock()
	
	// Run cleanup
	store.cleanup()
	
	// Verify the session was removed
	_, exists := store.Get(sessionID)
	if exists {
		t.Errorf("Session should have been cleaned up")
	}
}

func TestStore_GetUpdatesLastUsed(t *testing.T) {
	store := NewStore()
	
	// Set up a session
	sessionID := "test-session-id"
	targetURL := "https://example.com"
	store.Set(sessionID, targetURL)
	
	// Get the initial last used time
	store.mu.RLock()
	initialTime := store.sessions[sessionID].lastUsed
	store.mu.RUnlock()
	
	// Wait a small amount of time
	time.Sleep(10 * time.Millisecond)
	
	// Get the session, which should update the last used time
	store.Get(sessionID)
	
	// Check that the last used time was updated
	store.mu.RLock()
	updatedTime := store.sessions[sessionID].lastUsed
	store.mu.RUnlock()
	
	if !updatedTime.After(initialTime) {
		t.Errorf("Last used time was not updated: initial=%v, updated=%v", initialTime, updatedTime)
	}
}