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
//	models, err := client.ListModels(api.DeviceTypeAscend, false)
//	if err != nil {
//	    log.Fatalf("Failed to list models: %v", err)
//	}
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tsingmao/xw/internal/api"
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
//	// List all models compatible with Ascend devices
//	models, err := client.ListModels(api.DeviceTypeAscend, false)
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

// Run executes a model on the server with the given input.
//
// This method sends an inference request to the server, which loads the
// specified model (if not already loaded) and processes the input. The model
// must have been pulled before it can be run.
//
// Parameters:
//   - model: The name of the model to execute
//   - input: The input data to process
//   - options: Optional model-specific parameters (can be nil)
//
// Returns:
//   - A pointer to RunResponse containing output and metrics
//   - An error if the request fails or the model is not found
//
// Example:
//
//	resp, err := client.Run("llama3-8b", "Hello, world!", nil)
//	if err != nil {
//	    log.Fatalf("Model execution failed: %v", err)
//	}
//	fmt.Printf("Output: %s\n", resp.Output)
//	fmt.Printf("Latency: %vms\n", resp.Metrics["latency_ms"])
func (c *Client) Run(model, input string, options map[string]interface{}) (*api.RunResponse, error) {
	req := api.RunRequest{
		Model:   model,
		Input:   input,
		Options: options,
	}

	var resp api.RunResponse
	if err := c.doRequest("POST", "/api/run", req, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
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

// UpdateModelfile updates the Modelfile for a specific model.
//
// This method sends the modified Modelfile content to the server which will:
//   - Validate the Modelfile syntax and content
//   - Write the new content to the model's Modelfile on disk
//   - Return success or validation errors
//
// Parameters:
//   - modelID: The model identifier (e.g., "qwen2-0.5b")
//   - content: The new Modelfile content
//
// Returns:
//   - nil if update succeeds
//   - error if validation fails or update fails
//
// Example:
//
//	err := client.UpdateModelfile("qwen2-0.5b", newContent)
//	if err != nil {
//	    log.Printf("Failed to update Modelfile: %v", err)
//	}
func (c *Client) UpdateModelfile(modelID, content string) error {
	url := c.baseURL + "/api/models/update-modelfile"
	
	reqBody := map[string]string{
		"model":   modelID,
		"content": content,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to xw server at %s", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp api.ErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("server error: status %d", resp.StatusCode)
	}

	return nil
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
	if err := c.doRequest("POST", "/api/runtime/run", opts, &result); err != nil {
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
func (c *Client) RunModelWithSSE(opts interface{}, progressCallback func(string)) error {
	data, err := json.Marshal(opts)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/api/runtime/run"
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot connect to xw server at %s\n\nIs the server running? Start it with: xw serve", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respData, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respData))
	}

	// Read SSE stream
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			
			// Check for done event
			if strings.Contains(data, `"status":"success"`) {
				return nil
			}
			
			// Check for error event
			if strings.Contains(data, `"error"`) {
				var errData map[string]string
				if err := json.Unmarshal([]byte(data), &errData); err == nil {
					return fmt.Errorf("server error: %s", errData["error"])
				}
				return fmt.Errorf("server error: %s", data)
			}
			
			// Progress event
			if progressCallback != nil {
				progressCallback(data)
			}
		}
		// Ignore comments (lines starting with ':')
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stream: %w", err)
	}

	return nil
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
