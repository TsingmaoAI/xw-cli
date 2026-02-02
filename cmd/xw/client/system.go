// Package client - system.go implements system information operations.
//
// This file provides methods for querying server status, version information,
// and health checks. These operations help monitor and validate the server state.
package client

import (
	"github.com/tsingmaoai/xw-cli/internal/api"
)

// Version retrieves version and build information from the server.
//
// This method queries the server for its version, build time, and git commit.
// Useful for debugging, compatibility checking, and displaying in help text.
//
// Returns:
//   - A pointer to VersionResponse with server version details
//   - An error if the request fails
//
// Example:
//
//	ver, err := client.Version()
//	if err != nil {
//	    log.Fatalf("Failed to get version: %v", err)
//	}
//	fmt.Printf("Server v%s (built %s, commit %s)\n",
//	    ver.Version, ver.BuildTime, ver.GitCommit)
func (c *Client) Version() (*api.VersionResponse, error) {
	var resp api.VersionResponse
	if err := c.doRequest("GET", "/api/version", nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Health checks the server's health and readiness status.
//
// This method performs a health check to verify that the server is running
// and ready to handle requests. It's commonly used by monitoring systems,
// load balancers, and before attempting other operations.
//
// Returns:
//   - A pointer to HealthResponse with status information
//   - An error if the request fails or the server is unhealthy
//
// Example:
//
//	health, err := client.Health()
//	if err != nil {
//	    log.Fatalf("Server is unhealthy: %v", err)
//	}
//	fmt.Printf("Server status: %s\n", health.Status)
func (c *Client) Health() (*api.HealthResponse, error) {
	var resp api.HealthResponse
	if err := c.doRequest("GET", "/api/health", nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

