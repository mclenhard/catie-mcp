// Package ui provides a web interface for monitoring the MCP router proxy
package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/mclenhard/catie-mcp/pkg/config"
	"github.com/mclenhard/catie-mcp/pkg/logger"
)

// Stats tracks various metrics about the router proxy
type Stats struct {
	TotalRequests      int64            `json:"totalRequests"`
	RequestsByMethod   map[string]int64 `json:"requestsByMethod"`
	RequestsByEndpoint map[string]int64 `json:"requestsByEndpoint"`
	ResponseTimes      map[string][]int `json:"responseTimes"` // in milliseconds
	ErrorCount         int64            `json:"errorCount"`
	StartTime          time.Time        `json:"startTime"`
	mu                 sync.RWMutex
}

// UI handles the web interface for the router proxy
type UI struct {
	Stats    *Stats
	template *template.Template
	username string
	password string
	logger   *logger.Logger
}

// New creates a new UI instance
func New(config *config.Config) *UI {
	tmpl := template.Must(template.New("stats").Parse(statsTemplate))

	return &UI{
		Stats: &Stats{
			RequestsByMethod:   make(map[string]int64),
			RequestsByEndpoint: make(map[string]int64),
			ResponseTimes:      make(map[string][]int),
			StartTime:          time.Now(),
		},
		template: tmpl,
		username: config.RouteConfig.UI.Username,
		password: config.RouteConfig.UI.Password,
		logger:   logger.New(logger.Info), // Initialize with Info level
	}
}

// RecordRequest records statistics for a request
func (s *Stats) RecordRequest(method, endpoint string, responseTime time.Duration, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TotalRequests++
	s.RequestsByMethod[method]++
	s.RequestsByEndpoint[endpoint]++

	// Store response time (convert to milliseconds)
	respTimeMs := int(responseTime.Milliseconds())
	s.ResponseTimes[method] = append(s.ResponseTimes[method], respTimeMs)

	// Limit the number of stored response times to prevent memory issues
	if len(s.ResponseTimes[method]) > 1000 {
		s.ResponseTimes[method] = s.ResponseTimes[method][len(s.ResponseTimes[method])-1000:]
	}

	if isError {
		s.ErrorCount++
	}
}

// GetStats returns a copy of the current stats
func (s *Stats) GetStats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a deep copy to avoid race conditions
	statsCopy := Stats{
		TotalRequests:      s.TotalRequests,
		RequestsByMethod:   make(map[string]int64),
		RequestsByEndpoint: make(map[string]int64),
		ResponseTimes:      make(map[string][]int),
		ErrorCount:         s.ErrorCount,
		StartTime:          s.StartTime,

	}

	for k, v := range s.RequestsByMethod {
		statsCopy.RequestsByMethod[k] = v
	}

	for k, v := range s.RequestsByEndpoint {
		statsCopy.RequestsByEndpoint[k] = v
	}

	for k, v := range s.ResponseTimes {
		statsCopy.ResponseTimes[k] = make([]int, len(v))
		copy(statsCopy.ResponseTimes[k], v)
	}

	return statsCopy
}

