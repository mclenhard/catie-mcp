package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mclenhard/catie-mcp/pkg/config"
)

func TestRecordRequest(t *testing.T) {
	stats := &Stats{
		RequestsByMethod:   make(map[string]int64),
		RequestsByEndpoint: make(map[string]int64),
		ResponseTimes:      make(map[string][]int),
		StartTime:          time.Now(),
	}

	// Record some test requests
	stats.RecordRequest("GET", "/api/users", 150*time.Millisecond, false)
	stats.RecordRequest("POST", "/api/users", 200*time.Millisecond, false)
	stats.RecordRequest("GET", "/api/products", 100*time.Millisecond, true)

	// Verify stats were recorded correctly
	if stats.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", stats.TotalRequests)
	}

	if stats.RequestsByMethod["GET"] != 2 {
		t.Errorf("Expected 2 GET requests, got %d", stats.RequestsByMethod["GET"])
	}

	if stats.RequestsByMethod["POST"] != 1 {
		t.Errorf("Expected 1 POST request, got %d", stats.RequestsByMethod["POST"])
	}

	if stats.RequestsByEndpoint["/api/users"] != 2 {
		t.Errorf("Expected 2 /api/users requests, got %d", stats.RequestsByEndpoint["/api/users"])
	}

	if stats.ErrorCount != 1 {
		t.Errorf("Expected 1 error, got %d", stats.ErrorCount)
	}

	// Check response times
	if len(stats.ResponseTimes["GET"]) != 2 {
		t.Errorf("Expected 2 response times for GET, got %d", len(stats.ResponseTimes["GET"]))
	}
}

func TestGetStats(t *testing.T) {
	stats := &Stats{
		TotalRequests:      10,
		RequestsByMethod:   map[string]int64{"GET": 7, "POST": 3},
		RequestsByEndpoint: map[string]int64{"/api/users": 5, "/api/products": 5},
		ResponseTimes:      map[string][]int{"GET": {100, 150}, "POST": {200}},
		ErrorCount:         2,
		StartTime:          time.Now(),
	}

	// Get a copy of the stats
	statsCopy := stats.GetStats()

	// Verify the copy has the correct values
	if statsCopy.TotalRequests != 10 {
		t.Errorf("Expected 10 total requests in copy, got %d", statsCopy.TotalRequests)
	}

	// Modify the original stats
	stats.TotalRequests = 15
	stats.RequestsByMethod["GET"] = 10

	// Verify the copy wasn't affected
	if statsCopy.TotalRequests != 10 {
		t.Errorf("Stats copy was affected by changes to original")
	}

	if statsCopy.RequestsByMethod["GET"] != 7 {
		t.Errorf("Stats copy was affected by changes to original")
	}
}

func TestHandleStats(t *testing.T) {
	// Create a UI instance with test data
	cfg := &config.Config{
		RouteConfig: config.RouteConfig{
			UI: struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			}{},
		},
	}
	ui := New(cfg)

	// Add some test data
	ui.Stats.RecordRequest("GET", "/api/users", 150*time.Millisecond, false)
	ui.Stats.RecordRequest("POST", "/api/users", 200*time.Millisecond, false)

	// Test HTML response
	req, _ := http.NewRequest("GET", "/stats", nil)
	rr := httptest.NewRecorder()

	ui.HandleStats(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if ctype := rr.Header().Get("Content-Type"); ctype != "text/html" {
		t.Errorf("Content-Type header does not match: got %v want %v", ctype, "text/html")
	}

	// Test JSON response
	req, _ = http.NewRequest("GET", "/stats", nil)
	req.Header.Set("Accept", "application/json")
	rr = httptest.NewRecorder()

	ui.HandleStats(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if ctype := rr.Header().Get("Content-Type"); ctype != "application/json" {
		t.Errorf("Content-Type header does not match: got %v want %v", ctype, "application/json")
	}

	// Verify we can parse the JSON response
	var data map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &data); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}
}

func TestHandleMetrics(t *testing.T) {
	// Create a UI instance with test data
	cfg := &config.Config{
		RouteConfig: config.RouteConfig{
			UI: struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			}{},
		},
	}
	ui := New(cfg)

	// Add some test data
	ui.Stats.RecordRequest("GET", "/api/users", 150*time.Millisecond, false)
	ui.Stats.RecordRequest("POST", "/api/users", 200*time.Millisecond, true)

	// Test metrics response
	req, _ := http.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	ui.HandleMetrics(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	if ctype := rr.Header().Get("Content-Type"); ctype != "text/plain" {
		t.Errorf("Content-Type header does not match: got %v want %v", ctype, "text/plain")
	}

	// Check for expected metrics in the response
	body := rr.Body.String()
	expectedMetrics := []string{
		"mcp_router_requests_total 2",
		"mcp_router_errors_total 1",
		"mcp_router_requests_by_method{method=\"GET\"} 1",
		"mcp_router_requests_by_method{method=\"POST\"} 1",
		"mcp_router_requests_by_endpoint{endpoint=\"/api/users\"} 2",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected metric not found in response: %s", metric)
		}
	}
}

func TestBasicAuth(t *testing.T) {
	// Test with auth credentials
	cfg := &config.Config{
		RouteConfig: config.RouteConfig{
			UI: struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			}{
				Username: "admin",
				Password: "secret",
			},
		},
	}
	ui := New(cfg)

	// Create a simple handler for testing
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Success"))
	})

	// Wrap it with basic auth
	authHandler := ui.basicAuth(testHandler)

	// Test with no credentials
	req, _ := http.NewRequest("GET", "/stats", nil)
	rr := httptest.NewRecorder()

	authHandler(rr, req)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("Handler should return 401 without credentials, got: %v", status)
	}

	// Test with invalid credentials
	req, _ = http.NewRequest("GET", "/stats", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:wrongpass")))
	rr = httptest.NewRecorder()

	authHandler(rr, req)

	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("Handler should return 401 with invalid credentials, got: %v", status)
	}

	// Test with valid credentials
	req, _ = http.NewRequest("GET", "/stats", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:secret")))
	rr = httptest.NewRecorder()

	authHandler(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler should return 200 with valid credentials, got: %v", status)
	}

	// Test with no auth required
	cfg = &config.Config{
		RouteConfig: config.RouteConfig{
			UI: struct {
				Username string `yaml:"username"`
				Password string `yaml:"password"`
			}{},
		},
	}
	ui = New(cfg)

	authHandler = ui.basicAuth(testHandler)
	req, _ = http.NewRequest("GET", "/stats", nil)
	rr = httptest.NewRecorder()

	authHandler(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler should return 200 when no auth required, got: %v", status)
	}
}
