// Package handlers - models.go implements the model listing endpoint.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// Get model spec from registry
	spec := models.GetModelSpec(req.Model)
	if spec == nil {
		h.WriteError(w, "Model not found: "+req.Model, http.StatusNotFound)
		return
	}

	// Try to read Modelfile (user-editable, takes priority)
	modelPath := h.getModelPath(h.config.Storage.ModelsDir, req.Model)
	modelfileContent, hasModelfile := h.readModelfile(modelPath)

	// Build response following Ollama format
	response := make(map[string]interface{})
	response["model_id"] = spec.ID

	// Use ModelSpec values first (most accurate)
	if spec.Parameters > 0 {
		response["parameters"] = spec.Parameters
	}
	if spec.ContextLength > 0 {
		response["context_length"] = float64(spec.ContextLength)
	}
	if spec.EmbeddingLength > 0 {
		response["embedding_length"] = float64(spec.EmbeddingLength)
	}
	
	// Try to read config.json from model directory for additional info
	configData := h.readModelConfig(modelPath)
	
	// Extract information from config.json (fallback or supplement)
	if configData != nil {
		// Model architecture info
		if arch, ok := configData["architectures"].([]interface{}); ok && len(arch) > 0 {
			response["architecture"] = arch[0]
		}
		
		if family, ok := configData["model_type"].(string); ok {
			response["family"] = family
		}
		
		// Use config.json values only if not set in ModelSpec
		if _, hasParams := response["parameters"]; !hasParams {
			if hiddenSize, ok := configData["hidden_size"].(float64); ok {
				if numLayers, ok := configData["num_hidden_layers"].(float64); ok {
					// Rough estimation: params ≈ hidden_size² × num_layers × 12 / 1e9
					params := hiddenSize * hiddenSize * numLayers * 12 / 1e9
					response["parameters"] = params
				}
			}
		}
		
		if _, hasCtx := response["context_length"]; !hasCtx {
			if maxPos, ok := configData["max_position_embeddings"].(float64); ok {
				response["context_length"] = maxPos
			}
		}
		
		if _, hasEmb := response["embedding_length"]; !hasEmb {
			if hiddenSize, ok := configData["hidden_size"].(float64); ok {
				response["embedding_length"] = hiddenSize
			}
		}
		
		// Quantization
		if quant, ok := configData["quantization_config"].(map[string]interface{}); ok {
			if bits, ok := quant["bits"].(float64); ok {
				response["quantization"] = fmt.Sprintf("Q%d", int(bits))
			}
		}
	}
	
	// Read LICENSE file if exists
	if licenseContent := h.readLicenseFile(modelPath); licenseContent != "" {
		response["license"] = licenseContent
	}
	
	// Read capabilities from model metadata
	if metadata := h.readModelMetadata(modelPath); metadata != nil {
		if caps, ok := metadata["capabilities"].([]interface{}); ok {
			response["capabilities"] = caps
		}
		// Only override license if not found in LICENSE file
		if _, hasLicense := response["license"]; !hasLicense {
			if license, ok := metadata["license"].(string); ok {
				response["license"] = license
			}
		}
	}
	
	// Fallback to default values if not found in files
	if _, hasArch := response["architecture"]; !hasArch {
		response["architecture"] = "transformer"
	}
	if _, hasCaps := response["capabilities"]; !hasCaps {
		response["capabilities"] = []string{"completion"}
	}

	// Read generation_config.json for inference parameters (default values)
	if genConfig := h.readGenerationConfig(modelPath); genConfig != nil {
		// Convert generation config to inference parameters
		inferenceParams := make(map[string]interface{})
		
		// Common generation parameters
		if temp, ok := genConfig["temperature"].(float64); ok {
			inferenceParams["temperature"] = temp
		}
		if topP, ok := genConfig["top_p"].(float64); ok {
			inferenceParams["top_p"] = topP
		}
		if topK, ok := genConfig["top_k"].(float64); ok {
			inferenceParams["top_k"] = topK
		}
		// Support both max_length and max_new_tokens
		if maxLen, ok := genConfig["max_length"].(float64); ok {
			inferenceParams["max_length"] = int(maxLen)
		} else if maxNew, ok := genConfig["max_new_tokens"].(float64); ok {
			inferenceParams["max_new_tokens"] = int(maxNew)
		}
		if repPenalty, ok := genConfig["repetition_penalty"].(float64); ok {
			inferenceParams["repetition_penalty"] = repPenalty
		}
		if numCtx, ok := genConfig["num_ctx"].(float64); ok {
			inferenceParams["num_ctx"] = int(numCtx)
		}
		if doSample, ok := genConfig["do_sample"].(bool); ok {
			inferenceParams["do_sample"] = doSample
		}
		
		// Stop sequences
		if stop, ok := genConfig["stop"].([]interface{}); ok && len(stop) > 0 {
			// Join stop sequences with comma or use first one
			if stopStr, ok := stop[0].(string); ok {
				inferenceParams["stop"] = stopStr
			}
		} else if stopStr, ok := genConfig["stop"].(string); ok {
			inferenceParams["stop"] = stopStr
		}
		
		if len(inferenceParams) > 0 {
			response["inference_parameters"] = inferenceParams
		}
	}
	
	// Parse Modelfile if it exists (priority over generation_config.json)
	if hasModelfile {
		response["modelfile"] = modelfileContent

		// Extract directives from Modelfile
		if template := h.extractDirectiveFromModelfile(modelfileContent, "TEMPLATE"); template != "" {
			response["template"] = template
		}

		if system := h.extractDirectiveFromModelfile(modelfileContent, "SYSTEM"); system != "" {
			response["system"] = system
		}

		// Extract PARAMETER directives (overrides generation_config.json)
		params := h.extractParametersFromModelfile(modelfileContent)
		if len(params) > 0 {
			// Merge with existing parameters, Modelfile takes priority
			if existingParams, ok := response["inference_parameters"].(map[string]interface{}); ok {
				for k, v := range params {
					existingParams[k] = v
				}
				response["inference_parameters"] = existingParams
			} else {
				response["inference_parameters"] = params
			}
		}
	}

	// Fallback defaults if not in Modelfile
	if _, hasTemplate := response["template"]; !hasTemplate {
		response["template"] = "{{ .System }}\n{{ .Prompt }}"
	}

	if _, hasSystem := response["system"]; !hasSystem {
		response["system"] = "You are a helpful AI assistant."
	}

	h.WriteJSON(w, response, http.StatusOK)
}

