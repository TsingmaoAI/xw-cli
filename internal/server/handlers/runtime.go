package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	
	"github.com/tsingmao/xw/internal/hooks"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/models"
	"github.com/tsingmao/xw/internal/runtime"
)

// RunModel handles requests to start a model instance
//
// This endpoint supports Server-Sent Events (SSE) for streaming progress updates
// during model startup, Docker image pulling, etc.
//
// HTTP Method: POST
// Path: /api/runtime/run
// Content-Type: application/json
// Accept: text/event-stream (for SSE) or application/json
func (h *Handler) RunModel(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		ModelID        string                 `json:"model_id"`
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
		errorCh <- fmt.Errorf("failed to start model: %w", err)
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
	
	// Filter out stopped instances if not showing all
	if !showAll {
		filtered := make([]*runtime.RunInstance, 0)
		for _, inst := range instances {
			if inst.State == runtime.StateRunning {
				filtered = append(filtered, inst)
			}
		}
		instances = filtered
	}
	
	response := map[string]interface{}{
		"instances": instances,
	}
	
	h.WriteJSON(w, response, http.StatusOK)
}

// StopInstance handles requests to stop a running instance
func (h *Handler) StopInstance(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		InstanceID string `json:"instance_id"`
		Force      bool   `json:"force"`
	}
	
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	
	if reqBody.InstanceID == "" {
		h.WriteError(w, "instance_id is required", http.StatusBadRequest)
		return
	}
	
	if err := h.runtimeManager.StopCompat(reqBody.InstanceID); err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to stop instance: %v", err), http.StatusInternalServerError)
		return
	}
	
	response := map[string]interface{}{
		"message": "Instance stopped successfully",
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
