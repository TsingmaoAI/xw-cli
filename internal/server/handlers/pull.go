// Package handlers - pull.go implements the model download endpoint with SSE streaming.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/models"
)

// PullModel handles model download requests with real-time progress streaming.
//
// This endpoint downloads AI models from ModelScope and streams progress updates
// to the client using Server-Sent Events (SSE). This provides a responsive user
// experience with real-time feedback during long-running downloads.
//
// Workflow:
//  1. Validate the model is registered in the model registry
//  2. Resolve the ModelScope source ID for downloading
//  3. Establish SSE connection with appropriate headers
//  4. Stream download progress including:
//     - Status updates (starting, downloading, extracting)
//     - Progress percentages and transfer speeds
//     - Heartbeats to keep connection alive during quiet periods
//     - Completion notification with final model path
//  5. Send explicit end signal when download completes
//
// The handler uses the model's SourceID field to determine the actual ModelScope
// model identifier. This allows users to reference models by short, memorable IDs
// (e.g., "qwen2-7b") while automatically mapping to full ModelScope paths
// (e.g., "Qwen/Qwen2-7B").
//
// SSE Message Types:
//   - status: High-level status updates
//   - progress: Download progress with file info and transfer metrics
//   - heartbeat: Keep-alive messages during periods without output
//   - complete: Final success message with model path
//   - end: Explicit stream termination signal
//   - error: Download failure notification
//
// HTTP Method: POST
// Endpoint: /api/models/pull
//
// Request body: PullRequest JSON
//
//	{
//	  "model": "qwen2-7b",      // Model ID from registry
//	  "version": "main"         // Optional: ModelScope branch/tag (default: "main")
//	}
//
// Response: SSE stream with Content-Type: text/event-stream
//
// Example SSE messages:
//
//	data: {"type":"status","message":"Starting download of Qwen2 7B..."}
//
//	data: {"type":"progress","message":"Downloading [model.safetensors]: 15% | 1.5GB/10GB"}
//
//	data: {"type":"heartbeat","message":"Download in progress..."}
//
//	data: {"type":"complete","status":"success","message":"Model downloaded to /root/.xw/models/qwen2-7b","path":"/root/.xw/models/qwen2-7b"}
//
//	data: {"type":"end"}
//
// Example usage:
//
//	curl -X POST http://localhost:11581/api/models/pull \
//	  -H "Content-Type: application/json" \
//	  -d '{"model":"qwen2-7b"}'
func (h *Handler) PullModel(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method - only POST is allowed
	if r.Method != http.MethodPost {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse and validate request body
	var req api.PullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate model name is provided
	if req.Model == "" {
		h.WriteError(w, "Model name is required", http.StatusBadRequest)
		return
	}

	// Verify model is registered and retrieve its specification
	modelSpec := models.GetModelSpec(req.Model)
	if modelSpec == nil {
		h.WriteError(w, fmt.Sprintf("Model not found: %s", req.Model), http.StatusNotFound)
		return
	}

	// Resolve the actual source ID for downloading
	// If SourceID is set, use it; otherwise, fall back to the model ID
	// This allows flexible model naming while maintaining compatibility
	sourceID := modelSpec.SourceID
	if sourceID == "" {
		sourceID = req.Model
	}

	// Set SSE headers for streaming response
	// These headers are critical for proper SSE functionality
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering if behind proxy

	// Verify the response writer supports flushing
	// This is required for SSE to work properly
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.WriteError(w, "Streaming not supported by this server", http.StatusInternalServerError)
		return
	}

	// Log the pull operation for monitoring and debugging
	logger.Info("Pulling model: %s (source: %s)", req.Model, sourceID)

	// Send initial status message to inform client download is starting
	fmt.Fprintf(w, "data: {\"type\":\"status\",\"message\":\"Starting download of %s...\"}\n\n", modelSpec.DisplayName)
	flusher.Flush()

	// Execute the actual download with streaming output
	// Pass request context so download is cancelled if client disconnects
	// This delegates to the download implementation which handles:
	// - Direct HTTP downloads via Go ModelScope client
	// - Progress tracking and SSE streaming
	// - Automatic cancellation on client disconnect
	modelPath, err := h.downloadModelStreaming(r.Context(), sourceID, req.Version, w, flusher)
	if err != nil {
		// Send error message via SSE and terminate stream
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"message\":\"Failed to download: %s\"}\n\n", err.Error())
		flusher.Flush()
		return
	}

	// Generate Modelfile after successful download
	if err := h.generateModelfile(modelPath, req.Model, modelSpec); err != nil {
		logger.Warn("Failed to generate Modelfile for %s: %v", req.Model, err)
		// Don't fail the whole operation, just log the warning
	}

	// Send final success message with model path
	finalMsg := fmt.Sprintf(
		"{\"type\":\"complete\",\"status\":\"success\",\"message\":\"Model downloaded to %s\",\"path\":\"%s\"}",
		modelPath, modelPath,
	)
	fmt.Fprintf(w, "data: %s\n\n", finalMsg)
	flusher.Flush()

	// Send explicit end signal to notify client that stream is complete
	// This prevents the client from waiting indefinitely
	fmt.Fprintf(w, "data: {\"type\":\"end\"}\n\n")
	flusher.Flush()
}