// extractDirectiveFromModelfile extracts a directive value from Modelfile
func (h *Handler) extractDirectiveFromModelfile(content, directive string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, directive+" ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, directive+" "))

			// Handle triple quotes
			if strings.HasPrefix(rest, "\"\"\"") {
				rest = strings.TrimPrefix(rest, "\"\"\"")

				// Single line
				if strings.Contains(rest, "\"\"\"") {
					return strings.Split(rest, "\"\"\"")[0]
				}

				// Multi-line
				var parts []string
				if rest != "" {
					parts = append(parts, rest)
				}

				for j := i + 1; j < len(lines); j++ {
					if strings.Contains(lines[j], "\"\"\"") {
						ending := strings.Split(lines[j], "\"\"\"")[0]
						if ending != "" {
							parts = append(parts, ending)
						}
						break
					}
					parts = append(parts, lines[j])
				}
				return strings.Join(parts, "\n")
			}

			// Handle single quotes
			if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
				return strings.Trim(rest, "\"")
			}

			return rest
		}
	}

	return ""
}

// extractParametersFromModelfile extracts PARAMETER directives from Modelfile
func (h *Handler) extractParametersFromModelfile(content string) map[string]interface{} {
	params := make(map[string]interface{})
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "PARAMETER ") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "PARAMETER "))
			parts := strings.Fields(rest)

			if len(parts) >= 2 {
				key := parts[0]
				value := strings.Join(parts[1:], " ")
				value = strings.Trim(value, "\"")
				params[key] = value
			}
		}
	}

	return params
}

// readModelConfig reads the config.json file from model directory
func (h *Handler) readModelConfig(modelPath string) map[string]interface{} {
	configPath := filepath.Join(modelPath, "config.json")
	
	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil
	}
	
	// Read file
	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.Warn("Failed to read config.json: %v", err)
		return nil
	}
	
	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		logger.Warn("Failed to parse config.json: %v", err)
		return nil
	}
	
	return config
}

