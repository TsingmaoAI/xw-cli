// Package client provides an HTTP client for communicating with the xw server.
//
// This package implements the client-side of the xw API, enabling CLI tools
// and other applications to interact with the xw server over HTTP. It provides:
//   - High-level methods for all API operations
//   - Automatic request/response serialization
//   - Error handling and status code processing
//   - Configurable timeouts and HTTP client settings
//
// The client handles all HTTP communication details, allowing callers to work
// with native Go types rather than raw HTTP requests and responses.
//
// Example usage:
//
//	client := client.NewClient("http://localhost:11581")
//	models, err := client.ListModels(device.ConfigKeyAscend910B, false)
//	if err != nil {
//	    log.Fatalf("Failed to list models: %v", err)
//	}
package client

import (
	"net/http"
)

// Client is the HTTP client for communicating with the xw server.
//
// The Client provides a high-level interface for all server API operations.
// It manages HTTP connections, request serialization, response parsing, and
// error handling. All methods are thread-safe and can be called concurrently.
//
// The client uses a configurable HTTP client with sensible defaults for
// timeouts and connection pooling.
type Client struct {
	// baseURL is the base URL of the xw server.
	// Format: "http://host:port" (e.g., "http://localhost:11581")
	baseURL string

	// httpClient is the underlying HTTP client used for requests.
	// Configured with appropriate timeouts and connection settings.
	httpClient *http.Client
}

// NewClient creates a new client instance configured to communicate with
// a specific xw server.
//
// The client is created with default HTTP settings including:
//   - 30-second timeout for all requests
//   - Automatic connection pooling and keep-alive
//   - Automatic retry of idempotent requests
//
// Parameters:
//   - baseURL: The base URL of the xw server (e.g., "http://localhost:11581")
//
// Returns:
//   - A pointer to a configured Client ready for use.
//
// Example:
//
//	client := client.NewClient("http://localhost:11581")
//	health, err := client.Health()
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 0, // No timeout for streaming operations (SSE)
		},
	}
}

// GetBaseURL returns the server's base URL.
//
// This returns the complete base URL of the xw server that the client
// is configured to communicate with. The URL can be used directly for
// API requests through the server's proxy.
//
// Returns:
//   - The base URL string (e.g., "http://localhost:11581", "http://192.168.1.100:11581")
func (c *Client) GetBaseURL() string {
	return c.baseURL
}

