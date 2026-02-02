// Package client - execution.go implements model execution operations.
//
// This file provides methods for executing AI models with input data
// and retrieving inference results. These operations handle model
// invocation and result processing.
package client

import (
	"github.com/tsingmaoai/xw-cli/internal/api"
)

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