// readGenerationConfig reads the generation_config.json file from model directory
func (h *Handler) readGenerationConfig(modelPath string) map[string]interface{} {
	configPath := filepath.Join(modelPath, "generation_config.json")
	
	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil
	}
	
	// Read file
	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.Warn("Failed to read generation_config.json: %v", err)
		return nil
	}
	
	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		logger.Warn("Failed to parse generation_config.json: %v", err)
		return nil
	}
	
	return config
}

// readModelMetadata reads custom metadata from model directory
// This can include capabilities, license, and other custom fields
func (h *Handler) readModelMetadata(modelPath string) map[string]interface{} {
	metadataPath := filepath.Join(modelPath, "metadata.json")
	
	// Check if file exists
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		return nil
	}
	
	// Read file
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil
	}
	
	// Parse JSON
	var metadata map[string]interface{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil
	}
	
	return metadata
}

// readLicenseFile reads the LICENSE file from model directory
func (h *Handler) readLicenseFile(modelPath string) string {
	// Try common license file names
	licenseNames := []string{"LICENSE", "LICENSE.txt", "LICENSE.md", "license", "license.txt"}
	
	for _, name := range licenseNames {
		licensePath := filepath.Join(modelPath, name)
		
		// Check if file exists
		if _, err := os.Stat(licensePath); err == nil {
			// Read file
			data, err := os.ReadFile(licensePath)
			if err != nil {
				continue
			}
			return string(data)
		}
	}
	
	return ""
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
				// For downloaded models, populate metadata and modification time
			if spec := h.modelRegistry.GetSpec((*models)[i].Name); spec != nil {
				(*models)[i].Source = spec.SourceID
				
				if spec.Tag != "" {
					(*models)[i].Tag = spec.Tag
				} else {
					(*models)[i].Tag = "latest"
				}
				
				// Get first available engine from first supported device
				for _, engines := range spec.SupportedDevices {
					if len(engines) > 0 {
						backend := engines[0]
						(*models)[i].DefaultEngine = fmt.Sprintf("%s:%s", backend.Type, backend.Mode)
						break
					}
				}
			}
				(*models)[i].ModifiedAt = info.ModTime().Format(time.RFC3339)
			} else {
				(*models)[i].Status = "not_downloaded"
			}
		} else {
			(*models)[i].Status = "not_downloaded"
		}
		
		// For NOT downloaded models, populate metadata from ModelSpec
		if (*models)[i].Status == "not_downloaded" {
			if spec := h.modelRegistry.GetSpec((*models)[i].Name); spec != nil {
				(*models)[i].Source = spec.SourceID
				
				if spec.Tag != "" {
					(*models)[i].Tag = spec.Tag
				} else {
					(*models)[i].Tag = "latest"
				}
				
				// Get first available engine from first supported device
				for _, engines := range spec.SupportedDevices {
					if len(engines) > 0 {
						backend := engines[0]
						(*models)[i].DefaultEngine = fmt.Sprintf("%s:%s", backend.Type, backend.Mode)
						break
					}
				}
			}
		}
	}
}

