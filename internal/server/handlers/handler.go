// Package handlers provides HTTP request handlers for the xw server API.
//
// This package contains all HTTP endpoint handlers organized by functionality.
// Each handler is responsible for processing specific API requests, validating
// input, coordinating with business logic layers, and returning appropriate
// responses.
//
// Handler Structure:
//   - Handler: Core structure containing dependencies (registry, device manager, etc.)
//   - Individual handler functions: Implement specific API endpoints
//   - Helper methods: Common functionality like JSON serialization and error handling
//
// All handlers follow RESTful conventions and return JSON responses with
// appropriate HTTP status codes. Error responses use a standardized format
// for consistency across the API.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/device"
	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/models"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// Handler encapsulates all dependencies required by API handlers.
//
// This structure provides a clean way to pass server state and dependencies
// to individual handler functions without using global variables. It enables
// better testing, dependency injection, and separation of concerns.
//
// All HTTP handler methods are attached to this struct, allowing them to
// access the model registry, device manager, and other shared resources.
type Handler struct {
	// config holds the server configuration including storage paths and ports.
	config *config.Config

	// modelRegistry manages the catalog of available AI models.
	modelRegistry *models.Registry

	// deviceManager handles AI chip detection and availability.
	deviceManager *device.Manager

	// runtimeManager manages running model instances
	runtimeManager *runtime.Manager

	// loadModelsFunc is a callback to reload models from configuration
	loadModelsFunc func(string) error

	// version is the server version string for diagnostics.
	version string

	// buildTime is the timestamp when the server was built.
	buildTime string
}

// NewHandler creates a new Handler instance with the provided dependencies.
//
// This constructor initializes the handler with all required server components.
// The handler is then ready to process API requests by calling its various
// handler methods.
//
// Parameters:
//   - cfg: Server configuration
//   - registry: Model registry for querying available models
//   - deviceMgr: Device manager for hardware detection
//   - version: Server version string
//   - buildTime: Build timestamp
//   - gitCommit: Git commit hash
//
// Returns:
//   - A pointer to a fully initialized Handler instance.
//
// Example:
//
//	handler := handlers.NewHandler(cfg, registry, deviceMgr, "1.0.0", "2024-01-01", "abc123")
//	http.HandleFunc("/api/health", handler.Health)
func NewHandler(
	cfg *config.Config,
	registry *models.Registry,
	deviceMgr *device.Manager,
	runtimeMgr *runtime.Manager,
	loadModelsFunc func(string) error,
	version, buildTime string,
) *Handler {
	return &Handler{
		config:         cfg,
		modelRegistry:  registry,
		deviceManager:  deviceMgr,
		runtimeManager: runtimeMgr,
		loadModelsFunc: loadModelsFunc,
		version:        version,
		buildTime:      buildTime,
	}
}

// WriteJSON writes a JSON response to the HTTP client.
//
// This is a shared helper method used by all handlers to serialize responses
// to JSON format. It sets the appropriate Content-Type header, writes the
// status code, and encodes the data. If encoding fails, an error is logged.
//
// Parameters:
//   - w: The HTTP response writer
//   - data: The data structure to serialize (must be JSON-serializable)
//   - statusCode: The HTTP status code to send (e.g., 200, 201, 400)
//
// Example:
//
//	handler.WriteJSON(w, response, http.StatusOK)
func (h *Handler) WriteJSON(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("Failed to encode JSON response: %v", err)
	}
}

// WriteError writes a standardized error response to the HTTP client.
//
// This method ensures all API errors follow the same format, providing
// consistency for client applications. It creates an ErrorResponse with
// the provided message and status code, then serializes it to JSON.
//
// All error responses should use this method rather than writing errors
// directly to ensure proper formatting and logging.
//
// Parameters:
//   - w: The HTTP response writer
//   - message: Human-readable error message
//   - statusCode: HTTP error status code (4xx for client errors, 5xx for server errors)
//
// Example:
//
//	handler.WriteError(w, "Model not found", http.StatusNotFound)
func (h *Handler) WriteError(w http.ResponseWriter, message string, statusCode int) {
	resp := api.ErrorResponse{
		Error: message,
		Code:  fmt.Sprintf("%d", statusCode),
	}

	h.WriteJSON(w, resp, statusCode)
}

