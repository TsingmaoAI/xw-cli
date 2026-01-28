// Package handlers - models.go implements the model listing endpoint.
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

// ListModels handles requests to list available AI models.
//
// This endpoint queries the model registry and returns a list of models that
// match the specified criteria. Clients can filter models by:
//   - Device type: Only show models compatible with specific AI chips
//   - Show all: Include all models regardless of local device availability
//
// The returned model list includes comprehensive metadata for each model:
//   - Basic info: Name, version, description
//   - Hardware requirements: VRAM, supported devices
//   - Model specifications: Parameters, context length, license
//
// This endpoint is called by the CLI 'xw ls' command and can be used by
// other clients to discover available models before pulling or running them.
//
// HTTP Method: POST (uses POST to accept filter criteria in request body)
// Endpoint: /api/models/list
//
// Request body: ListModelsRequest JSON
//
//	{
//	  "device_type": "ascend",  // Optional: Filter by device type
//	  "show_all": false         // Optional: Show all or only available models
//	}
//
// Response: 200 OK with ListModelsResponse JSON
//
//	{
//	  "models": [
//	    {
//	      "name": "qwen2-7b",
//	      "display_name": "Qwen2 7B",
//	      "version": "2.0",
//	      "description": "Qwen2 7B parameter model...",
//	      "parameters": 7.0,
//	      "required_vram": 16,
//	      "supported_devices": ["ascend", "kunlun"],
//	      "tags": ["chat", "general"]
//	    }
//	  ]
//	}
//
// Example usage:
//
//	curl -X POST http://localhost:11581/api/models/list \
//	  -H "Content-Type: application/json" \
//	  -d '{"device_type":"ascend","show_all":false}'
func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method - only POST is allowed
	if r.Method != http.MethodPost {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req api.ListModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Get detected devices from device manager
	detectedDevices := h.deviceManager.GetDetectedDeviceTypes()
	
	// Get all models for counting
	allModels := h.modelRegistry.List(api.DeviceTypeAll, true)
	totalModels := len(allModels)

	// Query model registry with filters
	// The registry will apply device compatibility checks and filter logic
	var models []api.Model
	var availableModels int
	
	if req.ShowAll {
		// Show all models
		models = allModels
		availableModels = h.modelRegistry.CountAvailableModels(detectedDevices)
	} else {
		// Show only available models (default behavior)
		models = h.modelRegistry.ListAvailableModels(detectedDevices)
		availableModels = len(models)
	}
	
	// Check download status for each model
	h.enrichModelsWithDownloadStatus(&models)

	// Construct response with statistics
	resp := api.ListModelsResponse{
		Models:           models,
		TotalModels:      totalModels,
		AvailableModels:  availableModels,
		DetectedDevices:  detectedDevices,
	}

	// Return success response with model list
	h.WriteJSON(w, resp, http.StatusOK)
}

