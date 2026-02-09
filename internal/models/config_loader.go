// Package models - config_loader.go provides configuration-based model loading.
//
// This module bridges the configuration system with the model registry,
// converting YAML-based model definitions into ModelSpec instances.
package models

import (
	"fmt"
	"strings"
	
	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// LoadModelsFromConfig loads model specifications from the configuration file.
//
// This function reads the model configuration and converts it into ModelSpec
// format used by the model registry. It provides a bridge between the
// configuration system and the existing model registry logic.
//
// Parameters:
//   - configPath: Optional path to model configuration file (empty for default)
//
// Returns:
//   - Slice of ModelSpec instances
//   - Error if configuration cannot be loaded or is invalid
//
// Example:
//
//	specs, err := LoadModelsFromConfig("")
//	if err != nil {
//	    log.Fatalf("Failed to load models: %v", err)
//	}
//	registry := NewRegistry()
//	for _, spec := range specs {
//	    registry.RegisterModelSpec(&spec)
//	}
func LoadModelsFromConfig(configPath string) ([]ModelSpec, error) {
	// Load model configuration
	modConfig, err := config.LoadModelsConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load model configuration: %w", err)
	}
	
	// Convert configuration to ModelSpec format
	var specs []ModelSpec
	
	for _, model := range modConfig.Models {
		spec := ModelSpec{
			ID:               model.ModelID,
			SourceID:         model.SourceID,
			Parameters:       model.Parameters,
			ContextLength:    model.ContextLength,
			Tag:              model.Tag,
			Capabilities:     model.Capabilities,
			SupportedDevices: make(map[api.DeviceType][]BackendOption),
		}
		
		// Convert supported devices and their engines
		// Format: map[device_type][]engine_strings
		for deviceStr, engines := range model.SupportedDevices {
			deviceType := api.DeviceType(deviceStr)
			var backendOptions []BackendOption
			
			// Parse each engine string for this device
			for _, engine := range engines {
				backendOpt, err := parseEngine(engine)
				if err != nil {
					logger.Warn("Invalid engine format '%s' for model %s device %s: %v, skipping", 
						engine, model.ModelID, deviceStr, err)
					continue
				}
				backendOptions = append(backendOptions, backendOpt)
			}
			
			// Store device's engines in the map
			if len(backendOptions) > 0 {
				spec.SupportedDevices[deviceType] = backendOptions
			}
		}
		
		specs = append(specs, spec)
	}
	
	logger.Debug("Loaded %d model(s) from configuration", len(specs))
	return specs, nil
}

// parseEngine parses engine string in format "backend:mode" (e.g., "vllm:docker")
func parseEngine(engine string) (BackendOption, error) {
	parts := strings.SplitN(engine, ":", 2)
	if len(parts) != 2 {
		return BackendOption{}, fmt.Errorf("invalid engine format, expected 'backend:mode' (e.g., 'vllm:docker')")
	}
	
	backendType := api.BackendType(parts[0])
	deploymentMode := api.DeploymentMode(parts[1])
	
	// Validate backend type (only check non-empty, actual availability checked at runtime)
	if backendType == "" {
		return BackendOption{}, fmt.Errorf("backend type cannot be empty")
	}
	
	// Validate deployment mode
	switch deploymentMode {
	case api.DeploymentModeDocker, api.DeploymentModeNative:
		// Valid mode
	default:
		return BackendOption{}, fmt.Errorf("unknown deployment mode: %s", deploymentMode)
	}
	
	return BackendOption{
		Type: backendType,
		Mode: deploymentMode,
	}, nil
}

// LoadAndRegisterModelsFromConfig loads models from configuration and registers them.
//
// This function loads models from the configuration file and registers them
// with the global model registry. It should be called during application
// initialization to populate the registry with models from configuration.
//
// Parameters:
//   - configPath: Optional path to model configuration file (empty for default)
//
// Returns:
//   - Error if configuration loading fails
//
// Example:
//
//	if err := LoadAndRegisterModelsFromConfig(""); err != nil {
//	    log.Fatalf("Failed to load models: %v", err)
//	}
func LoadAndRegisterModelsFromConfig(configPath string) error {
	// Load model specs from configuration
	specs, err := LoadModelsFromConfig(configPath)
	if err != nil {
		return err
	}
	
	// Register all models with the global registry
	registeredCount := 0
	for i := range specs {
		RegisterModelSpec(&specs[i])
		registeredCount++
	}
	
	logger.Info("Loaded and registered %d model(s) from configuration", registeredCount)
	return nil
}

