package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/tsingmao/xw/internal/hooks"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/models"
	"github.com/tsingmao/xw/internal/runtime"
)

// StartModel handles requests to start a model instance
//
// This endpoint supports Server-Sent Events (SSE) for streaming progress updates
// during model startup, Docker image pulling, etc.
//
// HTTP Method: POST
// Path: /api/runtime/start
// Content-Type: application/json
// Accept: text/event-stream (for SSE) or application/json
func (h *Handler) StartModel(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		ModelID        string                 `json:"model_id"`
		Alias          string                 `json:"alias"`
		BackendType    models.BackendType     `json:"backend_type"`
		DeploymentMode models.DeploymentMode  `json:"deployment_mode"`
		Interactive    bool                   `json:"interactive"`
		Config         map[string]interface{} `json:"additional_config"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	
	// Check if client accepts SSE
	acceptSSE := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	
	if acceptSSE {
		// Use SSE for streaming progress
		h.runModelWithSSE(w, r, &reqBody)
	} else {
		// Use regular JSON response
		h.runModelJSON(w, &reqBody)
	}
}

// runModelWithSSE handles model running with SSE streaming
func (h *Handler) runModelWithSSE(w http.ResponseWriter, r *http.Request, reqBody *struct {
	ModelID        string                 `json:"model_id"`
	Alias          string                 `json:"alias"`
	BackendType    models.BackendType     `json:"backend_type"`
	DeploymentMode models.DeploymentMode  `json:"deployment_mode"`
	Interactive    bool                   `json:"interactive"`
	Config         map[string]interface{} `json:"additional_config"`
}) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.WriteError(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	
	// Create event channel
	eventCh := make(chan string, 100)
	doneCh := make(chan struct{})
	errorCh := make(chan error, 1)
	
	// Create cancellable context from request context
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel() // Ensure cleanup on exit
	
	// Start model in background with cancellable context
	go h.runModelAsync(ctx, reqBody, eventCh, doneCh, errorCh)
	
	// Stream events
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case event := <-eventCh:
			// Send event
			fmt.Fprintf(w, "data: %s\n\n", h.escapeSSE(event))
			flusher.Flush()
			
		case <-doneCh:
			// Success
			fmt.Fprintf(w, "event: done\ndata: {\"status\":\"success\"}\n\n")
			flusher.Flush()
			return
			
		case err := <-errorCh:
			// Error
			errData := map[string]string{"error": err.Error()}
			errJSON, _ := json.Marshal(errData)
			fmt.Fprintf(w, "event: error\ndata: %s\n\n", errJSON)
			flusher.Flush()
			return
			
		case <-ticker.C:
			// Keep-alive ping
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
			
		case <-r.Context().Done():
			// Client disconnected (Ctrl+C or network issue)
			// Cancel the context to stop all running hooks
			cancel()
			logger.Info("Client disconnected, cancelling running hooks")
			return
		}
	}
}

// runModelAsync runs the model asynchronously and sends progress events
func (h *Handler) runModelAsync(ctx context.Context, reqBody *struct {
	ModelID        string                 `json:"model_id"`
	Alias          string                 `json:"alias"`
	BackendType    models.BackendType     `json:"backend_type"`
	DeploymentMode models.DeploymentMode  `json:"deployment_mode"`
	Interactive    bool                   `json:"interactive"`
	Config         map[string]interface{} `json:"additional_config"`
}, eventCh chan<- string, doneCh chan<- struct{}, errorCh chan<- error) {
	
	defer close(eventCh)
	
	// Check if context is already cancelled
	if ctx.Err() != nil {
		errorCh <- fmt.Errorf("operation cancelled")
		return
	}
	
	// Get model spec first
	modelSpec := models.GetModelSpec(reqBody.ModelID)
	if modelSpec == nil {
		errorCh <- fmt.Errorf("model not found: %s", reqBody.ModelID)
		return
	}
	
	// Find the matching backend option from model spec
	var selectedBackend *models.BackendOption
	if reqBody.BackendType == "" || reqBody.DeploymentMode == "" {
		// Use first backend as default
		if len(modelSpec.Backends) == 0 {
			errorCh <- fmt.Errorf("no backends available for model %s", reqBody.ModelID)
			return
		}
		selectedBackend = &modelSpec.Backends[0]
		reqBody.BackendType = selectedBackend.Type
		reqBody.DeploymentMode = selectedBackend.Mode
		eventCh <- fmt.Sprintf("Using default backend: %s (%s mode)", reqBody.BackendType, reqBody.DeploymentMode)
	} else {
		// Find matching backend from user's choice
		for i := range modelSpec.Backends {
			backend := &modelSpec.Backends[i]
			if backend.Type == reqBody.BackendType && backend.Mode == reqBody.DeploymentMode {
				selectedBackend = backend
				break
			}
		}
		if selectedBackend == nil {
			errorCh <- fmt.Errorf("backend %s (%s mode) not available for model %s", 
				reqBody.BackendType, reqBody.DeploymentMode, reqBody.ModelID)
			return
		}
	}
	
	// Only support Docker mode for now
	if reqBody.DeploymentMode != models.DeploymentModeDocker {
		errorCh <- fmt.Errorf("only Docker mode is currently supported")
		return
	}
	
	// Use hook system to check and install Docker
	hookRunner := hooks.NewRunner()
	
	dockerHook := hooks.NewDockerHook(eventCh)
	hookRunner.Register(dockerHook)
	
	// Note: Image pulling is handled by the runtime itself
	// Each runtime (vllm-docker, mindie-docker) knows its own default image
	
	// Run all hooks in auto mode (will install if missing)
	// Use the cancellable context so hooks stop when client disconnects
	if err := hookRunner.Run(ctx, hooks.ModeAuto); err != nil {
		// Check if it's a cancellation
		if ctx.Err() != nil {
			errorCh <- fmt.Errorf("Operation cancelled by user")
			return
		}
		errorCh <- fmt.Errorf("Setup failed: %w", err)
		return
	}
	
	// Get model path
	modelPath := h.getModelPath(h.config.Storage.ModelsDir, reqBody.ModelID)
	
	// Prepare additional config
	additionalConfig := reqBody.Config
	if additionalConfig == nil {
		additionalConfig = make(map[string]interface{})
	}
	// Note: Don't pass image name - runtime uses its own default
	
	// Always auto-allocate port
	portAllocator := runtime.GetGlobalPortAllocator()
	port, err := portAllocator.GetFreePort()
	if err != nil {
		errorCh <- fmt.Errorf("failed to allocate port: %w", err)
		return
	}
	eventCh <- fmt.Sprintf("Allocated port %d for model instance", port)
	
	// Create run options
	opts := &runtime.RunOptions{
		ModelID:          reqBody.ModelID,
		Alias:            reqBody.Alias,
		ModelPath:        modelPath,
		BackendType:      string(reqBody.BackendType),
		DeploymentMode:   string(reqBody.DeploymentMode),
		Port:             port,
		Interactive:      reqBody.Interactive,
		AdditionalConfig: additionalConfig,
	}
	
	// Start the model
	eventCh <- "Starting model instance..."
	// Pass config directory to runtime manager, just like downloader uses h.config.Storage.ModelsDir
	instance, err := h.runtimeManager.Run(h.config.Storage.ConfigDir, opts)
	if err != nil {
		errorCh <- err
		return
	}
	
	// Send success event with instance info
	successData := map[string]interface{}{
		"instance_id":     instance.ID,
		"model_id":        instance.ModelID,
		"backend_type":    instance.BackendType,
		"deployment_mode": instance.DeploymentMode,
		"port":            instance.Port,
		"state":           instance.State,
	}
	
	dataJSON, _ := json.Marshal(successData)
	eventCh <- string(dataJSON)
	
	doneCh <- struct{}{}
}

// runModelJSON handles model running with regular JSON response
func (h *Handler) runModelJSON(w http.ResponseWriter, reqBody *struct {
	ModelID        string                 `json:"model_id"`
	Alias          string                 `json:"alias"`
	BackendType    models.BackendType     `json:"backend_type"`
	DeploymentMode models.DeploymentMode  `json:"deployment_mode"`
	Interactive    bool                   `json:"interactive"`
	Config         map[string]interface{} `json:"additional_config"`
}) {
	// For JSON mode, we don't stream progress
	// This is a simplified version
	
	// Always auto-allocate port
	portAllocator := runtime.GetGlobalPortAllocator()
	port, err := portAllocator.GetFreePort()
	if err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to allocate port: %v", err), http.StatusInternalServerError)
		return
	}
	
	opts := &runtime.RunOptions{
		ModelID:          reqBody.ModelID,
		Alias:            reqBody.Alias,
		BackendType:      string(reqBody.BackendType),
		DeploymentMode:   string(reqBody.DeploymentMode),
		Port:             port,
		Interactive:      reqBody.Interactive,
		AdditionalConfig: reqBody.Config,
	}
	
	// Pass config directory to runtime manager, just like downloader uses h.config.Storage.ModelsDir
	instance, err := h.runtimeManager.Run(h.config.Storage.ConfigDir, opts)
	if err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to start model: %v", err), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"instance_id":     instance.ID,
		"model_id":        instance.ModelID,
		"backend_type":    instance.BackendType,
		"deployment_mode": instance.DeploymentMode,
		"port":            instance.Port,
		"state":           instance.State,
	}
	
	h.WriteJSON(w, response, http.StatusOK)
}

// ListInstances handles requests to list running instances
func (h *Handler) ListInstances(w http.ResponseWriter, r *http.Request) {
	// Check if "all" parameter is set
	showAll := r.URL.Query().Get("all") == "true"
	
	instances := h.runtimeManager.ListCompat()
	
	// Check real status for each instance and update state
	for _, inst := range instances {
		// Only check running instances
		if inst.State == runtime.StateRunning {
			if inst.Port == 0 {
				// Running but no port assigned - unknown state
				inst.State = runtime.StateUnknown
			} else {
				// Build endpoint from port
				endpoint := fmt.Sprintf("http://localhost:%d", inst.Port)
				
				// Check if endpoint is actually accessible
				if h.checkEndpointAccessible(endpoint) {
					inst.State = runtime.StateReady
				} else {
					// Running but endpoint not accessible - unhealthy
					inst.State = runtime.StateUnhealthy
				}
			}
		}
	}
	
	// Filter out stopped instances if not showing all
	if !showAll {
		filtered := make([]*runtime.RunInstance, 0)
		for _, inst := range instances {
			// Show all non-stopped instances
			if inst.State != runtime.StateStopped {
				filtered = append(filtered, inst)
			}
		}
		instances = filtered
	}
	
	// Sort instances by created time (oldest first) for consistent order
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].CreatedAt.Before(instances[j].CreatedAt)
	})
	
	response := map[string]interface{}{
		"instances": instances,
	}
	
	h.WriteJSON(w, response, http.StatusOK)
}

// CheckInstanceReady checks if a model instance is ready to serve requests.
//
// This endpoint verifies that the instance's endpoint is accessible and responding.
// It's useful for waiting for an instance to fully start before sending requests.
//
// HTTP Method: GET
// Path: /api/runtime/check-ready?alias=ALIAS
//
// Query parameters:
//   - alias: The instance alias to check
//
// Response:
//
//	{
//	  "ready": true,
//	  "alias": "qwen2.5-7b-instruct",
//	  "endpoint": "http://localhost:10881",
//	  "message": "Instance is ready"
//	}
func (h *Handler) CheckInstanceReady(w http.ResponseWriter, r *http.Request) {
	// Get alias from query parameter
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		h.WriteError(w, "alias parameter is required", http.StatusBadRequest)
		return
	}

	// Get instance from runtime manager
	instances := h.runtimeManager.ListCompat()
	var instance *runtime.RunInstance
	for _, inst := range instances {
		if inst.Alias == alias {
			instance = inst
			break
		}
	}

	if instance == nil {
		h.WriteError(w, fmt.Sprintf("Instance not found: %s", alias), http.StatusNotFound)
		return
	}

	// Check if instance state is running
	if instance.State != runtime.StateRunning {
		h.WriteJSON(w, map[string]interface{}{
			"ready":    false,
			"alias":    alias,
			"state":    instance.State,
			"message":  fmt.Sprintf("Instance is in %s state", instance.State),
		}, http.StatusOK)
		return
	}

	// Build endpoint URL from port
	endpoint := fmt.Sprintf("http://localhost:%d", instance.Port)
	
	// Check if endpoint is accessible
	ready := h.checkEndpointAccessible(endpoint)

	response := map[string]interface{}{
		"ready":    ready,
		"alias":    alias,
		"endpoint": endpoint,
	}

	if ready {
		response["message"] = "Instance is ready"
	} else {
		response["message"] = "Instance is starting, endpoint not ready yet"
	}

	h.WriteJSON(w, response, http.StatusOK)
}

// checkEndpointAccessible checks if an HTTP endpoint is accessible
func (h *Handler) checkEndpointAccessible(endpoint string) bool {
	// Check the health endpoint
	client := &http.Client{
		Timeout: 1 * time.Second,
	}

	// Only check /health endpoint
	healthURL := endpoint + "/health"
	resp, err := client.Get(healthURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// Accept 20x status codes as success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true
	}

	// 404 also counts as success (for engines without /health endpoint)
	// but we'll log a warning
	if resp.StatusCode == http.StatusNotFound {
		logger.Warn("Endpoint %s returned 404 - engine may not implement /health", healthURL)
		return true
	}

	return false
}

// StopInstance handles requests to stop a running instance
func (h *Handler) StopInstance(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		InstanceID string `json:"instance_id"` // Deprecated: use alias instead
		Alias      string `json:"alias"`
		Force      bool   `json:"force"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	
	// Support both alias (new) and instance_id (legacy)
	identifier := reqBody.Alias
	if identifier == "" {
		identifier = reqBody.InstanceID
	}
	
	if identifier == "" {
		h.WriteError(w, "alias or instance_id is required", http.StatusBadRequest)
		return
	}
	
	if err := h.runtimeManager.StopByAliasCompat(identifier); err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to stop instance: %v", err), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"message": "Instance stopped successfully",
	}
	
	h.WriteJSON(w, response, http.StatusOK)
}

