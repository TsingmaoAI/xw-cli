// Package config - model_config.go implements model configuration loading and management.
//
// This module provides a flexible configuration system for AI model definitions.
// Model configurations are loaded from YAML files, allowing easy addition of new
// models without code changes.
//
// The configuration system supports model metadata, hardware compatibility,
// runtime backends, and quantization strategies.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	
	"github.com/tsingmaoai/xw-cli/internal/logger"
)


// ModelConfig defines configuration for an AI model.
//
// This structure represents a model that can be deployed and served by xw.
type ModelConfig struct {
	// Model identification
	
	// ModelID is the unique identifier for this model
	// Convention: lowercase, hyphen-separated (e.g., "qwen2-7b")
	ModelID string `yaml:"model_id"`
	
	// SourceID is the model ID on the source platform
	// Examples: "qwen/Qwen2-7B" (ModelScope), "Qwen/Qwen2-7B" (HuggingFace)
	SourceID string `yaml:"source_id"`
	
	// Model specifications
	
	// Parameters is the model size in billions of parameters
	// Example: 7.0 for 7B model
	Parameters float64 `yaml:"parameters,omitempty"`
	
	// ContextLength is the maximum context window size in tokens
	ContextLength int `yaml:"context_length,omitempty"`
	
	// Deployment configuration
	
	// SupportedDevices maps device types to their supported engines
	// Key: device config_key (e.g., "ascend-910b")
	// Value: list of engines in priority order (e.g., ["vllm:docker", "mindie:docker"])
	// Example:
	//   ascend-910b:
	//     - vllm:docker
	//     - mindie:docker
	SupportedDevices map[string][]string `yaml:"supported_devices"`
	
	// Tag specifies the model variant (e.g., "main", "int8", "fp16")
	Tag string `yaml:"tag,omitempty"`
	
	// Capabilities lists the model's supported features
	// Common values: "completion", "vision", "tool_use", "function_calling"
	Capabilities []string `yaml:"capabilities,omitempty"`
}

// ModelsConfig is the root configuration structure for model definitions.
//
// This structure maps to the YAML configuration file and contains all
// model definitions available in the system.
type ModelsConfig struct {
	// Version specifies the configuration schema version
	// Used for compatibility checking and migration
	Version string `yaml:"version"`
	
	// Models contains all available model configurations
	Models []ModelConfig `yaml:"models"`
	
	// ModelGroups provides logical grouping of related models (optional)
	// Example: {"qwen2": ["qwen2-0.5b", "qwen2-7b", "qwen2-72b"]}
	ModelGroups map[string][]string `yaml:"model_groups,omitempty"`
}

// ModelConfigLoader handles loading and caching of model configurations.
//
// The loader implements singleton pattern with lazy initialization.
// Configurations are loaded once and cached for the lifetime of the application.
//
// Thread Safety: All methods are safe for concurrent use.
type ModelConfigLoader struct {
	mu     sync.RWMutex
	config *ModelsConfig
	loaded bool
}

var (
	// modelConfigLoader is the global singleton instance
	modelConfigLoader = &ModelConfigLoader{}
	
	// defaultModelConfigPath is the default location for model configuration
	defaultModelConfigPath = "/etc/xw/models.yaml"
)

// LoadModelsConfig loads model configuration from the specified file path.
//
// This method reads and parses the YAML configuration file, validating the
// structure and content. If no path is provided, it uses the default location.
//
// The configuration is cached after first load. Subsequent calls return the
// cached configuration without re-reading the file.
//
// Configuration File Location:
//   - Provided configPath parameter, or default: /etc/xw/models.yaml
//
// Parameters:
//   - configPath: Optional path to configuration file (empty string for default)
//
// Returns:
//   - Pointer to loaded ModelsConfig
//   - Error if file cannot be read, parsed, or validated
//
// Example:
//
//	config, err := LoadModelsConfig("")
//	if err != nil {
//	    log.Fatalf("Failed to load model config: %v", err)
//	}
//	for _, model := range config.Models {
//	    fmt.Printf("Loaded model: %s (%s)\n", model.ModelName, model.ModelID)
//	}
func LoadModelsConfig(configPath string) (*ModelsConfig, error) {
	modelConfigLoader.mu.Lock()
	defer modelConfigLoader.mu.Unlock()
	
	// Return cached config if already loaded
	if modelConfigLoader.loaded {
		logger.Debug("Using cached model configuration")
		return modelConfigLoader.config, nil
	}
	
	// Determine config file path
	path := configPath
	if path == "" {
		path = defaultModelConfigPath
		logger.Debug("Using default model config path: %s", path)
	}
	
	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("model configuration file not found: %s", path)
	}
	
	// Read configuration file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read model config file %s: %w", path, err)
	}
	
	// Parse YAML
	var config ModelsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse model config YAML: %w", err)
	}
	
	// Validate configuration
	if err := validateModelsConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid model configuration: %w", err)
	}
	
	// Cache the loaded configuration
	modelConfigLoader.config = &config
	modelConfigLoader.loaded = true
	
	logger.Info("Loaded model configuration: %d model(s)", len(config.Models))
	
	return &config, nil
}

// GetModelsConfig returns the cached model configuration.
//
// This method provides access to the previously loaded configuration without
// re-reading the file. If configuration hasn't been loaded yet, it loads it
// from the default location.
//
// Returns:
//   - Pointer to ModelsConfig
//   - Error if configuration not loaded and loading fails
func GetModelsConfig() (*ModelsConfig, error) {
	modelConfigLoader.mu.RLock()
	if modelConfigLoader.loaded {
		config := modelConfigLoader.config
		modelConfigLoader.mu.RUnlock()
		return config, nil
	}
	modelConfigLoader.mu.RUnlock()
	
	// Not loaded yet, load with default path
	return LoadModelsConfig("")
}

