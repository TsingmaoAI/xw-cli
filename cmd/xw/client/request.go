// Package client - request.go implements low-level HTTP request handling.
//
// This file provides the core HTTP request/response logic used by all
// API methods in the client. It handles request serialization, response
// parsing, error handling, and status code validation.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/api"
)

// doRequest performs an HTTP request to the server.
//
// This is an internal helper method that handles the low-level details of
// HTTP communication including:
//   - Request serialization to JSON
//   - HTTP request creation and execution
//   - Response parsing and error handling
//   - Status code validation
//
// The method is used by all public API methods and is not intended for
// direct external use.
//
// Parameters:
//   - method: HTTP method (GET, POST, etc.)
//   - path: API endpoint path (e.g., "/api/models/list")
//   - reqBody: Request body to serialize (nil for no body)
//   - respBody: Pointer to struct for response deserialization (nil to ignore)
//
// Returns:
//   - nil if the request succeeds
//   - error if the request fails, times out, or server returns an error
func (c *Client) doRequest(method, path string, reqBody, respBody interface{}) error {
	url := c.baseURL + path

	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to xw server at %s\n\nIs the server running? Start it with: xw serve", c.baseURL)
	}
	defer resp.Body.Close()

	// Read response body
	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for error responses
	if resp.StatusCode >= 400 {
		var errResp api.ErrorResponse
		if err := json.Unmarshal(respData, &errResp); err == nil {
			return fmt.Errorf("server error: %s", errResp.Error)
		}
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	// Unmarshal success response
	if respBody != nil {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

