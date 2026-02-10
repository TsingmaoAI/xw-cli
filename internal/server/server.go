// Package server provides the HTTP server implementation for the xw application.
//
// This package implements the server-side of the xw API, handling incoming
// HTTP requests from CLI clients and other API consumers. It provides:
//   - RESTful API endpoints for all operations
//   - Request routing and middleware
//   - Integration with model registry and device manager
//   - Graceful shutdown support
//   - Request logging and error handling
//
// The server is designed to run as a long-lived service process, either as
// a standalone daemon or as a systemd service. It maintains state including
// loaded models and device availability.
//
// Example usage:
//
//	cfg := config.NewDefaultConfig()
//	srv := server.NewServer(cfg)
//	if err := srv.Start(); err != nil {
//	    log.Fatalf("Server failed: %v", err)
//	}
package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/device"
	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/models"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
	"github.com/tsingmaoai/xw-cli/internal/server/handlers"
)

// Server is the HTTP server that handles API requests from clients.
//
// The Server manages the complete lifecycle of the xw service including:
//   - HTTP request handling and routing
//   - Model registry and device manager integration
//   - Graceful startup and shutdown
//   - Request logging and monitoring
//
// The server is thread-safe and can handle multiple concurrent requests.
// It uses standard Go HTTP server with configurable timeouts and limits.
type Server struct {
	// config holds the server configuration including host, port, and storage.
	config *config.Config
	
	// httpServer is the underlying HTTP server instance.
	httpServer *http.Server
	
	// modelRegistry manages the catalog of available models.
	modelRegistry *models.Registry
	
	// deviceManager handles device detection and availability.
	deviceManager *device.Manager
	
	// runtimeManager manages running model instances
	runtimeManager *runtime.Manager
	
	// version is the server version string.
	version string
	
	// buildTime is the timestamp when the server was built.
	buildTime string
}

// NewServer creates and initializes a new server instance.
//
// The server is created with the provided configuration and initializes all
// required subsystems including:
//   - Model registry with default models
//   - Device manager with hardware detection
//   - Version information for diagnostics
//
// The server is ready to start after creation but is not yet listening for
// connections. Call Start() to begin accepting requests.
//
// Parameters:
//   - cfg: The configuration for the server
//   - runtimeMgr: The runtime manager
//   - version: Server version string
//
// Returns:
//   - A pointer to a fully initialized Server ready to start.
//
// Example:
//
//	cfg := config.NewDefaultConfig()
//	srv := server.NewServer(cfg, runtimeMgr, "1.0.0")
//	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
//	    log.Fatalf("Server error: %v", err)
//	}
func NewServer(cfg *config.Config, runtimeMgr *runtime.Manager, version string) *Server {
	return &Server{
		config:         cfg,
		modelRegistry:  models.GetDefaultRegistry(),
		deviceManager:  device.NewManager(),
		runtimeManager: runtimeMgr,
		version:        version,
		buildTime:      time.Now().Format(time.RFC3339),
	}
}

