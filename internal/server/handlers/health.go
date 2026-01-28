// Package handlers - health.go implements the health check endpoint.
package handlers

import (
	"net/http"

	"github.com/tsingmao/xw/internal/api"
)

// Health handles health check requests from monitoring systems and load balancers.
//
// This endpoint provides a simple way to verify that the server is running and
// able to respond to requests. It returns a 200 OK status with a JSON response
// indicating the server is healthy.
//
// This is typically used by:
//   - Load balancers for health probing
//   - Monitoring systems like Prometheus or Datadog
//   - Kubernetes liveness/readiness probes
//   - Manual server status checks
//
// The health check is intentionally lightweight and does not perform deep
// validation of subsystems. For more detailed diagnostics, use the /api/version
// endpoint or implement a dedicated diagnostics endpoint.
//
// HTTP Method: GET
// Endpoint: /api/health
//
// Response: 200 OK with HealthResponse JSON
//
// Example response:
//
//	{
//	  "status": "healthy",
//	  "message": "Server is running"
//	}
//
// Example usage:
//
//	curl http://localhost:11581/api/health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method - only GET is allowed
	if r.Method != http.MethodGet {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Construct health response
	resp := api.HealthResponse{
		Status:  "healthy",
		Message: "Server is running",
	}

	// Return success response
	h.WriteJSON(w, resp, http.StatusOK)
}

