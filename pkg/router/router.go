package router

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mclenhard/catie-mcp/pkg/config"
	"github.com/mclenhard/catie-mcp/pkg/logger"
	"github.com/mclenhard/catie-mcp/pkg/session"
	"github.com/mclenhard/catie-mcp/pkg/ui"
)

// JSONRPCRequest structure
type JSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// ConfigInterface is an interface that both MockConfig and config.Config can implement
type ConfigInterface interface {
	GetDefault() string
	GetResourceRegexes() []config.RouteRule
	GetToolRegexes() []config.RouteRule
	GetAllTargets() []string
	GetToolMappings() []config.ToolMapping
}

// Router handles the routing of MCP requests
type Router struct {
	Config       ConfigInterface // Changed from config.Config
	UI           *ui.UI
	SessionStore *session.Store
	Logger       *logger.Logger
}

// New creates a new Router instance
func New(cfg *config.Config, ui *ui.UI) *Router {
	return &Router{
		Config:       cfg,
		UI:           ui,
		SessionStore: session.NewStore(),
		Logger:       logger.New(logger.Info), // Default to Info level
	}
}

// Add this constant at the top of the file
const (
	// DefaultTimeout is the default timeout for HTTP requests
	DefaultTimeout = 30 * time.Second
)

// HandleMCPRequest processes incoming MCP requests and routes them to the appropriate target
func (r *Router) HandleMCPRequest(w http.ResponseWriter, req *http.Request) {
	startTime := time.Now()
	var isError bool

	r.Logger.Info("Received request: Method=%s, Path=%s, ContentType=%s",
		req.Method, req.URL.Path, req.Header.Get("Content-Type"))

	// Handle OPTIONS requests (CORS preflight)
	if req.Method == http.MethodOptions {
		r.Logger.Debug("Handling OPTIONS request")
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Session-Id, Authorization, MCP-Protocol-Version")
		w.Header().Set("Access-Control-Max-Age", "86400") // 24 hours

		// Respond with 200 OK for OPTIONS requests
		w.WriteHeader(http.StatusOK)
		r.UI.Stats.RecordRequest("OPTIONS", "cors", time.Since(startTime), false)
		return
	}

	// Check if this is a GET request (for SSE streaming)
	if req.Method == http.MethodGet {
		r.Logger.Debug("Handling GET request for SSE")
		// Extract session ID if present
		sessionID := req.Header.Get("Mcp-Session-Id")
		r.Logger.Debug("Session ID: %s", sessionID)

		// Determine target based on session ID or other routing logic
		targetURL := r.determineTargetForSession(sessionID)
		r.Logger.Info("Routing SSE stream to target: %s", targetURL)

		// Set up SSE headers for client
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Create a new GET request to the target server
		proxyReq, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			r.Logger.Error("Error creating proxy request: %v", err)
			http.Error(w, "error creating proxy request: "+err.Error(), http.StatusInternalServerError)
			isError = true
			r.UI.Stats.RecordRequest("GET", targetURL, time.Since(startTime), isError)
			return
		}

		// Copy relevant headers from the original request
		proxyReq.Header.Set("Accept", "text/event-stream")
		if sessionID != "" {
			proxyReq.Header.Set("Mcp-Session-Id", sessionID)
		}

		// Copy Last-Event-ID header if present (for resuming streams)
		if lastEventID := req.Header.Get("Last-Event-ID"); lastEventID != "" {
			proxyReq.Header.Set("Last-Event-ID", lastEventID)
		}

		// Make the request to the target server
		client := &http.Client{
			// No timeout for SSE connections
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			r.Logger.Error("Error connecting to target server: %v", err)
			http.Error(w, "error connecting to target server: "+err.Error(), http.StatusBadGateway)
			isError = true
			r.UI.Stats.RecordRequest("GET", targetURL, time.Since(startTime), isError)
			return
		}
		defer resp.Body.Close()

		// Check if the target server returned an error
		if resp.StatusCode != http.StatusOK {
			// Read error body and forward it to the client
			errorBody, _ := io.ReadAll(resp.Body)
			r.Logger.Error("Target server returned error status %d: %s", resp.StatusCode, string(errorBody))
			http.Error(w, string(errorBody), resp.StatusCode)
			isError = true
			r.UI.Stats.RecordRequest("GET", targetURL, time.Since(startTime), isError)
			return
		}

		r.Logger.Debug("Successfully connected to SSE stream from target")

		// Create a context that's canceled when the client connection closes
		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()

		// Create a done channel to signal when to close the connection
		done := make(chan bool)
		var once sync.Once // Add this to ensure the channel is only closed once

		// Handle client disconnection using context
		go func() {
			<-ctx.Done()
			once.Do(func() { close(done) }) // Use sync.Once to safely close the channel
			// Also close the response body to terminate the connection to the target
			resp.Body.Close()
			r.Logger.Debug("Client disconnected, closing SSE stream")
		}()

		// Stream SSE events from target to client
		go func() {
			scanner := bufio.NewScanner(resp.Body)
			// Set a larger buffer for the scanner to handle large SSE events
			const maxScanTokenSize = 1024 * 1024 // 1MB
			buf := make([]byte, maxScanTokenSize)
			scanner.Buffer(buf, maxScanTokenSize)

			eventCount := 0
			for scanner.Scan() {
				select {
				case <-done:
					return
				default:
					line := scanner.Text()
					fmt.Fprintf(w, "%s\n", line)
					// If this is the end of an event, flush the buffer
					if line == "" {
						eventCount++
						if eventCount%100 == 0 {
							r.Logger.Debug("Streamed %d SSE events so far", eventCount)
						}
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
					}
				}
			}

			if err := scanner.Err(); err != nil {
				r.Logger.Error("Error scanning SSE stream: %v", err)
			}

			r.Logger.Info("SSE stream closed after sending %d events", eventCount)
			once.Do(func() { close(done) }) // Safely close the channel if not already closed
		}()

		// Set up heartbeat ticker
		heartbeatTicker := time.NewTicker(30 * time.Second)
		defer heartbeatTicker.Stop()

		// Send heartbeat events to keep the connection alive
		go func() {
			for {
				select {
				case <-heartbeatTicker.C:
					// Send a heartbeat event
					fmt.Fprintf(w, "event: heartbeat\ndata: %d\n\n", time.Now().Unix())
					if flusher, ok := w.(http.Flusher); ok {
						flusher.Flush()
					}
					r.Logger.Debug("Sent heartbeat event")
				case <-done:
					return
				}
			}
		}()

		// Wait until done
		<-done
		r.UI.Stats.RecordRequest("GET", targetURL, time.Since(startTime), isError)
		return
	}

	// Handle POST requests (client sending messages to server)
	if req.Method == http.MethodPost {
		r.Logger.Debug("Handling POST request")

		// Read the request body
		body, err := io.ReadAll(req.Body)
		if err != nil {
			r.Logger.Error("Error reading request body: %v", err)
			http.Error(w, "error reading request body: "+err.Error(), http.StatusBadRequest)
			isError = true
			r.UI.Stats.RecordRequest("POST", "unknown", time.Since(startTime), isError)
			return
		}

		// Parse the JSON-RPC request to determine routing
		var jsonRPCRequest JSONRPCRequest
		if err := json.Unmarshal(body, &jsonRPCRequest); err != nil {
			// Try to parse as a batch request
			var batchRequest []JSONRPCRequest
			if batchErr := json.Unmarshal(body, &batchRequest); batchErr != nil {
				r.Logger.Error("Error parsing JSON-RPC request: %v", err)
				http.Error(w, "error parsing JSON-RPC request: "+err.Error(), http.StatusBadRequest)
				isError = true
				r.UI.Stats.RecordRequest("POST", "invalid_json", time.Since(startTime), isError)
				return
			}
			// Use the first request in the batch for routing
			if len(batchRequest) > 0 {
				jsonRPCRequest = batchRequest[0]
			}
		}

		// Extract session ID if present
		sessionID := req.Header.Get("Mcp-Session-Id")
		r.Logger.Debug("Session ID: %s", sessionID)

		// Check if this is an initialization request
		isInitialize := jsonRPCRequest.Method == "initialize"

		// Determine target URL based on the request content
		targetURL := r.determineTargetForSession(sessionID)
		r.Logger.Info("Routing request to target: %s", targetURL)

		// Apply tool name transformation if needed
		r.transformToolCall(jsonRPCRequest, targetURL)

		// Re-encode the possibly modified request
		modifiedBody, err := json.Marshal(jsonRPCRequest)
		if err != nil {
			http.Error(w, "error encoding request: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Create a new POST request to the target server
		proxyReq, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(modifiedBody))
		if err != nil {
			r.Logger.Error("Error creating proxy request: %v", err)
			http.Error(w, "error creating proxy request: "+err.Error(), http.StatusInternalServerError)
			isError = true
			r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
			return
		}

		// Copy headers from the original request
		proxyReq.Header.Set("Content-Type", "application/json")

		// Set Accept header to support both JSON and SSE responses
		proxyReq.Header.Set("Accept", req.Header.Get("Accept"))
		if proxyReq.Header.Get("Accept") == "" {
			proxyReq.Header.Set("Accept", "application/json, text/event-stream")
		}

		// Forward session ID if present
		if sessionID != "" {
			proxyReq.Header.Set("Mcp-Session-Id", sessionID)
		}

		// Make the request to the target server with a reasonable timeout
		client := &http.Client{
			Timeout: 30 * time.Second, // Use a longer timeout for initialization
		}

		resp, err := client.Do(proxyReq)
		if err != nil {
			r.Logger.Error("Error connecting to target server: %v", err)
			http.Error(w, "error connecting to target server: "+err.Error(), http.StatusBadGateway)
			isError = true
			r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
			return
		}
		defer resp.Body.Close()

		// Check if the target server returned an error
		if resp.StatusCode >= 400 {
			// Read error body and forward it to the client
			errorBody, _ := io.ReadAll(resp.Body)
			r.Logger.Error("Target server returned error status %d: %s", resp.StatusCode, string(errorBody))
			for name, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(errorBody)
			isError = true
			r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
			return
		}

		// Check if this is an initialization response with a session ID
		if isInitialize && resp.Header.Get("Mcp-Session-Id") != "" {
			newSessionID := resp.Header.Get("Mcp-Session-Id")
			r.Logger.Info("Received new session ID: %s", newSessionID)

			// Store the session ID and target URL mapping
			r.SessionStore.Set(newSessionID, targetURL)
		}

		// Check content type to determine how to handle the response
		contentType := resp.Header.Get("Content-Type")

		// Copy all response headers to the client
		for name, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}

		// If the response is an SSE stream, handle it accordingly
		if strings.Contains(contentType, "text/event-stream") {
			r.Logger.Debug("Target server returned SSE stream")

			// Set SSE headers
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Create a context that's canceled when the client connection closes
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()

			// Create a done channel to signal when to close the connection
			done := make(chan bool)
			var once sync.Once // Add this to ensure the channel is only closed once

			// Handle client disconnection
			go func() {
				<-ctx.Done()
				once.Do(func() { close(done) }) // Use sync.Once to safely close the channel
				r.Logger.Debug("Client disconnected, closing SSE stream")
			}()

			// Stream SSE events from target to client
			scanner := bufio.NewScanner(resp.Body)
			// Set a larger buffer for the scanner to handle large SSE events
			const maxScanTokenSize = 1024 * 1024 // 1MB
			buf := make([]byte, maxScanTokenSize)
			scanner.Buffer(buf, maxScanTokenSize)

			go func() {
				for scanner.Scan() {
					select {
					case <-done:
						return
					default:
						line := scanner.Text()
						fmt.Fprintf(w, "%s\n", line)
						// If this is the end of an event, flush the buffer
						if line == "" {
							if flusher, ok := w.(http.Flusher); ok {
								flusher.Flush()
							}
						}
					}
				}

				if err := scanner.Err(); err != nil {
					r.Logger.Error("Error scanning SSE stream: %v", err)
				}

				once.Do(func() { close(done) }) // Safely close the channel if not already closed
			}()

			// Set up heartbeat ticker
			heartbeatTicker := time.NewTicker(30 * time.Second)
			defer heartbeatTicker.Stop()

			// Send heartbeat events to keep the connection alive
			go func() {
				for {
					select {
					case <-heartbeatTicker.C:
						// Send a heartbeat event
						fmt.Fprintf(w, "event: heartbeat\ndata: %d\n\n", time.Now().Unix())
						if flusher, ok := w.(http.Flusher); ok {
							flusher.Flush()
						}
						r.Logger.Debug("Sent heartbeat event")
					case <-done:
						return
					}
				}
			}()

			// Wait until done
			<-done
			r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
			return
		}

		// For regular JSON responses, just copy the response body
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			r.Logger.Error("Error reading response body: %v", err)
			http.Error(w, "error reading response body: "+err.Error(), http.StatusInternalServerError)
			isError = true
			r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
			return
		}

		// Write the response status code and body
		w.WriteHeader(resp.StatusCode)
		w.Write(responseBody)

		// If this was an initialization request, log the response
		if isInitialize {
			r.Logger.Info("Successfully processed initialization request")

			// Parse the response to extract any relevant information
			var initResponse map[string]interface{}
			if err := json.Unmarshal(responseBody, &initResponse); err == nil {
				r.Logger.Debug("Initialization response: %v", initResponse)
			}
		}

		r.UI.Stats.RecordRequest("POST", targetURL, time.Since(startTime), isError)
		return
	}

	// Handle POST requests (client sending messages to server)
	if req.Method != http.MethodPost {
		r.Logger.Warn("Received unsupported method: %s", req.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		isError = true
		r.UI.Stats.RecordRequest("unknown", "error", time.Since(startTime), isError)
		return
	}

	// Check Accept header
	acceptHeader := req.Header.Get("Accept")
	if !strings.Contains(acceptHeader, "application/json") && !strings.Contains(acceptHeader, "text/event-stream") {
		r.Logger.Warn("Invalid Accept header: %s", acceptHeader)
		http.Error(w, "Invalid Accept header", http.StatusBadRequest)
		isError = true
		r.UI.Stats.RecordRequest("unknown", "error", time.Since(startTime), isError)
		return
	}

	// Extract session ID if present
	sessionID := req.Header.Get("Mcp-Session-Id")
	r.Logger.Debug("Received POST request with session ID: %s", sessionID)

	body, err := io.ReadAll(req.Body)
	if err != nil {
		r.Logger.Error("Failed to read request body: %v", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		isError = true
		r.UI.Stats.RecordRequest("unknown", "error", time.Since(startTime), isError)
		return
	}

	// Try to parse as a single request or as a batch
	var method string
	var targetURL string

	// First try to parse as a single request
	var rpcReq JSONRPCRequest
	if err := json.Unmarshal(body, &rpcReq); err == nil {
		// Successfully parsed as a single request
		method = rpcReq.Method
		r.Logger.Debug("Parsed single JSON-RPC request with method: %s", method)

		// Determine target URL based on the request
		if rpcReq.Method == "initialize" {
			// For initialize requests, use the default target
			targetURL = r.Config.GetDefault()
			r.Logger.Info("Initialize request, using default target: %s", targetURL)
		} else {
			// For other requests, use the routing logic
			targetURL = r.RouteByContext(rpcReq)
			r.Logger.Info("Routing method '%s' to target: %s", method, targetURL)
		}
	} else {
		// Try to parse as a batch
		var batchReq []JSONRPCRequest
		if err := json.Unmarshal(body, &batchReq); err == nil {
			// Successfully parsed as a batch
			r.Logger.Debug("Parsed batch request with %d methods", len(batchReq))

			// For simplicity, use the first request's method for logging
			if len(batchReq) > 0 {
				method = batchReq[0].Method

				// For batch requests, we need a routing strategy
				// Here we use the first request to determine the target
				targetURL = r.RouteByContext(batchReq[0])
				r.Logger.Info("Routing batch request (first method: '%s') to target: %s", method, targetURL)
			} else {
				method = "batch"
				targetURL = r.Config.GetDefault()
				r.Logger.Info("Empty batch request, using default target: %s", targetURL)
			}
		} else {
			// Failed to parse as either single request or batch
			r.Logger.Error("Failed to parse JSON-RPC request: %v", err)
			http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
			isError = true
			r.UI.Stats.RecordRequest("unknown", "error", time.Since(startTime), isError)
			return
		}
	}

	// Create a new POST request to the target server
	proxyReq, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		r.Logger.Error("Error creating proxy request: %v", err)
		http.Error(w, "error creating proxy request: "+err.Error(), http.StatusInternalServerError)
		isError = true
		r.UI.Stats.RecordRequest(method, targetURL, time.Since(startTime), isError)
		return
	}

	// Copy relevant headers from the original request
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Accept", acceptHeader)
	if sessionID != "" {
		proxyReq.Header.Set("Mcp-Session-Id", sessionID)
	}

	// Forward the Authorization header
	if authHeader := req.Header.Get("Authorization"); authHeader != "" {
		proxyReq.Header.Set("Authorization", authHeader)
		r.Logger.Debug("Forwarding Authorization header")
	}

	// Forward MCP-Protocol-Version header if present
	if protocolVersion := req.Header.Get("MCP-Protocol-Version"); protocolVersion != "" {
		proxyReq.Header.Set("MCP-Protocol-Version", protocolVersion)
	}

	// Make the request to the target server
	client := &http.Client{
		Timeout: DefaultTimeout,
	}
	ctx, cancel := context.WithTimeout(req.Context(), DefaultTimeout)
	defer cancel()
	proxyReq = proxyReq.WithContext(ctx)
	resp, err := client.Do(proxyReq)
	if err != nil {
		r.Logger.Error("Error forwarding request to target: %v", err)
		http.Error(w, "error forwarding request: "+err.Error(), http.StatusBadGateway)
		isError = true
		r.UI.Stats.RecordRequest(method, targetURL, time.Since(startTime), isError)
		return
	}
	defer resp.Body.Close()

	// Check if the response contains a session ID
	sessionID = resp.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		// Store the session ID -> target mapping
		r.SessionStore.Set(sessionID, targetURL)
		r.Logger.Debug("Stored session mapping: %s -> %s", sessionID, targetURL)
	}

	// Copy response headers
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Copy response status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		r.Logger.Error("Error reading response body: %v", err)
		// We've already written headers, so we can't send an HTTP error
		return
	}
	w.Write(respBody)

	// Record stats
	duration := time.Since(startTime)
	isError = resp.StatusCode >= 400
	r.UI.Stats.RecordRequest(method, targetURL, duration, isError)
}

