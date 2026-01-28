// Package handlers implements HTTP request handlers for the xw server.
//
// This file provides a high-performance HTTP proxy that forwards OpenAI-compatible
// API requests to underlying inference services. The proxy operates at the HTTP
// protocol level, minimizing overhead by avoiding unnecessary request body parsing.
package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/runtime"
)

// ProxyHandler handles proxying requests to inference services.
//
// This handler implements a transparent HTTP proxy that:
//   - Forwards requests to running model instances
//   - Handles both streaming and non-streaming responses
//   - Supports OpenAI-compatible API endpoints
//   - Preserves request/response semantics at the protocol level
//   - Controls concurrency based on tensor parallelism degree
//
// The proxy is designed for high performance and low latency, making it
// suitable for production inference workloads.
type ProxyHandler struct {
	handler           *Handler                      // Reference to main handler for accessing runtime manager
	concurrencyMgr    *concurrencyManager           // Manages concurrent requests per instance
}

// concurrencyManager manages concurrent request limits for each model instance.
//
// Each instance has a semaphore based on its tensor_parallel value, which
// determines how many concurrent requests it can handle efficiently.
type concurrencyManager struct {
	mu         sync.RWMutex
	semaphores map[string]chan struct{} // instanceID -> semaphore channel
}

// newConcurrencyManager creates a new concurrency manager.
func newConcurrencyManager() *concurrencyManager {
	return &concurrencyManager{
		semaphores: make(map[string]chan struct{}),
	}
}

// acquireSlot acquires a concurrency slot for the given instance.
//
// This method will block if the instance is already handling the maximum
// number of concurrent requests. The maxConcurrency parameter is typically
// the tensor_parallel value from the instance metadata.
//
// Parameters:
//   - ctx: Request context for cancellation
//   - instanceID: Unique identifier for the instance
//   - maxConcurrency: Maximum concurrent requests allowed
//
// Returns:
//   - Release function to call when request is complete
//   - Error if context is cancelled or semaphore creation fails
func (cm *concurrencyManager) acquireSlot(ctx context.Context, instanceID string, maxConcurrency int) (func(), error) {
	// Get or create semaphore for this instance
	cm.mu.Lock()
	sem, exists := cm.semaphores[instanceID]
	if !exists {
		// Create a buffered channel as semaphore
		// Size is the max concurrent requests (typically tensor_parallel value)
		sem = make(chan struct{}, maxConcurrency)
		cm.semaphores[instanceID] = sem
		logger.Debug("Created concurrency semaphore for instance %s (max: %d)", instanceID, maxConcurrency)
	}
	cm.mu.Unlock()
	
	// Try to acquire a slot
	select {
	case sem <- struct{}{}:
		// Slot acquired
		logger.Debug("Acquired concurrency slot for instance %s", instanceID)
		
		// Return release function
		return func() {
			<-sem
			logger.Debug("Released concurrency slot for instance %s", instanceID)
		}, nil
		
	case <-ctx.Done():
		// Context cancelled while waiting
		return nil, fmt.Errorf("request cancelled while waiting for concurrency slot: %w", ctx.Err())
	}
}

// cleanupInstance removes the semaphore for a stopped instance.
//
// This should be called when an instance is stopped to free resources.
func (cm *concurrencyManager) cleanupInstance(instanceID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	
	if sem, exists := cm.semaphores[instanceID]; exists {
		close(sem)
		delete(cm.semaphores, instanceID)
		logger.Debug("Cleaned up concurrency semaphore for instance %s", instanceID)
	}
}

// NewProxyHandler creates a new proxy handler instance.
//
// The proxy handler requires access to the runtime manager to resolve
// instance IDs to actual service endpoints.
func NewProxyHandler(h *Handler) *ProxyHandler {
	return &ProxyHandler{
		handler:        h,
		concurrencyMgr: newConcurrencyManager(),
	}
}

// minimalRequest represents minimal fields needed from request body.
//
// We only extract the fields we need for routing and stream detection,
// avoiding full request body parsing which would introduce unnecessary overhead.
type minimalRequest struct {
	Model  string `json:"model"`           // Model name for routing to correct instance
	Stream bool   `json:"stream,omitempty"` // Whether this is a streaming request
}