// HandleStats serves the stats page
func (ui *UI) HandleStats(w http.ResponseWriter, r *http.Request) {
	stats := ui.Stats.GetStats()

	// Calculate uptime
	uptime := time.Since(stats.StartTime).String()

	// Calculate average response times
	avgResponseTimes := make(map[string]int)
	for method, times := range stats.ResponseTimes {
		if len(times) == 0 {
			continue
		}

		sum := 0
		for _, t := range times {
			sum += t
		}
		avgResponseTimes[method] = sum / len(times)
	}

	data := struct {
		Stats            Stats
		Uptime           string
		AvgResponseTimes map[string]int
	}{
		Stats:            stats,
		Uptime:           uptime,
		AvgResponseTimes: avgResponseTimes,
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	ui.template.Execute(w, data)
}

// RegisterHandlers registers the UI handlers with the provided mux
func (ui *UI) RegisterHandlers(mux *http.ServeMux) {
	fmt.Println("Registering UI handlers with provided mux")
	mux.HandleFunc("/stats", ui.basicAuth(ui.HandleStats))
	mux.HandleFunc("/metrics", ui.basicAuth(ui.HandleMetrics))
	fmt.Println("UI handlers registered for /stats and /metrics")
}

// basicAuth wraps an http.HandlerFunc with basic authentication
func (ui *UI) basicAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ui.logger.Debug("Received request for %s from %s", r.URL.Path, r.RemoteAddr)

		// Skip auth if credentials aren't configured
		if ui.username == "" && ui.password == "" {
			ui.logger.Info("No auth credentials configured, skipping authentication for %s", r.URL.Path)
			handler(w, r)
			return
		}

		ui.logger.Debug("Auth required for %s - username: '%s', password: [hidden]", r.URL.Path, ui.username)
		user, pass, ok := r.BasicAuth()

		if !ok {
			ui.logger.Warn("No auth credentials provided for %s from %s", r.URL.Path, r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Basic realm="MCP Router Stats"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorized - No credentials provided"))
			return
		}

		ui.logger.Debug("Auth attempt for %s - provided username: '%s'", r.URL.Path, user)

		if user != ui.username || pass != ui.password {
			ui.logger.Warn("Invalid credentials for %s from %s (user: %s)", r.URL.Path, r.RemoteAddr, user)
			w.Header().Set("WWW-Authenticate", `Basic realm="MCP Router Stats"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorized - Invalid credentials"))
			return
		}

		ui.logger.Info("Authorized access to %s from %s (user: %s)", r.URL.Path, r.RemoteAddr, user)
		handler(w, r)
	}
}

// HandleMetrics serves Prometheus-compatible metrics
func (ui *UI) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	stats := ui.Stats.GetStats()

	w.Header().Set("Content-Type", "text/plain")

	// Basic Prometheus-style metrics
	w.Write([]byte("# HELP mcp_router_requests_total Total number of requests processed\n"))
	w.Write([]byte("# TYPE mcp_router_requests_total counter\n"))
	w.Write([]byte("mcp_router_requests_total " + strconv.FormatInt(stats.TotalRequests, 10) + "\n\n"))

	w.Write([]byte("# HELP mcp_router_errors_total Total number of request errors\n"))
	w.Write([]byte("# TYPE mcp_router_errors_total counter\n"))
	w.Write([]byte("mcp_router_errors_total " + strconv.FormatInt(stats.ErrorCount, 10) + "\n\n"))

	// Method-specific metrics
	w.Write([]byte("# HELP mcp_router_requests_by_method Number of requests by method\n"))
	w.Write([]byte("# TYPE mcp_router_requests_by_method counter\n"))
	for method, count := range stats.RequestsByMethod {
		w.Write([]byte("mcp_router_requests_by_method{method=\"" + method + "\"} " + strconv.FormatInt(count, 10) + "\n"))
	}

	// Endpoint-specific metrics
	w.Write([]byte("\n# HELP mcp_router_requests_by_endpoint Number of requests by endpoint\n"))
	w.Write([]byte("# TYPE mcp_router_requests_by_endpoint counter\n"))
	for endpoint, count := range stats.RequestsByEndpoint {
		w.Write([]byte("mcp_router_requests_by_endpoint{endpoint=\"" + endpoint + "\"} " + strconv.FormatInt(count, 10) + "\n"))
	}

	// Response time metrics
	w.Write([]byte("\n# HELP mcp_router_response_time_ms Average response time in milliseconds\n"))
	w.Write([]byte("# TYPE mcp_router_response_time_ms gauge\n"))
	for method, times := range stats.ResponseTimes {
		if len(times) == 0 {
			continue
		}

		sum := 0
		for _, t := range times {
			sum += t
		}
		avg := sum / len(times)
		w.Write([]byte("mcp_router_response_time_ms{method=\"" + method + "\"} " + strconv.Itoa(avg) + "\n"))
	}

	// Uptime metric
	uptime := time.Since(stats.StartTime).Seconds()
	w.Write([]byte("\n# HELP mcp_router_uptime_seconds Time since the router started in seconds\n"))
	w.Write([]byte("# TYPE mcp_router_uptime_seconds counter\n"))
	w.Write([]byte("mcp_router_uptime_seconds " + strconv.FormatFloat(uptime, 'f', 1, 64) + "\n"))
}

// HTML template for the stats page
const statsTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>Request Statistics</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: Arial, sans-serif;
            line-height: 1.6;
            margin: 0;
            padding: 20px;
            color: #333;
        }
        h1 {
            color: #2c3e50;
            border-bottom: 2px solid #eee;
            padding-bottom: 10px;
        }
        .stats-container {
            display: flex;
            flex-wrap: wrap;
            gap: 20px;
        }
        .stats-card {
            background: #f9f9f9;
            border-radius: 5px;
            padding: 15px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            flex: 1;
            min-width: 300px;
        }
        .stats-card h2 {
            margin-top: 0;
            color: #3498db;
        }
        table {
            width: 100%;
            border-collapse: collapse;
        }
        table, th, td {
            border: 1px solid #ddd;
        }
        th, td {
            padding: 8px;
            text-align: left;
        }
        th {
            background-color: #f2f2f2;
        }
        .refresh-btn {
            background: #3498db;
            color: white;
            border: none;
            padding: 10px 15px;
            border-radius: 4px;
            cursor: pointer;
            margin-bottom: 20px;
        }
        .refresh-btn:hover {
            background: #2980b9;
        }
    </style>
</head>
<body>
    <h1>MCP Router Proxy Statistics</h1>
    
    <button class="refresh-btn" onclick="location.reload()">Refresh Stats</button>
    
    <div class="stats-container">
        <div class="stats-card">
            <h2>General Stats</h2>
            <table>
                <tr>
                    <td>Total Requests</td>
                    <td>{{.Stats.TotalRequests}}</td>
                </tr>
                <tr>
                    <td>Error Count</td>
                    <td>{{.Stats.ErrorCount}}</td>
                </tr>
                <tr>
                    <td>Uptime</td>
                    <td>{{.Uptime}}</td>
                </tr>
            </table>
        </div>
        
        <div class="stats-card">
            <h2>Requests by Method</h2>
            <table>
                <tr>
                    <th>Method</th>
                    <th>Count</th>
                    <th>Avg Response Time (ms)</th>
                </tr>
                {{range $method, $count := .Stats.RequestsByMethod}}
                <tr>
                    <td>{{$method}}</td>
                    <td>{{$count}}</td>
                    <td>{{index $.AvgResponseTimes $method}}</td>
                </tr>
                {{end}}
            </table>
        </div>
        
        <div class="stats-card">
            <h2>Requests by Endpoint</h2>
            <table>
                <tr>
                    <th>Endpoint</th>
                    <th>Count</th>
                </tr>
                {{range $endpoint, $count := .Stats.RequestsByEndpoint}}
                <tr>
                    <td>{{$endpoint}}</td>
                    <td>{{$count}}</td>
                </tr>
                {{end}}
            </table>
        </div>
    </div>
    
    <script>
        // Auto-refresh every 30 seconds
        setTimeout(function() {
            location.reload();
        }, 30000);
    </script>
</body>
</html>
`