// ClearModelsConfigCache clears the model configuration cache.
//
// This function invalidates the cached model configuration, forcing
// the next LoadModelsConfig call to read from disk.
func ClearModelsConfigCache() {
	modelConfigLoader.mu.Lock()
	modelConfigLoader.loaded = false
	modelConfigLoader.config = nil
	modelConfigLoader.mu.Unlock()
	logger.Debug("Models configuration cache cleared")
}

// ReloadModelsConfig forces a reload of the model configuration.
//
// This method clears the cache and re-reads the configuration file.
// Useful for applying configuration changes without restarting the application.
//
// Parameters:
//   - configPath: Optional path to configuration file (empty for default)
//
// Returns:
//   - Pointer to reloaded ModelsConfig
//   - Error if reload fails
func ReloadModelsConfig(configPath string) (*ModelsConfig, error) {
	ClearModelsConfigCache()
	logger.Info("Reloading model configuration")
	return LoadModelsConfig(configPath)
}

// validateModelsConfig performs validation on the loaded configuration.
//
// Validation checks:
//   - Version field is present
//   - At least one model is defined
//   - Each model has required fields
//   - No duplicate model IDs
//   - Runtime configs are valid
//   - Hardware requirements are reasonable
//
// Parameters:
//   - config: Configuration to validate
//
// Returns:
//   - nil if valid
//   - Error describing validation failure
func validateModelsConfig(config *ModelsConfig) error {
	if config.Version == "" {
		return fmt.Errorf("configuration version is required")
	}
	
	if len(config.Models) == 0 {
		return fmt.Errorf("at least one model must be defined")
	}
	
	// Track model IDs to detect duplicates
	modelIDs := make(map[string]bool)
	
	for i, model := range config.Models {
		if model.ModelID == "" {
			return fmt.Errorf("model[%d]: model_id is required", i)
		}
		
		// Check for duplicate model IDs
		if modelIDs[model.ModelID] {
			return fmt.Errorf("duplicate model_id: %s", model.ModelID)
		}
		modelIDs[model.ModelID] = true
		
		// Validate source ID
		if model.SourceID == "" {
			return fmt.Errorf("model %s: source_id is required", model.ModelID)
		}
		
		// Validate supported devices
		if len(model.SupportedDevices) == 0 {
			return fmt.Errorf("model %s: at least one supported device is required", model.ModelID)
		}
		
		// Validate each device's engines
		for device, engines := range model.SupportedDevices {
			if len(engines) == 0 {
				return fmt.Errorf("model %s, device %s: at least one engine is required", model.ModelID, device)
			}
			
			for j, engine := range engines {
				if engine == "" {
					return fmt.Errorf("model %s, device %s, engine[%d]: engine string is required", model.ModelID, device, j)
				}
				// Basic format validation (should be "backend:mode")
				if !strings.Contains(engine, ":") {
					return fmt.Errorf("model %s, device %s, engine[%d]: invalid format '%s', expected 'backend:mode' (e.g., 'vllm:docker')", 
						model.ModelID, device, j, engine)
				}
			}
		}
	}
	
	return nil
}

// FindModelByID searches for a model by its ID.
//
// This is the primary method for looking up model configuration.
//
// Parameters:
//   - config: ModelsConfig to search
//   - modelID: The model_id to find
//
// Returns:
//   - Pointer to ModelConfig if found
//   - nil if not found
func FindModelByID(config *ModelsConfig, modelID string) *ModelConfig {
	for i := range config.Models {
		if config.Models[i].ModelID == modelID {
			return &config.Models[i]
		}
	}
	return nil
}

// FindModelsByDeviceType returns all models compatible with a device type.
//
// This method is used to filter models based on hardware availability.
//
// Parameters:
//   - config: ModelsConfig to search
//   - deviceConfigKey: Device config key to filter by
//
// Returns:
//   - Slice of pointers to compatible ModelConfig objects
func FindModelsByDeviceType(config *ModelsConfig, deviceConfigKey string) []*ModelConfig {
	var models []*ModelConfig
	for i := range config.Models {
		// Check if device exists in the map
		if _, exists := config.Models[i].SupportedDevices[deviceConfigKey]; exists {
			models = append(models, &config.Models[i])
		}
	}
	return models
}


// GetAllModelIDs returns a list of all model IDs defined in the configuration.
//
// Useful for validation and displaying available models.
//
// Parameters:
//   - config: ModelsConfig to extract IDs from
//
// Returns:
//   - Slice of model ID strings
func GetAllModelIDs(config *ModelsConfig) []string {
	ids := make([]string, len(config.Models))
	for i, model := range config.Models {
		ids[i] = model.ModelID
	}
	return ids
}

// SaveModelsConfig writes a ModelsConfig to a YAML file.
//
// This method is primarily used for:
//   - Generating template configuration files
//   - Exporting modified configurations
//   - Testing and validation
//
// Parameters:
//   - config: Configuration to save
//   - path: File path to write to
//
// Returns:
//   - Error if file cannot be written
func SaveModelsConfig(config *ModelsConfig, path string) error {
	// Validate before saving
	if err := validateModelsConfig(config); err != nil {
		return fmt.Errorf("cannot save invalid configuration: %w", err)
	}
	
	// Marshal to YAML with nice formatting
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config to YAML: %w", err)
	}
	
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	
	// Write file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}
	
	logger.Info("Saved model configuration to %s", path)
	return nil
}