// RemoveInstance handles HTTP requests to remove a model instance.
//
// HTTP Method: POST
// Path: /api/runtime/remove
// Content-Type: application/json
func (h *Handler) RemoveInstance(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		InstanceID string `json:"instance_id"` // Deprecated: use alias instead
		Alias      string `json:"alias"`
		Force      bool   `json:"force"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	
	// Support both alias (new) and instance_id (legacy)
	identifier := reqBody.Alias
	if identifier == "" {
		identifier = reqBody.InstanceID
	}
	
	if identifier == "" {
		h.WriteError(w, "alias or instance_id is required", http.StatusBadRequest)
		return
	}
	
	if err := h.runtimeManager.RemoveByAliasCompat(identifier, reqBody.Force); err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to remove instance: %v", err), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"message": "Instance removed successfully",
	}
	
	h.WriteJSON(w, response, http.StatusOK)
}

// escapeSSE escapes special characters for SSE
func (h *Handler) escapeSSE(s string) string {
	// Replace newlines with spaces for SSE
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// StreamLogs streams instance logs.
//
// HTTP Method: GET
// Path: /api/runtime/logs?alias=ALIAS&follow=true|false
// Accept: text/plain or text/event-stream
func (h *Handler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		h.WriteError(w, "alias parameter is required", http.StatusBadRequest)
		return
	}
	
	// Get follow parameter (default: true for backward compatibility)
	follow := r.URL.Query().Get("follow") != "false"
	
	// Get log stream from runtime manager
	logStream, err := h.runtimeManager.GetLogsByAlias(r.Context(), alias, follow)
	if err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to get logs: %v", err), http.StatusInternalServerError)
		return
	}
	defer logStream.Close()
	
	// Set headers for streaming
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering
	
	// Flush headers immediately
	flusher, hasFlusher := w.(http.Flusher)
	if hasFlusher {
		flusher.Flush()
	}
	
	// Create a flushing writer to ensure real-time streaming
	flushWriter := &flushingWriter{
		writer:  w,
		flusher: flusher,
	}
	
	// Use stdcopy to demultiplex Docker's log stream format
	// Docker streams stdout and stderr in a multiplexed format with 8-byte headers
	// stdcopy.StdCopy properly separates and writes stdout and stderr to the response
	_, err = stdcopy.StdCopy(flushWriter, flushWriter, logStream)
	if err != nil && err != io.EOF {
		logger.Error("Error streaming logs: %v", err)
	}
}

// flushingWriter wraps http.ResponseWriter to flush after each write
type flushingWriter struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func (fw *flushingWriter) Write(p []byte) (n int, err error) {
	n, err = fw.writer.Write(p)
	if err == nil && fw.flusher != nil {
		fw.flusher.Flush()
	}
	return n, err
}
