// Main entry point for the MCP router proxy
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mclenhard/catie-mcp/pkg/config"
	"github.com/mclenhard/catie-mcp/pkg/router"
	"github.com/mclenhard/catie-mcp/pkg/ui"
)

var (
	configPath      = flag.String("config", "router_config.yaml", "Path to configuration file")
	serverPort      = flag.String("port", ":80", "Server port")
	configInterval  = flag.Duration("config-interval", 30*time.Second, "Configuration reload interval")
	shutdownTimeout = flag.Duration("shutdown-timeout", 30*time.Second, "Graceful shutdown timeout")
)

func main() {
	flag.Parse()

	// Initialize configuration
	cfg := config.New(*configPath)

	// Start config watcher in background
	go cfg.WatchConfig(*configPath, *configInterval)

	// Initialize UI
	uiHandler := ui.New(cfg)

	// Initialize router with UI
	r := router.New(cfg, uiHandler)

	// Set up HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", r.HandleMCPRequest)
	mux.HandleFunc("/health", handleHealth)

	// Register UI handlers with our mux
	uiHandler.RegisterHandlers(mux)

	server := &http.Server{
		Addr:    *serverPort,
		Handler: mux,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("MCP router proxy listening on %s", *serverPort)
		log.Printf("Stats UI available at http://localhost%s/stats", *serverPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Set up graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer cancel()

	// Attempt graceful shutdown
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited gracefully")
}

// handleHealth is a simple health check endpoint
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