// determineTargetForSession returns the target URL for a given session ID
func (r *Router) determineTargetForSession(sessionID string) string {
	// If no session ID is provided, return the default target
	if sessionID == "" {
		r.Logger.Debug("No session ID provided, using default target")
		return r.Config.GetDefault()
	}

	// Check if we have this session ID in our session store
	if target, exists := r.SessionStore.Get(sessionID); exists {
		r.Logger.Debug("Found existing session mapping: %s -> %s", sessionID, target)
		return target
	}

	// If the session ID is not recognized, we have a few options:
	// 1. Return the default target
	// 2. Use a consistent hashing algorithm to map the session ID to a target
	// 3. Use a round-robin or load-balancing approach

	// For now, we'll use a simple approach - hash the session ID to consistently
	// map it to one of our configured targets
	targets := r.Config.GetAllTargets()
	if len(targets) == 0 {
		return r.Config.GetDefault()
	}

	// Use a hash of the session ID to pick a target
	h := fnv.New32a()
	h.Write([]byte(sessionID))
	index := int(h.Sum32()) % len(targets)
	target := targets[index]

	r.Logger.Debug("Created new session mapping via hashing: %s -> %s", sessionID, target)
	// Store this mapping for future reference
	r.SessionStore.Set(sessionID, target)

	return target
}