// generateModelfile creates a Modelfile in the model directory.
//
// The Modelfile serves as a user-editable configuration layer on top of the
// base model, similar to Docker's Dockerfile concept. It allows users to:
//   - Customize system prompts
//   - Adjust inference parameters
//   - Configure prompt templates
//   - Document model metadata
//
// Parameters:
//   - modelPath: Path to the downloaded model directory
//   - modelID: Model identifier (e.g., "qwen2-0.5b")
//   - spec: Model specification containing metadata
//
// Returns:
//   - Error if Modelfile creation fails
func (h *Handler) generateModelfile(modelPath, modelID string, spec *models.ModelSpec) error {
	modelfilePath := filepath.Join(modelPath, "Modelfile")
	
	// Check if Modelfile already exists (don't overwrite user customizations)
	if _, err := os.Stat(modelfilePath); err == nil {
		logger.Info("Modelfile already exists at %s, skipping generation", modelfilePath)
		return nil
	}
	
	// Build Modelfile content
	var content strings.Builder
	
	// Header
	content.WriteString("# Modelfile\n\n")
	content.WriteString(fmt.Sprintf("FROM %s\n\n", modelID))
	
	// Description
	content.WriteString(fmt.Sprintf("# %s\n\n", spec.Description))
	
	// Parameters section
	content.WriteString("Parameters:\n")
	content.WriteString(fmt.Sprintf("  Model Size:      %.1fB parameters\n", spec.Parameters))
	content.WriteString(fmt.Sprintf("  Context Length:  %d tokens\n", spec.ContextLength))
	content.WriteString(fmt.Sprintf("  Required VRAM:   %d GB\n\n", spec.RequiredVRAM))
	
	// Template section
	content.WriteString("Template:\n")
	content.WriteString("  {{ .System }}\n")
	content.WriteString("  {{ .Prompt }}\n\n")
	
	// System prompt section
	content.WriteString("System Prompt:\n")
	content.WriteString("  You are a helpful AI assistant.\n\n")
	
	// Inference engine section
	content.WriteString("Inference Engine:\n")
	content.WriteString("  Available Backends:\n")
	for i, backend := range spec.Backends {
		content.WriteString(fmt.Sprintf("    %d. %s:%s\n", i+1, backend.Type, backend.Mode))
	}
	content.WriteString("\n")
	
	// Supported devices section
	content.WriteString("  Supported Devices:\n")
	for _, device := range spec.SupportedDevices {
		content.WriteString(fmt.Sprintf("    - %s\n", device))
	}
	content.WriteString("\n")
	
	// Write to file
	if err := os.WriteFile(modelfilePath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to write Modelfile: %w", err)
	}
	
	logger.Info("Generated Modelfile at %s", modelfilePath)
	return nil
}