// ProxyRequest handles proxying a request to an inference service.
//
// This method implements the core proxy logic following OpenAI API conventions:
//   1. Validate request path matches OpenAI API format (/v1/*)
//   2. Extract model name from request body (minimal parsing)
//   3. Find running instance matching the model name
//   4. Detect if request is streaming by inspecting request body
//   5. Forward request to target service
//   6. Stream or buffer response back to client
//
// OpenAI API Endpoints Supported:
//   - POST /v1/chat/completions - Chat completions (streaming/non-streaming)
//   - POST /v1/completions - Text completions (streaming/non-streaming)
//   - POST /v1/embeddings - Embeddings (non-streaming only)
//
// The proxy preserves HTTP semantics including:
//   - Request headers (except Host and Connection)
//   - Response headers
//   - Status codes
//   - Streaming vs buffered transfer
func (p *ProxyHandler) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	// Validate path follows OpenAI API convention: /v1/{endpoint}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.Error(w, "Invalid API path. Expected OpenAI-compatible format: /v1/{endpoint}", http.StatusBadRequest)
		return
	}

	logger.Debug("Proxying OpenAI API request: %s %s", r.Method, r.URL.Path)
	// Read request body to extract model name and streaming mode
	// We need to preserve the body for forwarding, so we read it entirely
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Extract minimal fields from request body: model and stream
	// We do minimal JSON parsing - only extract the fields we need for routing
	var minReq minimalRequest
	if len(bodyBytes) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
		if err := decoder.Decode(&minReq); err != nil {
			logger.Error("Failed to parse request body: %v", err)
			http.Error(w, "Invalid request body: must be valid JSON", http.StatusBadRequest)
			return
		}
	}

	// Validate model field is present
	if minReq.Model == "" {
		http.Error(w, "Missing required field: model", http.StatusBadRequest)
		return
	}

	logger.Debug("Request model: %s, streaming: %v", minReq.Model, minReq.Stream)

	// Find running instance that matches the model name
	// We look for instances where the model ID matches the requested model
	instance, err := p.findInstanceByModel(r.Context(), minReq.Model)
	if err != nil {
		logger.Error("No running instance found for model %s: %v", minReq.Model, err)
		http.Error(w, fmt.Sprintf("No running instance found for model: %s", minReq.Model), http.StatusNotFound)
		return
	}

	if instance.State != "running" {
		logger.Warn("Instance %s is not running (state: %s)", instance.ID, instance.State)
		http.Error(w, fmt.Sprintf("Model instance is not running (state: %s)", instance.State), http.StatusServiceUnavailable)
		return
	}

	logger.Debug("Routing to instance %s on port %d", instance.ID, instance.Port)

	// Get concurrency limit from instance metadata (max_concurrent)
	// Default to 0 (unlimited) if not specified
	maxConcurrency := 0
	if maxConcurrentStr, ok := instance.Metadata["max_concurrent"]; ok && maxConcurrentStr != "" {
		if mc, err := strconv.Atoi(maxConcurrentStr); err == nil && mc > 0 {
			maxConcurrency = mc
		}
	}
	
	// Only apply concurrency control if max_concurrent was explicitly set
	var releaseSlot func()
	if maxConcurrency > 0 {
		// Acquire concurrency slot for this instance
		// This will block if the instance is already at max capacity
		slot, err := p.concurrencyMgr.acquireSlot(r.Context(), instance.ID, maxConcurrency)
		if err != nil {
			logger.Warn("Failed to acquire concurrency slot for instance %s: %v", instance.ID, err)
			http.Error(w, "Service temporarily unavailable (concurrency limit reached)", http.StatusServiceUnavailable)
			return
		}
		releaseSlot = slot
		defer releaseSlot() // Ensure slot is released when request completes
		
		logger.Debug("Processing request for instance %s (max concurrent: %d)", instance.ID, maxConcurrency)
	} else {
		logger.Debug("Processing request for instance %s (unlimited concurrency)", instance.ID)
	}

	// Construct target URL - preserve the original path and query
	targetURL := fmt.Sprintf("http://localhost:%d%s", instance.Port, r.URL.Path)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	logger.Debug("Forwarding to: %s", targetURL)

	// Create proxy request
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("Failed to create proxy request: %v", err)
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	// Skip certain headers that should not be forwarded
	for name, values := range r.Header {
		// Skip hop-by-hop headers
		if name == "Connection" || name == "Keep-Alive" || name == "Proxy-Authenticate" ||
			name == "Proxy-Authorization" || name == "Te" || name == "Trailers" ||
			name == "Transfer-Encoding" || name == "Upgrade" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(name, value)
		}
	}

	// Ensure we have proper content type
	if proxyReq.Header.Get("Content-Type") == "" && len(bodyBytes) > 0 {
		proxyReq.Header.Set("Content-Type", "application/json")
	}

	// Create HTTP client with reasonable timeout
	client := &http.Client{
		Timeout: 0, // No timeout for streaming requests
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Execute proxy request
	resp, err := client.Do(proxyReq)
	if err != nil {
		logger.Error("Proxy request failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to forward request: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for name, values := range resp.Header {
		// Skip hop-by-hop headers
		if name == "Connection" || name == "Keep-Alive" || name == "Proxy-Authenticate" ||
			name == "Proxy-Authorization" || name == "Te" || name == "Trailers" ||
			name == "Transfer-Encoding" || name == "Upgrade" {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Handle response based on streaming mode
	if minReq.Stream {
		// For streaming responses, we need to flush each chunk immediately
		p.handleStreamingResponse(w, resp.Body)
	} else {
		// For non-streaming responses, we can buffer the entire response
		p.handleBufferedResponse(w, resp.Body)
	}

	logger.Debug("Proxy request completed successfully for instance: %s", instance.ID)
}

// findInstanceByModel finds a running instance that serves the specified model.
//
// This method searches all running instances to find one whose model ID
// matches the requested model name. The matching is case-insensitive and
// supports both exact matches and prefix matches (e.g., "qwen2-7b" matches
// "qwen2-7b-instruct-123456").
//
// Parameters:
//   - ctx: Request context
//   - modelName: The model name from the request body
//
// Returns:
//   - The matching instance if found
//   - Error if no matching instance is found or lookup fails
func (p *ProxyHandler) findInstanceByModel(ctx context.Context, modelName string) (*runtime.Instance, error) {
	// Get all running instances
	instances, err := p.handler.runtimeManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	// Normalize model name for comparison
	modelNameLower := strings.ToLower(modelName)

	// First pass: look for exact match
	for _, inst := range instances {
		if inst.State == "running" {
			instModelLower := strings.ToLower(inst.ModelID)
			if instModelLower == modelNameLower {
				logger.Debug("Found exact match: instance %s for model %s", inst.ID, modelName)
				return inst, nil
			}
		}
	}

	// Second pass: look for prefix match
	// This handles cases like "qwen2-7b" matching "qwen2-7b-instruct"
	for _, inst := range instances {
		if inst.State == "running" {
			instModelLower := strings.ToLower(inst.ModelID)
			if strings.HasPrefix(instModelLower, modelNameLower) || strings.HasPrefix(modelNameLower, instModelLower) {
				logger.Debug("Found prefix match: instance %s (model: %s) for requested model %s", inst.ID, inst.ModelID, modelName)
				return inst, nil
			}
		}
	}

	return nil, fmt.Errorf("no running instance found for model: %s", modelName)
}

// handleStreamingResponse handles streaming responses from the inference service.
//
// Streaming responses use Server-Sent Events (SSE) format:
//   - Each line starts with "data: "
//   - JSON objects are sent line by line
//   - Stream ends with "data: [DONE]"
//
// This method:
//   1. Reads the response line by line
//   2. Flushes each line immediately to the client
//   3. Handles connection errors gracefully
//
// This ensures low latency for streaming responses, which is critical
// for chat applications where users expect incremental output.
func (p *ProxyHandler) handleStreamingResponse(w http.ResponseWriter, body io.ReadCloser) {
	// Get flusher for streaming
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("Response writer does not support flushing")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Create a buffered reader for efficient line reading
	reader := bufio.NewReader(body)
	
	// Buffer for reading chunks
	buf := make([]byte, 4096)

	for {
		// Read chunk from response
		n, err := reader.Read(buf)
		if n > 0 {
			// Write chunk to client
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				logger.Debug("Client disconnected during streaming: %v", writeErr)
				return
			}
			
			// Flush immediately for low latency
			flusher.Flush()
		}

		if err != nil {
			if err == io.EOF {
				// Normal end of stream
				logger.Debug("Stream completed successfully")
				return
			}
			// Network error or connection closed
			logger.Debug("Stream interrupted: %v", err)
			return
		}
	}
}

// handleBufferedResponse handles non-streaming responses from the inference service.
//
// Non-streaming responses are simpler:
//   1. Read entire response body
//   2. Write to client in one go
//   3. Return
//
// This is suitable for:
//   - Embeddings requests (always non-streaming)
//   - Chat/completion requests with stream=false
//   - Any other synchronous operations
func (p *ProxyHandler) handleBufferedResponse(w http.ResponseWriter, body io.ReadCloser) {
	// For non-streaming, we can use efficient copying
	written, err := io.Copy(w, body)
	if err != nil {
		logger.Error("Failed to write response body: %v", err)
		return
	}
	
	logger.Debug("Wrote %d bytes in buffered response", written)
}


// HealthCheck provides a health check endpoint for the proxy.
//
// This can be used by load balancers or monitoring systems to verify
// that the proxy is operational.
func (p *ProxyHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	
	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"service":   "xw-proxy",
	}
	
	json.NewEncoder(w).Encode(response)
}