// RouteByContext determines the target URL based on the request method and parameters
func (r *Router) RouteByContext(req JSONRPCRequest) string {
	switch req.Method {
	case "resources/read":
		uri, ok := req.Params["uri"].(string)
		if ok {
			r.Logger.Debug("Routing resources/read for URI: %s", uri)
			for _, rule := range r.Config.GetResourceRegexes() {
				if rule.Pattern.MatchString(uri) {
					r.Logger.Debug("Matched resource rule, using target: %s", rule.Target)
					return rule.Target
				}
			}
		}
	case "tools/call":
		name, ok := req.Params["name"].(string)
		if ok {
			r.Logger.Debug("Routing tools/call for tool: %s", name)
			for _, rule := range r.Config.GetToolRegexes() {
				if rule.Pattern.MatchString(name) {
					r.Logger.Debug("Matched tool rule, using target: %s", rule.Target)
					return rule.Target
				}
			}
		}
	}
	r.Logger.Debug("No specific routing rule matched, using default target")
	return r.Config.GetDefault()
}

// transformToolCall applies tool name transformation if needed
func (r *Router) transformToolCall(req JSONRPCRequest, targetURL string) {
	// Access fields directly
	if req.Method != "tools/call" {
		return
	}

	// Check if name exists in params
	name, ok := req.Params["name"].(string)
	if !ok {
		return
	}

	// Rest of function remains the same
	for _, mapping := range r.Config.GetToolMappings() {
		if mapping.OriginalName == name && mapping.Target == targetURL {
			// Transform the tool name
			req.Params["name"] = mapping.TargetName
			return
		}
	}
}
