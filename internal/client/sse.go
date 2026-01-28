// Package client - sse.go implements SSE (Server-Sent Events) support
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tsingmao/xw/internal/api"
)

// SSEMessage represents a parsed SSE message
type SSEMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
	Path    string `json:"path,omitempty"`
}

// pullWithSSE performs a model pull with SSE streaming
func (c *Client) pullWithSSE(model, version string, progressCallback func(string)) (*api.PullResponse, error) {
	req := api.PullRequest{
		Model:   model,
		Version: version,
	}

	// Marshal request body
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := c.baseURL + "/api/models/pull"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to xw server at %s\n\nIs the server running? Start it with: xw serve", c.baseURL)
	}
	defer resp.Body.Close()

	// Check for error response
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error: status %d", resp.StatusCode)
	}

	// Read SSE stream
	scanner := bufio.NewScanner(resp.Body)
	var finalResponse *api.PullResponse

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {...}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Parse JSON message
		data := strings.TrimPrefix(line, "data: ")
		var msg SSEMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			// Ignore parse errors, continue reading
			continue
		}

		// Handle different message types
		switch msg.Type {
		case "status", "progress":
			if progressCallback != nil {
				progressCallback(msg.Message)
			}
		case "heartbeat":
			// Heartbeat to keep connection alive, don't display
			// Just continue reading
		case "error":
			return nil, fmt.Errorf("download failed: %s", msg.Message)
		case "complete":
			finalResponse = &api.PullResponse{
				Status:   msg.Status,
				Progress: 100,
				Message:  msg.Message,
			}
		case "end":
			// Explicit end signal, break out of loop
			if finalResponse == nil {
				return nil, fmt.Errorf("received end signal but no completion response")
			}
			return finalResponse, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	if finalResponse == nil {
		return nil, fmt.Errorf("download incomplete: no final response received")
	}

	return finalResponse, nil
}

