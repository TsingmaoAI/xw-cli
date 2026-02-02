// Package client - models.go implements model management operations.
//
// This file provides methods for querying, listing, downloading, and managing
// AI models in the xw system. These operations interact with the server's
// model registry and local model storage.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/api"
)

// ListModels retrieves a list of available models from the server.
//
// This method queries the server for models, optionally filtering by device
// compatibility. It's commonly used to display available models to users or
// validate model availability before operations.
//
// Parameters:
//   - deviceType: Filter models by device type (use DeviceTypeAll for no filter)
//   - showAll: If true, returns all models regardless of device type
//
// Returns:
//   - A slice of Model structs matching the filter criteria
//   - An error if the request fails or the server returns an error
//
// Example:
//
//	// List all models compatible with Ascend 910B devices
//	models, err := client.ListModels(device.ConfigKeyAscend910B, false)
//	if err != nil {
//	    log.Fatalf("Failed to list models: %v", err)
//	}
//	for _, model := range models {
//	    fmt.Printf("%s (v%s): %s\n", model.Name, model.Version, model.Description)
//	}
func (c *Client) ListModels(deviceType api.DeviceType, showAll bool) ([]api.Model, error) {
	resp, err := c.ListModelsWithStats(deviceType, showAll)
	if err != nil {
		return nil, err
	}
	return resp.Models, nil
}

// ListModelsWithStats queries available models with statistics.
//
// This method is similar to ListModels but returns the full response
// including statistics about total models, available models, and detected devices.
//
// Parameters:
//   - deviceType: Filter models by device type (DeviceTypeAll for no filter)
//   - showAll: If true, show all models regardless of device availability
//
// Returns:
//   - A pointer to ListModelsResponse containing models and statistics
//   - An error if the request fails
func (c *Client) ListModelsWithStats(deviceType api.DeviceType, showAll bool) (*api.ListModelsResponse, error) {
	req := api.ListModelsRequest{
		DeviceType: deviceType,
		ShowAll:    showAll,
	}

	var resp api.ListModelsResponse
	if err := c.doRequest("POST", "/api/models/list", req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// ListDownloadedModels queries models that have been downloaded.
//
// This method returns only models that are currently downloaded and available locally.
//
// Returns:
//   - Slice of downloaded model information
//   - An error if the request fails
func (c *Client) ListDownloadedModels() ([]api.DownloadedModel, error) {
	var resp struct {
		Models []api.DownloadedModel `json:"models"`
	}

	if err := c.doRequest("GET", "/api/models/downloaded", nil, &resp); err != nil {
		return nil, err
	}

	return resp.Models, nil
}

// GetModel retrieves detailed information about a specific model.
//
// Parameters:
//   - modelID: The unique model identifier
//
// Returns:
//   - Map containing model details
//   - Error if the model doesn't exist or the request fails
func (c *Client) GetModel(modelID string) (map[string]interface{}, error) {
	url := c.baseURL + "/api/models/show"

	reqBody := map[string]string{
		"model": modelID,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to xw server at %s", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp api.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			return nil, fmt.Errorf("server error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("server error: status %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result, nil
}

// Pull downloads and installs a model with streaming progress updates.
//
// This method downloads a model from ModelScope with real-time progress
// updates via Server-Sent Events (SSE). Progress messages are sent to the
// provided callback function as they arrive.
//
// Parameters:
//   - model: The ModelScope model ID (e.g., "Qwen/Qwen2-7B")
//   - version: The specific version (empty string for latest)
//   - progressCallback: Function called for each progress message
//
// Returns:
//   - A pointer to PullResponse with final status
//   - An error if the request fails
//
// Example:
//
//	resp, err := client.Pull("Qwen/Qwen2-7B", "", func(msg string) {
//	    fmt.Println(msg)
//	})
func (c *Client) Pull(model, version string, progressCallback func(string)) (*api.PullResponse, error) {
	return c.pullWithSSE(model, version, progressCallback)
}

