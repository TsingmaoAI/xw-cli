// Package handlers - version.go implements the version information endpoint.
package handlers

import (
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/api"
)

// Version handles requests for server version information.
//
// This endpoint returns detailed version metadata about the server build,
// including the semantic version, build timestamp, and git commit hash.
// This information is useful for:
//   - Debugging and troubleshooting
//   - Verifying deployed versions
//   - Checking API compatibility
//   - Support and diagnostics
//
// The version information is embedded at build time using ldflags, ensuring
// it accurately reflects the deployed binary. Clients can use this to verify
// server version compatibility and report issues with specific builds.
//
// HTTP Method: GET
// Endpoint: /api/version
//
// Response: 200 OK with VersionResponse JSON
//
// Example response:
//
//	{
//	  "version": "1.0.0",
//	  "build_time": "2026-01-26T10:30:00Z",
//	  "git_commit": "a1b2c3d4"
//	}
//
// Example usage:
//
//	curl http://localhost:11581/api/version
//	xw version --server http://localhost:11581
func (h *Handler) Version(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method - only GET is allowed
	if r.Method != http.MethodGet {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Construct version response with build metadata
	resp := api.VersionResponse{
		Version:   h.version,
		BuildTime: h.buildTime,
		GitCommit: h.gitCommit,
	}

	// Return success response
	h.WriteJSON(w, resp, http.StatusOK)
}

