// Package client - sse.go implements SSE (Server-Sent Events) support.
//
// This file provides SSE-based communication for streaming operations like
// model downloads, where the server needs to send real-time progress updates
// to the client.
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// SSEMessage represents a parsed Server-Sent Events message.
//
// SSE messages are sent from the server during long-running operations
// to provide real-time status updates and progress information.
//
// Message types:
//   - "status": General status update
//   - "progress": Download progress update
//   - "heartbeat": Keep-alive signal
//   - "error": Error occurred during operation
//   - "complete": Operation completed successfully
//   - "end": Stream termination signal
type SSEMessage struct {
	// Type indicates the message category
	Type string `json:"type"`

	// Message contains the human-readable message content
	Message string `json:"message"`

	// Status indicates the final status (used with "complete" type)
	Status string `json:"status,omitempty"`

	// Path contains the file or resource path (if applicable)
	Path string `json:"path,omitempty"`
}

// pullWithSSE performs a model pull operation with Server-Sent Events streaming.
//
// This method downloads a model from the registry while providing real-time
// progress updates via SSE. The server streams status messages throughout the
// download process, allowing the client to display progress to the user.
//
// The SSE stream provides:
//   - Real-time download progress
//   - Status updates at each stage
//   - Heartbeat signals to keep connection alive
//   - Error notifications if download fails
//   - Completion signal with final status
//
// Parameters:
//   - model: Model identifier (e.g., "qwen2-7b")
//   - version: Model version (empty string for latest)
//   - progressCallback: Optional callback function for progress updates
//
// Returns:
//   - PullResponse with final status and completion message
//   - Error if connection fails, download fails, or stream is invalid
//
// Example:
//
//	resp, err := client.pullWithSSE("qwen2-7b", "", func(msg string) {
//	    fmt.Println("Progress:", msg)
//	})
func (c *Client) pullWithSSE(model, version string, progressCallback func(string)) (*api.PullResponse, error) {
	// Construct pull request
	req := api.PullRequest{
		Model:   model,
		Version: version,
	}

	// Serialize request body
	reqBody, err := json.Marshal(req)
	if err != nil {
		logger.Error("Failed to marshal pull request for model %s: %v", model, err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with SSE headers
	url := c.baseURL + "/api/models/pull"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
	if err != nil {
		logger.Error("Failed to create HTTP request for %s: %v", url, err)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers for SSE communication
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	logger.Debug("Initiating SSE pull for model %s (version: %s)", model, version)

	// Execute HTTP request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("Failed to connect to server at %s: %v", c.baseURL, err)
		return nil, fmt.Errorf("cannot connect to xw server at %s\n\nIs the server running? Start it with: xw serve", c.baseURL)
	}
	defer resp.Body.Close()

	// Check for HTTP error status
	if resp.StatusCode >= 400 {
		logger.Error("Server returned error status %d for model %s", resp.StatusCode, model)
		return nil, fmt.Errorf("server error: status %d", resp.StatusCode)
	}

	logger.Debug("SSE connection established, reading stream for model %s", model)

	// Process SSE stream
	return c.processSSEStream(resp.Body, progressCallback)
}

// processSSEStream reads and processes the SSE event stream.
//
// This method parses SSE messages line by line, handling different message
// types appropriately. It continues until receiving an "end" signal or
// encountering an error.
//
// Parameters:
//   - body: Response body reader (SSE stream)
//   - progressCallback: Optional callback for progress messages
//
// Returns:
//   - PullResponse with final result
//   - Error if stream parsing fails or error message received
func (c *Client) processSSEStream(body interface{ Read([]byte) (int, error) }, progressCallback func(string)) (*api.PullResponse, error) {
	scanner := bufio.NewScanner(body)
	var finalResponse *api.PullResponse

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {...}" - skip non-data lines
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Extract and parse JSON payload
		data := strings.TrimPrefix(line, "data: ")
		var msg SSEMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			// Ignore malformed messages and continue reading
			logger.Warn("Failed to parse SSE message: %v (data: %s)", err, data)
			continue
		}

		// Handle message based on type
		switch msg.Type {
		case "status", "progress":
			// Status and progress updates - forward to callback
			if progressCallback != nil {
				progressCallback(msg.Message)
			}

		case "heartbeat":
			// Heartbeat signal to keep connection alive
			// No action needed, just continue reading
			logger.Debug("Received heartbeat signal")

		case "error":
			// Error occurred during download
			logger.Error("Download failed: %s", msg.Message)
			return nil, fmt.Errorf("download failed: %s", msg.Message)

		case "complete":
			// Download completed successfully
			finalResponse = &api.PullResponse{
				Status:   msg.Status,
				Progress: 100,
				Message:  msg.Message,
			}

		case "end":
			// Stream termination signal
			logger.Debug("Received end signal, closing SSE stream")
			if finalResponse == nil {
				logger.Error("Received end signal without completion response")
				return nil, fmt.Errorf("received end signal but no completion response")
			}
			return finalResponse, nil

		default:
			// Unknown message type - log but continue
			logger.Warn("Unknown SSE message type: %s", msg.Type)
		}
	}

	// Check for scanner errors
	if err := scanner.Err(); err != nil {
		logger.Error("Error reading SSE stream: %v", err)
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	// Stream ended without explicit "end" signal
	if finalResponse == nil {
		logger.Error("SSE stream ended without completion response")
		return nil, fmt.Errorf("download incomplete: no final response received")
	}

	return finalResponse, nil
}