// getModelPath constructs the full path where a model would be stored.
//
// New directory structure: models/{model_id}/{tag}
// Example: ~/.xw/models/qwen2-0.5b/latest
//
// Parameters:
//   - modelsDir: Base models directory
//   - modelName: Model ID (e.g., "qwen2-0.5b")
//
// Returns:
//   - Full path to the model directory (defaults to "latest" tag)
func (h *Handler) getModelPath(modelsDir, modelName string) string {
	// Use the new directory structure: models/{model_id}/{tag}
	// Default to "latest" tag
	return filepath.Join(modelsDir, modelName, "latest")
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

// ListDownloadedModels handles requests to list downloaded models.
//
// This endpoint scans the models directory and returns information about
// models that have been downloaded and are available locally.
//
// HTTP Method: GET
// Endpoint: /api/models/downloaded
//
// Response: 200 OK with JSON
//
//	{
//	  "models": [
//	    {
//	      "name": "Qwen/Qwen2.5-7B-Instruct",
//	      "tag": "latest",
//	      "size": 15240000000,
//	      "default_engine": "vllm:docker",
//	      "modified": "2024-01-28T10:00:00Z"
//	    }
//	  ]
//	}
func (h *Handler) ListDownloadedModels(w http.ResponseWriter, r *http.Request) {
	// Validate HTTP method
	if r.Method != http.MethodGet {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get models directory from config
	modelsDir := h.config.Storage.ModelsDir
	if modelsDir == "" {
		modelsDir = filepath.Join(os.Getenv("HOME"), ".xw", "models")
	}

	// Scan models directory
	downloadedModels := []map[string]interface{}{}
	
	// Check if directory exists
	if _, err := os.Stat(modelsDir); os.IsNotExist(err) {
		// No models directory, return empty list
		h.WriteJSON(w, map[string]interface{}{
			"models": downloadedModels,
		}, http.StatusOK)
		return
	}

	// Read directory entries
	entries, err := os.ReadDir(modelsDir)
	if err != nil {
		logger.Error("Failed to read models directory: %v", err)
		h.WriteError(w, fmt.Sprintf("Failed to read models directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Process models directory with new structure: models/{model_id}/{tag}
	// Scan for model ID directories (first level)
	for _, modelIDEntry := range entries {
		// Skip hidden files and directories (starting with .)
		if strings.HasPrefix(modelIDEntry.Name(), ".") {
			continue
		}
		
		if !modelIDEntry.IsDir() {
			continue
		}

		// modelIDEntry.Name() is the model ID (e.g., "qwen2-0.5b")
		modelID := modelIDEntry.Name()
		
		// Look up model spec in registry by ID
		spec := models.GetModelSpec(modelID)
		if spec == nil {
			logger.Warn("Model directory %s not found in registry, skipping", modelID)
			continue
		}

		// Read model ID directory to find tag directories (second level)
		modelIDPath := filepath.Join(modelsDir, modelID)
		tagEntries, err := os.ReadDir(modelIDPath)
		if err != nil {
			logger.Warn("Failed to read model directory %s: %v", modelID, err)
			continue
		}

		// Scan for tag directories (second level)
		for _, tagEntry := range tagEntries {
			// Skip hidden directories
			if strings.HasPrefix(tagEntry.Name(), ".") {
				continue
			}
			
			if !tagEntry.IsDir() {
				continue
			}

			tag := tagEntry.Name()
			modelPath := filepath.Join(modelIDPath, tag)

			// Verify directory contains model files
			if !h.hasModelFiles(modelPath) {
				continue
			}

			// Get directory size
			size, err := getDirSize(modelPath)
			if err != nil {
				logger.Warn("Failed to get size for %s/%s: %v", modelID, tag, err)
				size = 0
			}

			// Get modification time
			info, err := tagEntry.Info()
			if err != nil {
				logger.Warn("Failed to get info for %s/%s: %v", modelID, tag, err)
				continue
			}

		// Get default engine from model spec (first engine of first device)
		defaultEngine := "vllm:docker" // fallback
		for _, engines := range spec.SupportedDevices {
			if len(engines) > 0 {
				backend := engines[0]
				defaultEngine = string(backend.Type) + ":" + string(backend.Mode)
				break
			}
		}

			modelInfo := map[string]interface{}{
				"id":             spec.ID,        // Model ID (e.g., "qwen2-0.5b")
				"source":         spec.SourceID,  // SourceID for downloading (e.g., "Qwen/Qwen2-0.5B")
				"tag":            tag,            // Version tag (e.g., "latest", "v1.0")
				"size":           float64(size),
				"default_engine": defaultEngine,
				"modified":       info.ModTime().Format(time.RFC3339),
			}

			downloadedModels = append(downloadedModels, modelInfo)
		}
	}

	// Return response
	h.WriteJSON(w, map[string]interface{}{
		"models": downloadedModels,
	}, http.StatusOK)
}

// getDirSize calculates the total size of a directory recursively.
func getDirSize(path string) (int64, error) {
	var size int64
	
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	
	return size, err
}

