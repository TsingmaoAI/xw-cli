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
	"github.com/tsingmao/xw/internal/device"
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
	fmt.Fprintf(w, "data: {\"type\":\"status\",\"message\":\"Starting download of %s...\"}\n\n", modelSpec.ID)
	flusher.Flush()

	// Execute the actual download with streaming output
	// Pass request context so download is cancelled if client disconnects
	// This delegates to the download implementation which handles:
	// - Direct HTTP downloads via Go ModelScope client
	// - Progress tracking and SSE streaming
	// - Automatic cancellation on client disconnect
	// Use "latest" as default tag if version is not specified
	tag := req.Version
	if tag == "" {
		tag = "latest"
	}
	modelPath, err := h.downloadModelStreaming(r.Context(), sourceID, req.Model, tag, w, flusher)
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

	// Apply chip-specific model configuration adjustments
	// For Ascend 310P, ensure torch_dtype is set to float16 in config.json
	if err := h.adjustModelConfigForChip(modelPath); err != nil {
		logger.Warn("Failed to adjust model config for chip: %v", err)
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
// This function generates an Ollama-compatible Modelfile format.
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
	
	// Build Modelfile content in Ollama format
	var content strings.Builder
	
	// Header comment
	content.WriteString("# Modelfile generated by \"xw show\"\n")
	content.WriteString("# To build a new Modelfile based on this, replace FROM with:\n")
	content.WriteString(fmt.Sprintf("# FROM %s\n\n", modelID))
	
	// FROM directive - use model path
	content.WriteString(fmt.Sprintf("FROM %s\n\n", modelPath))
	
	// TEMPLATE directive - read from tokenizer_config.json
	template := h.readChatTemplateFromTokenizer(modelPath)
	if template != "" {
		content.WriteString(fmt.Sprintf("TEMPLATE \"\"\"%s\"\"\"\n\n", template))
	}
	
	// SYSTEM directive - default system prompt
	systemPrompt := "You are Qwen, created by Alibaba Cloud. You are a helpful assistant."
	content.WriteString(fmt.Sprintf("SYSTEM %s\n\n", systemPrompt))
	
	// PARAMETER directives - read from generation_config.json or use defaults
	genConfig := h.readGenerationConfig(modelPath)
	hasParams := false
	
	if genConfig != nil {
		// Read from generation_config.json if available
		if temp, ok := genConfig["temperature"].(float64); ok {
			content.WriteString(fmt.Sprintf("PARAMETER temperature %.1f\n", temp))
			hasParams = true
		}
		if topP, ok := genConfig["top_p"].(float64); ok {
			content.WriteString(fmt.Sprintf("PARAMETER top_p %.2f\n", topP))
			hasParams = true
		}
		if topK, ok := genConfig["top_k"].(float64); ok {
			content.WriteString(fmt.Sprintf("PARAMETER top_k %.0f\n", topK))
			hasParams = true
		}
		if repPenalty, ok := genConfig["repetition_penalty"].(float64); ok {
			content.WriteString(fmt.Sprintf("PARAMETER repeat_penalty %.1f\n", repPenalty))
			hasParams = true
		}
	}
	
	// If no parameters found, use sensible defaults
	if !hasParams {
		content.WriteString("PARAMETER temperature 0.7\n")
		content.WriteString("PARAMETER top_p 0.8\n")
		content.WriteString("PARAMETER top_k 20\n")
		content.WriteString("PARAMETER repeat_penalty 1.05\n")
	}
	content.WriteString("\n")
	
	// LICENSE directive - read from LICENSE file
	if licenseContent := h.readLicenseFile(modelPath); licenseContent != "" {
		content.WriteString(fmt.Sprintf("LICENSE \"\"\"\n%s\"\"\"\n", licenseContent))
	}
	
	// Write to file
	if err := os.WriteFile(modelfilePath, []byte(content.String()), 0644); err != nil {
		return fmt.Errorf("failed to write Modelfile: %w", err)
	}
	
	logger.Info("Generated Modelfile at %s", modelfilePath)
	return nil
}

// readChatTemplateFromTokenizer reads the chat template from tokenizer_config.json
func (h *Handler) readChatTemplateFromTokenizer(modelPath string) string {
	tokenizerConfigPath := filepath.Join(modelPath, "tokenizer_config.json")
	
	data, err := os.ReadFile(tokenizerConfigPath)
	if err != nil {
		logger.Debug("Failed to read tokenizer_config.json: %v", err)
		return ""
	}
	
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		logger.Debug("Failed to parse tokenizer_config.json: %v", err)
		return ""
	}
	
	// Get chat_template directly from config (keep original Jinja2 format)
	chatTemplate, ok := config["chat_template"].(string)
	if !ok || chatTemplate == "" {
		return ""
	}
	
	return chatTemplate
}

// adjustModelConfigForChip applies chip-specific adjustments to model configuration.
//
// For Ascend 310P chips, this function modifies the model's config.json to ensure
// torch_dtype is set to "float16" for optimal compatibility and performance.
//
// The function:
//   1. Detects if any Ascend 310P chips are present in the system
//   2. If found, reads the model's config.json file
//   3. Updates the torch_dtype field to "float16"
//   4. Writes the modified configuration back to disk
//
// This automatic adjustment prevents runtime errors and ensures models work
// correctly on 310P hardware without manual configuration.
//
// Parameters:
//   - modelPath: Path to the downloaded model directory
//
// Returns:
//   - nil on success or if no adjustment is needed
//   - error if config modification fails
func (h *Handler) adjustModelConfigForChip(modelPath string) error {
	// Check if we have any Ascend 310P devices
	chipsByType, err := device.FindAIChips()
	if err != nil {
		// If chip detection fails, skip adjustment
		logger.Debug("Failed to detect chips for config adjustment: %v", err)
		return nil
	}
	
	has310P := false
	for _, chips := range chipsByType {
		for _, chip := range chips {
			if chip.ConfigKey == "ascend-310p" {
				has310P = true
				break
			}
		}
		if has310P {
			break
		}
	}
	
	// If no 310P chips detected, no adjustment needed
	if !has310P {
		return nil
	}
	
	logger.Info("Detected Ascend 310P chip, adjusting model config for compatibility")
	
	// Read config.json
	configPath := filepath.Join(modelPath, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		// If config.json doesn't exist, it's not critical
		if os.IsNotExist(err) {
			logger.Debug("No config.json found at %s, skipping adjustment", configPath)
			return nil
		}
		return fmt.Errorf("failed to read config.json: %w", err)
	}
	
	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config.json: %w", err)
	}
	
	// Check if torch_dtype needs adjustment
	currentDtype, _ := config["torch_dtype"].(string)
	if currentDtype == "float16" {
		logger.Debug("torch_dtype is already float16, no adjustment needed")
		return nil
	}
	
	// Update torch_dtype to float16
	config["torch_dtype"] = "float16"
	logger.Info("Changed torch_dtype from '%s' to 'float16' for Ascend 310P compatibility", currentDtype)
	
	// Write back to file with proper formatting
	newData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config.json: %w", err)
	}
	
	if err := os.WriteFile(configPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
	}
	
	logger.Info("Successfully updated config.json for Ascend 310P")
	return nil
}

