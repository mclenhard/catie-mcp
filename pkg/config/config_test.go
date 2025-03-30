package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := `
default: http://default-service:8080
resources:
  "^/resource/([^/]+)$": "http://resource-service:8080"
tools:
  "^/tool/([^/]+)$": "http://tool-service:8080"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Test creating a new config
	cfg := New(configPath)

	// Verify the config was loaded correctly
	if cfg.GetDefault() != "http://default-service:8080" {
		t.Errorf("Expected default to be 'http://default-service:8080', got '%s'", cfg.GetDefault())
	}

	if len(cfg.GetResourceRegexes()) != 1 {
		t.Errorf("Expected 1 resource regex, got %d", len(cfg.GetResourceRegexes()))
	}

	if len(cfg.GetToolRegexes()) != 1 {
		t.Errorf("Expected 1 tool regex, got %d", len(cfg.GetToolRegexes()))
	}
}

func TestLoad(t *testing.T) {
	// Table-driven test
	tests := []struct {
		name                  string
		configContent         string
		expectedDefault       string
		expectedResourceCount int
		expectedToolCount     int
	}{
		{
			name: "basic config",
			configContent: `
default: http://default-service:8080
resources:
  "^/resource/([^/]+)$": "http://resource-service:8080"
tools:
  "^/tool/([^/]+)$": "http://tool-service:8080"
`,
			expectedDefault:       "http://default-service:8080",
			expectedResourceCount: 1,
			expectedToolCount:     1,
		},
		{
			name: "multiple resources and tools",
			configContent: `
default: http://default-service:8080
resources:
  "^/resource/([^/]+)$": "http://resource-service:8080"
  "^/api/v1/([^/]+)$": "http://api-service:8080"
tools:
  "^/tool/([^/]+)$": "http://tool-service:8080"
  "^/utility/([^/]+)$": "http://utility-service:8080"
`,
			expectedDefault:       "http://default-service:8080",
			expectedResourceCount: 2,
			expectedToolCount:     2,
		},
		{
			name: "no default",
			configContent: `
resources:
  "^/resource/([^/]+)$": "http://resource-service:8080"
tools:
  "^/tool/([^/]+)$": "http://tool-service:8080"
`,
			expectedDefault:       "",
			expectedResourceCount: 1,
			expectedToolCount:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with test content
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(tt.configContent), 0644); err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			// Create config and test loading
			cfg := &Config{}
			cfg.Load(configPath)

			if cfg.GetDefault() != tt.expectedDefault {
				t.Errorf("Expected default to be '%s', got '%s'", tt.expectedDefault, cfg.GetDefault())
			}

			if len(cfg.GetResourceRegexes()) != tt.expectedResourceCount {
				t.Errorf("Expected %d resource regexes, got %d", tt.expectedResourceCount, len(cfg.GetResourceRegexes()))
			}

			if len(cfg.GetToolRegexes()) != tt.expectedToolCount {
				t.Errorf("Expected %d tool regexes, got %d", tt.expectedToolCount, len(cfg.GetToolRegexes()))
			}
		})
	}
}

func TestGetAllTargets(t *testing.T) {
	// Create a config with known values
	cfg := &Config{
		RouteConfig: RouteConfig{
			Default: "http://default:8080",
			Resources: map[string]string{
				"pattern1": "http://resource1:8080",
				"pattern2": "http://resource2:8080",
			},
			Tools: map[string]string{
				"pattern3": "http://tool1:8080",
				"pattern4": "http://resource1:8080", // Duplicate target
			},
		},
	}

	// Manually set up the regexes (normally done by Load)
	cfg.ResourceRegexes = []RouteRule{
		{Pattern: nil, Target: "http://resource1:8080"},
		{Pattern: nil, Target: "http://resource2:8080"},
	}

	cfg.ToolRegexes = []RouteRule{
		{Pattern: nil, Target: "http://tool1:8080"},
		{Pattern: nil, Target: "http://resource1:8080"},
	}

	targets := cfg.GetAllTargets()

	// Update the expected number of unique targets
	expectedTargets := 4 // default + resource1 + resource2 + tool1
	if len(targets) != expectedTargets {
		t.Errorf("Expected %d unique targets, got %d: %v", expectedTargets, len(targets), targets)
	}

	// Check that all expected targets are present
	expectedURLs := []string{
		"http://default:8080",
		"http://resource1:8080",
		"http://resource2:8080",
		"http://tool1:8080",
	}

	for _, url := range expectedURLs[:expectedTargets] {
		if !contains(targets, url) {
			t.Errorf("Expected target '%s' not found in results: %v", url, targets)
		}
	}
}

func TestWatchConfig(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	initialConfig := `
default: http://initial-default:8080
resources:
  "^/resource/([^/]+)$": "http://initial-resource:8080"
tools:
  "^/tool/([^/]+)$": "http://initial-tool:8080"
`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("Failed to write initial test config: %v", err)
	}

	// Create config
	cfg := New(configPath)

	// Start watching in a goroutine with a short interval
	go cfg.WatchConfig(configPath, 100*time.Millisecond)

	// Verify initial config
	if cfg.GetDefault() != "http://initial-default:8080" {
		t.Errorf("Initial default incorrect, got '%s'", cfg.GetDefault())
	}

	// Wait a moment to ensure watcher is running
	time.Sleep(200 * time.Millisecond)

	// Update the config file
	updatedConfig := `
default: http://updated-default:8080
resources:
  "^/resource/([^/]+)$": "http://updated-resource:8080"
  "^/new-resource/([^/]+)$": "http://new-resource:8080"
tools:
  "^/tool/([^/]+)$": "http://updated-tool:8080"
`
	if err := os.WriteFile(configPath, []byte(updatedConfig), 0644); err != nil {
		t.Fatalf("Failed to write updated test config: %v", err)
	}

	// Wait for the watcher to detect the change
	time.Sleep(300 * time.Millisecond)

	// Verify config was updated
	if cfg.GetDefault() != "http://updated-default:8080" {
		t.Errorf("Updated default incorrect, got '%s'", cfg.GetDefault())
	}

	if len(cfg.GetResourceRegexes()) != 2 {
		t.Errorf("Expected 2 resource regexes after update, got %d", len(cfg.GetResourceRegexes()))
	}
}