// ShowModel handles requests to show detailed information about a specific model.
//
// This endpoint retrieves comprehensive information about a model including:
//   - Configuration and metadata
//   - Hardware requirements and supported devices
//   - Available inference backends
//   - Prompt templates and system prompts
//
// HTTP Method: POST
// Endpoint: /api/models/show
//
// Request body:
//
//	{
//	  "model": "qwen2-0.5b"
//	}
//
// Response: 200 OK with model details
//
//	{
//	  "model": {
//	    "id": "qwen2-0.5b",
//	    "name": "Qwen2 0.5B",
//	    ...
//	  }
//	}
func (h *Handler) ShowModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get model from registry
	model, err := h.modelRegistry.Get(req.Model)
	if err != nil {
		h.WriteError(w, "Model not found: "+req.Model, http.StatusNotFound)
		return
	}

	// Get model spec for additional details
	spec := models.GetModelSpec(req.Model)
	
	// Try to read Modelfile first (user-editable layer)
	modelPath := h.getModelPath(h.config.Storage.ModelsDir, req.Model)
	modelfileContent, hasModelfile := h.readModelfile(modelPath)
	
	response := map[string]interface{}{
		"model":        model,
		"has_modelfile": hasModelfile,
	}
	
	// If Modelfile exists, include its content
	if hasModelfile {
		response["modelfile"] = modelfileContent
		response["source"] = "modelfile" // Indicates this is user-editable
	} else {
		response["source"] = "spec" // Indicates this is read-only from code
	}
	
	// Add spec details (always available as base info)
	if spec != nil {
		response["tag"] = spec.Tag
		response["parameters"] = spec.Parameters
		response["context_length"] = spec.ContextLength
		response["embedding_length"] = spec.EmbeddingLength
		response["required_vram"] = spec.RequiredVRAM
		response["license"] = spec.License
		response["homepage"] = spec.Homepage
		response["display_name"] = spec.DisplayName
		response["family"] = spec.Family
		response["description"] = spec.Description
		response["capabilities"] = spec.Capabilities
		
		// Convert backends to strings
		backends := make([]string, len(spec.Backends))
		for i, b := range spec.Backends {
			backends[i] = fmt.Sprintf("%s:%s", b.Type, b.Mode)
		}
		response["backends"] = backends
		
		// Add supported devices
		devices := make([]string, len(spec.SupportedDevices))
		for i, d := range spec.SupportedDevices {
			devices[i] = string(d)
		}
		response["supported_devices"] = devices
	}

	h.WriteJSON(w, response, http.StatusOK)
}

// enrichModelsWithDownloadStatus checks the download status of models.
//
// This method updates the Status field of each model by checking:
//   - If .download.lock exists: status = "downloading"
//   - If model directory exists with files: status = "downloaded"
//   - Otherwise: status = "not_downloaded"
//
// Parameters:
//   - models: Pointer to slice of models to enrich with download status
func (h *Handler) enrichModelsWithDownloadStatus(models *[]api.Model) {
	modelsDir := h.config.Storage.ModelsDir
	
	for i := range *models {
		// Construct paths for model directory and lock file
		// ModelScope downloads to: models_dir/Owner/Name structure
		modelPath := h.getModelPath(modelsDir, (*models)[i].Name)
		lockPath := filepath.Join(modelPath, ".download.lock")
		
		// Check if download is in progress
		if _, err := os.Stat(lockPath); err == nil {
			(*models)[i].Status = "downloading"
			continue
		}
		
		// Check if model directory exists and has files
		if info, err := os.Stat(modelPath); err == nil && info.IsDir() {
			// Check if directory has actual model files (not just empty)
			if h.hasModelFiles(modelPath) {
				(*models)[i].Status = "downloaded"
			} else {
				(*models)[i].Status = "not_downloaded"
			}
		} else {
			(*models)[i].Status = "not_downloaded"
		}
	}
}

// getModelPath constructs the full path where a model would be stored.
//
// ModelScope uses Owner/Name structure, so for model ID "qwen2-7b",
// we need to find the actual directory which might be like "Qwen/Qwen2-7B".
//
// Parameters:
//   - modelsDir: Base models directory
//   - modelName: Model name/ID
//
// Returns:
//   - Full path to the model directory
func (h *Handler) getModelPath(modelsDir, modelName string) string {
	// Try to get model spec to find the actual source ID
	spec := models.GetModelSpec(modelName)
	if spec != nil && spec.SourceID != "" {
		// SourceID is in format "Owner/Name"
		return filepath.Join(modelsDir, spec.SourceID)
	}
	
	// Fallback: assume model is stored directly under name
	return filepath.Join(modelsDir, modelName)
}

// hasModelFiles checks if a directory contains actual model files.
//
// This prevents marking empty directories as "downloaded".
//
// Parameters:
//   - dirPath: Directory path to check
//
// Returns:
//   - true if directory contains at least one regular file
func (h *Handler) hasModelFiles(dirPath string) bool {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	
	// Look for at least one regular file
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			// Found a non-hidden regular file
			return true
		}
	}
	
	return false
}

