// Package config provides configuration handling for the MCP router proxy
package config

import (
	"log"
	"os"
	"regexp"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// RouteConfig structure for route mapping
type RouteConfig struct {
	Resources map[string]string `yaml:"resources"`
	Tools     map[string]string `yaml:"tools"`
	Default   string            `yaml:"default"`
	UI        struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"ui"`
}

// RouteRule represents a compiled regex pattern and its target
type RouteRule struct {
	Pattern *regexp.Regexp
	Target  string
}

// Config holds the application configuration and related data
type Config struct {
	RouteConfig     RouteConfig
	ResourceRegexes []RouteRule
	ToolRegexes     []RouteRule
	ConfigMutex     sync.RWMutex
}

// New creates a new Config instance and loads the initial configuration
func New(configPath string) *Config {
	c := &Config{}
	c.Load(configPath)
	return c
}

// Load reads and parses the configuration file
func (c *Config) Load(configPath string) {
	configData, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	var newConfig RouteConfig
	if err := yaml.Unmarshal(configData, &newConfig); err != nil {
		log.Fatalf("Invalid config format: %v", err)
	}

	var newResources []RouteRule
	for pattern, url := range newConfig.Resources {
		r, err := regexp.Compile(pattern)
		if err != nil {
			log.Fatalf("Invalid resource regex pattern '%s': %v", pattern, err)
		}
		newResources = append(newResources, RouteRule{r, url})
	}

	var newTools []RouteRule
	for pattern, url := range newConfig.Tools {
		r, err := regexp.Compile(pattern)
		if err != nil {
			log.Fatalf("Invalid tool regex pattern '%s': %v", pattern, err)
		}
		newTools = append(newTools, RouteRule{r, url})
	}

	c.ConfigMutex.Lock()
	c.RouteConfig = newConfig
	c.ResourceRegexes = newResources
	c.ToolRegexes = newTools
	c.ConfigMutex.Unlock()

	log.Println("Router config loaded")
}

// WatchConfig monitors the config file for changes and reloads when necessary
func (c *Config) WatchConfig(filename string, interval time.Duration) {
	lastMod := time.Time{}
	for {
		info, err := os.Stat(filename)
		if err == nil && info.ModTime().After(lastMod) {
			lastMod = info.ModTime()
			c.Load(filename)
			log.Println("Router config reloaded")
		}
		time.Sleep(interval)
	}
}

// GetDefault returns the default route
func (c *Config) GetDefault() string {
	c.ConfigMutex.RLock()
	defer c.ConfigMutex.RUnlock()
	return c.RouteConfig.Default
}

// GetResourceRegexes returns a copy of the resource regexes with read lock
func (c *Config) GetResourceRegexes() []RouteRule {
	c.ConfigMutex.RLock()
	defer c.ConfigMutex.RUnlock()
	return c.ResourceRegexes
}

// GetToolRegexes returns a copy of the tool regexes with read lock
func (c *Config) GetToolRegexes() []RouteRule {
	c.ConfigMutex.RLock()
	defer c.ConfigMutex.RUnlock()
	return c.ToolRegexes
}

// GetAllTargets returns all configured target URLs
func (c *Config) GetAllTargets() []string {
	c.ConfigMutex.RLock()
	defer c.ConfigMutex.RUnlock()

	targets := make([]string, 0)

	// Add the default target
	if c.RouteConfig.Default != "" {
		targets = append(targets, c.RouteConfig.Default)
	}

	// Add targets from resource rules
	for _, rule := range c.ResourceRegexes {
		if !contains(targets, rule.Target) {
			targets = append(targets, rule.Target)
		}
	}

	// Add targets from tool rules
	for _, rule := range c.ToolRegexes {
		if !contains(targets, rule.Target) {
			targets = append(targets, rule.Target)
		}
	}

	return targets
}

// Helper function to check if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
