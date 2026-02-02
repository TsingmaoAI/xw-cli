// Package client - instances.go implements model instance management operations.
//
// This file provides methods for managing runtime model instances including
// starting, stopping, listing, and monitoring model containers. These operations
// handle the lifecycle of running model services.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// RunModel starts a model instance.
//
// This method sends a request to start a model instance with the specified options.
//
// Parameters:
//   - opts: Runtime options for the model
//
// Returns:
//   - Response map with instance information
//   - error if the request fails
func (c *Client) RunModel(opts interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := c.doRequest("POST", "/api/runtime/start", opts, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// RunModelWithSSE starts a model instance with SSE streaming for progress updates.
//
// This method sends a request to start a model with real-time progress via SSE.
//
// Parameters:
//   - opts: Runtime options for the model
//   - progressCallback: Function called for each progress event
//
// Returns:
//   - error if the request fails
func (c *Client) RunModelWithSSE(opts interface{}, progressCallback func(string)) (map[string]interface{}, error) {
	return c.RunModelWithSSEContext(context.Background(), opts, progressCallback)
}

// RunModelWithSSEContext starts a model instance with SSE streaming and context support.
//
// This method sends a request to start a model with real-time progress via SSE,
// and supports cancellation via context.
//
// Parameters:
//   - ctx: Context for cancellation
//   - opts: Runtime options for the model
//   - progressCallback: Function called for each progress event
//
// Returns:
//   - error if the request fails
func (c *Client) RunModelWithSSEContext(ctx context.Context, opts interface{}, progressCallback func(string)) (map[string]interface{}, error) {
	data, err := json.Marshal(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/api/runtime/start"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() != nil {
			return nil, fmt.Errorf("operation cancelled")
		}
		return nil, fmt.Errorf("cannot connect to xw server at %s\n\nIs the server running? Start it with: xw serve", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respData, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	var instanceInfo map[string]interface{}

	// Read SSE stream
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled")
		default:
		}
		
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Try to parse as JSON to extract instance info
			var eventData map[string]interface{}
			if err := json.Unmarshal([]byte(data), &eventData); err == nil {
				// Save instance info if it contains instance_id or alias
				if _, hasInstanceID := eventData["instance_id"]; hasInstanceID {
					instanceInfo = eventData
				}
				if _, hasAlias := eventData["alias"]; hasAlias {
					instanceInfo = eventData
				}
			}

			// Check for done event
			if strings.Contains(data, `"status":"success"`) {
				return instanceInfo, nil
			}

			// Check for error event
			if strings.Contains(data, `"error"`) {
				var errData map[string]string
				if err := json.Unmarshal([]byte(data), &errData); err == nil {
					return nil, fmt.Errorf("%s", errData["error"])
				}
				return nil, fmt.Errorf("%s", data)
			}

			// Progress event
			if progressCallback != nil {
				progressCallback(data)
			}
		}
		// Ignore comments (lines starting with ':')
	}

	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("operation cancelled")
		}
		return nil, fmt.Errorf("error reading stream: %w", err)
	}

	return instanceInfo, nil
}

// ListInstances lists running model instances.
//
// Parameters:
//   - all: If true, includes stopped instances
//
// Returns:
//   - Slice of instance information maps
//   - error if the request fails
func (c *Client) ListInstances(all bool) ([]interface{}, error) {
	path := "/api/runtime/instances"
	if all {
		path += "?all=true"
	}

	var result struct {
		Instances []interface{} `json:"instances"`
	}

	if err := c.doRequest("GET", path, nil, &result); err != nil {
		return nil, err
	}

	return result.Instances, nil
}

// StopInstance stops a running model instance.
//
// Parameters:
//   - instanceID: The instance to stop
//   - force: If true, force stop even if in use
//
// Returns:
//   - error if the request fails
func (c *Client) StopInstance(instanceID string, force bool) error {
	reqBody := map[string]interface{}{
		"instance_id": instanceID,
		"force":       force,
	}

	var result map[string]interface{}
	if err := c.doRequest("POST", "/api/runtime/stop", reqBody, &result); err != nil {
		return err
	}

	return nil
}

// RemoveInstance removes a model instance.
//
// This method sends a request to the server to remove the specified instance.
// The instance must be stopped first unless force is true.
//
// Parameters:
//   - instanceID: ID of the instance to remove
//   - force: If true, removes the instance even if it's running
//
// Returns:
//   - Error if the request fails or the server returns an error
func (c *Client) RemoveInstance(instanceID string, force bool) error {
	reqBody := map[string]interface{}{
		"instance_id": instanceID,
		"force":       force,
	}

	var result map[string]interface{}
	if err := c.doRequest("POST", "/api/runtime/remove", reqBody, &result); err != nil {
		return err
	}

	return nil
}

// StopInstanceByAlias stops a model instance by its alias.
//
// This method sends a request to the server to stop the specified instance using alias.
//
// Parameters:
//   - alias: Alias of the instance to stop
//   - force: If true, forces stop even if instance is processing requests
//
// Returns:
//   - Error if the request fails or the server returns an error
func (c *Client) StopInstanceByAlias(alias string, force bool) error {
	reqBody := map[string]interface{}{
		"alias": alias,
		"force": force,
	}

	var result map[string]interface{}
	if err := c.doRequest("POST", "/api/runtime/stop", reqBody, &result); err != nil {
		return err
	}

	return nil
}

// RemoveInstanceByAlias removes a model instance by its alias.
//
// This method sends a request to the server to remove the specified instance using alias.
// The instance must be stopped first unless force is true.
//
// Parameters:
//   - alias: Alias of the instance to remove
//   - force: If true, removes the instance even if it's running
//
// Returns:
//   - Error if the request fails or the server returns an error
func (c *Client) RemoveInstanceByAlias(alias string, force bool) error {
	reqBody := map[string]interface{}{
		"alias": alias,
		"force": force,
	}

	var result map[string]interface{}
	if err := c.doRequest("POST", "/api/runtime/remove", reqBody, &result); err != nil {
		return err
	}

	return nil
}

// CheckInstanceReady checks if a model instance is ready to serve requests.
//
// This method verifies that the instance's endpoint is accessible and responding.
//
// Parameters:
//   - alias: The instance alias to check
//
// Returns:
//   - true if the instance is ready, false otherwise
//   - error if the request fails
func (c *Client) CheckInstanceReady(alias string) (bool, error) {
	url := fmt.Sprintf("%s/api/runtime/check-ready?alias=%s", c.baseURL, alias)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("cannot connect to xw server at %s", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}

	ready, ok := result["ready"].(bool)
	if !ok {
		return false, fmt.Errorf("invalid response format")
	}

	return ready, nil
}

// StreamInstanceLogs streams logs from a running instance.
//
// This method connects to the server to stream container logs.
// The logCallback function is called for each log line received.
//
// Parameters:
//   - alias: Alias of the instance to stream logs from
//   - follow: If true, stream logs in real-time; if false, return existing logs and exit
//   - logCallback: Function called for each log line
//
// Returns:
//   - Error if the request fails or the stream is interrupted
func (c *Client) StreamInstanceLogs(alias string, follow bool, logCallback func(string)) error {
	url := fmt.Sprintf("%s/api/runtime/logs?alias=%s&follow=%t", c.baseURL, alias, follow)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to xw server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to stream logs: %s", string(respData))
	}

	// Stream logs directly with small buffer for real-time output
	buf := make([]byte, 256) // Small buffer for low latency
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if logCallback != nil {
				logCallback(string(buf[:n]))
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("error reading log stream: %w", err)
		}
	}
}

