package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/mclenhard/catie-mcp/pkg/config"
	"github.com/mclenhard/catie-mcp/pkg/logger"
	"github.com/mclenhard/catie-mcp/pkg/session"
	"github.com/mclenhard/catie-mcp/pkg/ui"
)

// MockConfig implements a simple config for testing
type MockConfig struct {
	DefaultURL      string
	ResourceRegexes []config.RouteRule
	ToolRegexes     []config.RouteRule
	AllTargets      []string
}

func (m *MockConfig) GetDefault() string {
	return m.DefaultURL
}

func (m *MockConfig) GetResourceRegexes() []config.RouteRule {
	return m.ResourceRegexes
}

func (m *MockConfig) GetToolRegexes() []config.RouteRule {
	return m.ToolRegexes
}

func (m *MockConfig) GetAllTargets() []string {
	return m.AllTargets
}

func (m *MockConfig) GetToolMappings() []config.ToolMapping {
	// Return an empty map for testing purposes
	return []config.ToolMapping{}
}

func TestRouteByContext(t *testing.T) {
	// Create a mock config
	mockConfig := &MockConfig{
		DefaultURL: "http://default:8080",
		ResourceRegexes: []config.RouteRule{
			{Pattern: compileRegex(t, "^/resource/([^/]+)$"), Target: "http://resource:8080"},
			{Pattern: compileRegex(t, "^/api/v1/([^/]+)$"), Target: "http://api:8080"},
		},
		ToolRegexes: []config.RouteRule{
			{Pattern: compileRegex(t, "^/tool/([^/]+)$"), Target: "http://tool:8080"},
			{Pattern: compileRegex(t, "^/utility/([^/]+)$"), Target: "http://utility:8080"},
		},
		AllTargets: []string{
			"http://default:8080",
			"http://resource:8080",
			"http://api:8080",
			"http://tool:8080",
			"http://utility:8080",
		},
	}

	// Create a router with the mock config
	r := &Router{
		Config: mockConfig,
		Logger: logger.New(logger.Debug),
	}

	// Test cases
	tests := []struct {
		name           string
		request        JSONRPCRequest
		expectedTarget string
	}{
		{
			name: "resources/read with matching URI",
			request: JSONRPCRequest{
				Method: "resources/read",
				Params: map[string]interface{}{
					"uri": "/resource/test",
				},
			},
			expectedTarget: "http://resource:8080",
		},
		{
			name: "resources/read with different matching URI",
			request: JSONRPCRequest{
				Method: "resources/read",
				Params: map[string]interface{}{
					"uri": "/api/v1/test",
				},
			},
			expectedTarget: "http://api:8080",
		},
		{
			name: "resources/read with non-matching URI",
			request: JSONRPCRequest{
				Method: "resources/read",
				Params: map[string]interface{}{
					"uri": "/nonmatching/test",
				},
			},
			expectedTarget: "http://default:8080",
		},
		{
			name: "tools/call with matching name",
			request: JSONRPCRequest{
				Method: "tools/call",
				Params: map[string]interface{}{
					"name": "/tool/test",
				},
			},
			expectedTarget: "http://tool:8080",
		},
		{
			name: "tools/call with different matching name",
			request: JSONRPCRequest{
				Method: "tools/call",
				Params: map[string]interface{}{
					"name": "/utility/test",
				},
			},
			expectedTarget: "http://utility:8080",
		},
		{
			name: "tools/call with non-matching name",
			request: JSONRPCRequest{
				Method: "tools/call",
				Params: map[string]interface{}{
					"name": "/nonmatching/test",
				},
			},
			expectedTarget: "http://default:8080",
		},
		{
			name: "unknown method",
			request: JSONRPCRequest{
				Method: "unknown/method",
				Params: map[string]interface{}{},
			},
			expectedTarget: "http://default:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := r.RouteByContext(tt.request)
			if target != tt.expectedTarget {
				t.Errorf("Expected target %s, got %s", tt.expectedTarget, target)
			}
		})
	}
}

func TestDetermineTargetForSession(t *testing.T) {
	// Create a mock config
	mockConfig := &MockConfig{
		DefaultURL: "http://default:8080",
		AllTargets: []string{
			"http://default:8080",
			"http://target1:8080",
			"http://target2:8080",
		},
	}

	// Create a router with the mock config
	r := &Router{
		Config:       mockConfig,
		SessionStore: NewTestSessionStore(),
		Logger:       logger.New(logger.Debug),
	}

	// Test with no session ID
	target := r.determineTargetForSession("")
	if target != "http://default:8080" {
		t.Errorf("Expected default target for empty session ID, got %s", target)
	}

	// Test with a known session ID
	r.SessionStore.Set("known-session", "http://target1:8080")
	target = r.determineTargetForSession("known-session")
	if target != "http://target1:8080" {
		t.Errorf("Expected target1 for known session ID, got %s", target)
	}

	// Test with an unknown session ID
	// This should hash consistently to one of the targets
	target1 := r.determineTargetForSession("unknown-session-1")
	target2 := r.determineTargetForSession("unknown-session-1") // Same ID should give same target
	if target1 != target2 {
		t.Errorf("Same session ID gave different targets: %s and %s", target1, target2)
	}

	// Different unknown session IDs might hash to different targets
	// but we can at least verify they're in our list of targets
	target3 := r.determineTargetForSession("unknown-session-2")
	found := false
	for _, t := range mockConfig.AllTargets {
		if t == target3 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Target %s not found in list of valid targets", target3)
	}
}

func TestHandleMCPRequest_Initialize(t *testing.T) {
	// Create a mock config
	mockConfig := &MockConfig{
		DefaultURL: "http://default:8080",
	}

	// Create a router with the mock config
	r := &Router{
		Config:       mockConfig,
		UI:           &ui.UI{Stats: &ui.Stats{RequestsByMethod: make(map[string]int64), RequestsByEndpoint: make(map[string]int64), ResponseTimes: make(map[string][]int)}},
		SessionStore: NewTestSessionStore(),
		Logger:       logger.New(logger.Debug),
	}

	// Create a test server that will respond to our proxied requests
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Add a session ID to the response
		w.Header().Set("Mcp-Session-Id", "test-session-123")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`))
	}))
	defer testServer.Close()

	// Override the default URL to point to our test server
	mockConfig.DefaultURL = testServer.URL

	// Create an initialize request
	initRequest := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]interface{}{},
	}
	requestBody, _ := json.Marshal(initRequest)

	// Create a test request
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Create a response recorder
	rr := httptest.NewRecorder()

	// Call the handler
	r.HandleMCPRequest(rr, req)

	// Check the response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, rr.Code)
	}

	// Check that the session ID was stored
	target, exists := r.SessionStore.Get("test-session-123")
	if !exists {
		t.Errorf("Session ID was not stored")
	}
	if target != testServer.URL {
		t.Errorf("Expected target %s, got %s", testServer.URL, target)
	}

	// Check the response body
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Errorf("Failed to parse response body: %v", err)
	}
	if response["result"].(map[string]interface{})["status"] != "ok" {
		t.Errorf("Expected status 'ok', got %v", response["result"])
	}
}

// Helper function to compile regex patterns for testing
func compileRegex(t *testing.T, pattern string) *regexp.Regexp {
	r, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("Failed to compile regex pattern '%s': %v", pattern, err)
	}
	return r
}

// NewTestSessionStore creates a simple session store for testing
func NewTestSessionStore() *session.Store {
	return session.NewStore()
}