// readModelfile reads the Modelfile from a model directory.
//
// This function attempts to read the user-editable Modelfile that was
// generated during model download. The Modelfile represents the user's
// customization layer on top of the base model specification.
//
// Parameters:
//   - modelPath: Path to the model directory
//
// Returns:
//   - content: The Modelfile content as a string
//   - exists: Whether the Modelfile exists
func (h *Handler) readModelfile(modelPath string) (string, bool) {
	modelfilePath := filepath.Join(modelPath, "Modelfile")
	
	// Check if Modelfile exists
	if _, err := os.Stat(modelfilePath); os.IsNotExist(err) {
		return "", false
	}
	
	// Read Modelfile content
	content, err := os.ReadFile(modelfilePath)
	if err != nil {
		logger.Warn("Failed to read Modelfile at %s: %v", modelfilePath, err)
		return "", false
	}
	
	return string(content), true
}

// UpdateModelfile handles Modelfile update requests.
//
// This endpoint allows users to update a model's Modelfile with new content.
// It validates the content before writing to ensure data integrity.
//
// HTTP Method: POST
// Endpoint: /api/models/update-modelfile
//
// Request body:
//
//	{
//	  "model": "qwen2-0.5b",
//	  "content": "# Modelfile\nFROM qwen2-0.5b\n..."
//	}
//
// Response: 200 OK on success, 400/404 on error
func (h *Handler) UpdateModelfile(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method
	if r.Method != http.MethodPost {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		Model   string `json:"model"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate model exists
	_, err := h.modelRegistry.Get(req.Model)
	if err != nil {
		h.WriteError(w, "Model not found: "+req.Model, http.StatusNotFound)
		return
	}

	// Get model path
	modelPath := h.getModelPath(h.config.Storage.ModelsDir, req.Model)
	modelfilePath := filepath.Join(modelPath, "Modelfile")

	// Check if model has been downloaded
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		h.WriteError(w, fmt.Sprintf("Model %s has not been downloaded yet", req.Model), http.StatusBadRequest)
		return
	}

	// Validate Modelfile content (server-side)
	if err := validateModelfileContent(req.Content); err != nil {
		h.WriteError(w, fmt.Sprintf("Validation failed: %v", err), http.StatusBadRequest)
		return
	}

	// Write new content to Modelfile
	if err := os.WriteFile(modelfilePath, []byte(req.Content), 0644); err != nil {
		h.WriteError(w, fmt.Sprintf("Failed to write Modelfile: %v", err), http.StatusInternalServerError)
		return
	}

	logger.Info("Updated Modelfile for %s", req.Model)

	// Return success
	h.WriteJSON(w, map[string]string{
		"status":  "success",
		"message": "Modelfile updated successfully",
	}, http.StatusOK)
}

// validateModelfileContent validates the Modelfile content.
//
// This performs server-side validation to ensure data integrity.
//
// Validation rules:
//   - Must not be empty
//   - Must contain a FROM directive
//   - FROM directive must specify a model name
//   - Basic syntax validation
//
// Parameters:
//   - content: The Modelfile content to validate
//
// Returns:
//   - nil if valid
//   - error describing validation failure
func validateModelfileContent(content string) error {
	// Trim whitespace
	content = strings.TrimSpace(content)

	// Check if empty
	if content == "" {
		return fmt.Errorf("Modelfile cannot be empty")
	}

	// Parse lines and look for FROM directive
	lines := strings.Split(content, "\n")
	hasFrom := false
	fromLine := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for FROM directive
		if strings.HasPrefix(line, "FROM ") {
			hasFrom = true
			fromLine = line
			break
		}
	}

	// FROM is required
	if !hasFrom {
		return fmt.Errorf("Modelfile must contain a FROM directive")
	}

	// Validate FROM syntax
	parts := strings.Fields(fromLine)
	if len(parts) < 2 {
		return fmt.Errorf("FROM directive must specify a model name")
	}

	modelName := parts[1]
	if modelName == "" {
		return fmt.Errorf("FROM directive has empty model name")
	}

	// Additional validation could be added here:
	// - Check if referenced model exists
	// - Validate parameter syntax
	// - Check for dangerous directives

	return nil
}