// Start starts the HTTP server and begins accepting connections.
//
// This method configures the HTTP server with all routes and middleware,
// then starts listening on the configured host and port. The method blocks
// until the server is shut down via Stop() or encounters a fatal error.
//
// The server registers the following endpoints:
//   - GET  /api/health       - Health check
//   - GET  /api/version      - Version information
//   - POST /api/models/list  - List available models
//   - POST /api/models/pull  - Pull a model
//   - POST /api/run          - Execute a model
//
// All requests are logged through the logging middleware.
//
// Returns:
//   - nil if the server shuts down gracefully
//   - http.ErrServerClosed after graceful shutdown
//   - error if the server fails to start or encounters a fatal error
//
// Example:
//
//	go func() {
//	    if err := srv.Start(); err != nil && err != http.ErrServerClosed {
//	        log.Fatalf("Server failed: %v", err)
//	    }
//	}()
//	// Wait for signal to shutdown...
//	srv.Stop(context.Background())
func (s *Server) Start() error {
	// Create handler instance with all dependencies
	h := handlers.NewHandler(
		s.config,
		s.modelRegistry,
		s.deviceManager,
		s.runtimeManager,
		s.version,
		s.buildTime,
	)

	// Create proxy handler for inference service proxying
	proxyHandler := handlers.NewProxyHandler(h)

	mux := http.NewServeMux()

	// Register routes with handlers from the handlers package
	mux.HandleFunc("/api/health", h.Health)
	mux.HandleFunc("/api/version", h.Version)
	mux.HandleFunc("/api/models/list", h.ListModels)
	mux.HandleFunc("/api/models/downloaded", h.ListDownloadedModels)
	mux.HandleFunc("/api/models/show", h.ShowModel)
	mux.HandleFunc("/api/models/pull", h.PullModel)
	
	// Device management endpoints
	mux.HandleFunc("/api/devices/list", h.ListDevices)
	mux.HandleFunc("/api/devices/supported", h.GetSupportedDevices)
	
	// Configuration management endpoints
	mux.HandleFunc("/api/config/info", h.ConfigInfo)
	mux.HandleFunc("/api/config/set", h.ConfigSet)
	mux.HandleFunc("/api/config/get", h.ConfigGet)
	
	// Runtime management endpoints
	mux.HandleFunc("/api/runtime/start", h.StartModel)
	mux.HandleFunc("/api/runtime/instances", h.ListInstances)
	mux.HandleFunc("/api/runtime/check-ready", h.CheckInstanceReady)
	mux.HandleFunc("/api/runtime/stop", h.StopInstance)
	mux.HandleFunc("/api/runtime/remove", h.RemoveInstance)
	mux.HandleFunc("/api/runtime/logs", h.StreamLogs)
	
	// OpenAI-compatible API endpoints
	// These endpoints follow the OpenAI API specification and are proxied
	// to running model instances based on the "model" field in request body
	mux.HandleFunc("/v1/chat/completions", proxyHandler.ProxyRequest)
	mux.HandleFunc("/v1/completions", proxyHandler.ProxyRequest)
	mux.HandleFunc("/v1/embeddings", proxyHandler.ProxyRequest)
	
	// Health check for proxy
	mux.HandleFunc("/v1/health", proxyHandler.HealthCheck)

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.loggingMiddleware(mux),
		// No timeouts for streaming operations (model downloads)
		// ReadTimeout:  0,  // No read timeout
		// WriteTimeout: 0,  // No write timeout for SSE streaming
		IdleTimeout: 120 * time.Second,
	}

	logger.Info("Starting xw server on %s", addr)
	return s.httpServer.ListenAndServe()
}

// Stop gracefully shuts down the server without interrupting active connections.
//
// This method initiates a graceful shutdown of the HTTP server. It:
//   - Stops accepting new connections
//   - Waits for active requests to complete
//   - Respects the timeout in the provided context
//   - Cleans up resources
//
// If the context expires before all connections close, the server is forcefully
// terminated.
//
// Parameters:
//   - ctx: Context with timeout for graceful shutdown
//
// Returns:
//   - nil if shutdown completes within the timeout
//   - error if shutdown fails or times out
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := srv.Stop(ctx); err != nil {
//	    logger.Error("Shutdown error: %v", err)
//	}
func (s *Server) Stop(ctx context.Context) error {
	logger.Info("Shutting down server...")
	return s.httpServer.Shutdown(ctx)
}

// loggingMiddleware wraps an HTTP handler to log all requests.
//
// This middleware logs each incoming request with:
//   - Client IP address
//   - HTTP method and path
//   - Request processing duration
//
// The logging helps with debugging, monitoring, and auditing of API usage.
//
// Parameters:
//   - next: The next handler in the chain to call after logging
//
// Returns:
//   - An http.Handler that logs requests and calls the next handler
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		logger.Info("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		logger.Debug("Completed in %v", time.Since(start))
	})
}
