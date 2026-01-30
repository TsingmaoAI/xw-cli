package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

const (
	// RuntimeImagesFileName is the name of the runtime images configuration file
	RuntimeImagesFileName = "runtime_images.yaml"
)

// RuntimeImagesConfig represents the configuration for Docker images
// used by different chip types, inference engines, and CPU architectures.
//
// Structure:
//   - Map of chip model to its engine configurations
//     Key: Chip model name (e.g., "ascend-910b", "ascend-310p")
//     Value: Map of engine name to architecture-specific images
//
// Example YAML:
//   ascend-910b:
//     vllm:
//       arm64: quay.io/ascend/vllm-ascend:v0.11.0rc0-arm64
//       amd64: quay.io/ascend/vllm-ascend:v0.11.0rc0-amd64
//     mindie:
//       arm64: harbor.tsingmao.com/xuanwu/mindie:2.2.RC1-800I-A2-arm64
//       amd64: harbor.tsingmao.com/xuanwu/mindie:2.2.RC1-800I-A2-amd64
type RuntimeImagesConfig map[string]map[string]map[string]string

// GetDefaultRuntimeImagesConfig returns the default runtime images configuration.
//
// This function defines the default Docker images for all supported combinations
// of chip models, inference engines, and CPU architectures.
//
// Supported Chips:
//   - ascend-910b: Huawei Ascend 910B (training and inference)
//   - ascend-310p: Huawei Ascend 310P (inference only)
//
// Supported Engines:
//   - vllm: vLLM inference engine
//   - mindie: MindIE inference engine (Huawei's optimized engine)
//
// Supported Architectures:
//   - arm64: ARM 64-bit (aarch64)
//   - amd64: x86 64-bit (x86_64)
//
// Returns:
//   - Default RuntimeImagesConfig with all chip/engine/architecture combinations
func GetDefaultRuntimeImagesConfig() RuntimeImagesConfig {
	return RuntimeImagesConfig{
		// Ascend 910B configuration
		"ascend-910b": {
			"vllm": {
				"arm64": "quay.io/ascend/vllm-ascend:v0.11.0rc0-arm64",
				"amd64": "NONE",
			},
			"mindie": {
				"arm64": "harbor.tsingmao.com/xuanwu/mindie:2.2.RC1-800I-A2-py311-openeuler24.03-lts-arm64",
				"amd64": "NONE",
			},
			"mlguider": {
				"arm64": "harbor.tsingmao.com/mlguider/release:0123-xw-arm64",
				"amd64": "NONE",
			},
		},
		
		// Ascend 310P configuration
		"ascend-310p": {
			"vllm": {
				"arm64": "quay.io/ascend/vllm-ascend:main-310p",
				"amd64": "NONE",
			},
			"mindie": {
				"arm64": "harbor.tsingmao.com/xuanwu/mindie:2.3.0-300I-Duo-py311-openeuler24.03-lts",
				"amd64": "NONE",
			},
			"mlguider": {
				"arm64": "harbor.tsingmao.com/mlguider/release:0123-xw-arm64",
				"amd64": "NONE",
			},
		},
	}
}

// GetOrCreateRuntimeImagesConfig gets the runtime images configuration from file
// or creates a new one with defaults if it doesn't exist.
//
// This function is called during server bootstrap to ensure the configuration
// file exists. If the file is not present, it writes the default configuration
// to runtime_images.yaml in the config directory.
//
// The configuration file is stored in the same directory as server.conf.
//
// Parameters:
//   - c: Config instance with ConfigDir set
//
// Returns:
//   - RuntimeImagesConfig instance (either loaded from file or default)
//   - Error if file operations fail
func (c *Config) GetOrCreateRuntimeImagesConfig() (RuntimeImagesConfig, error) {
	confPath := filepath.Join(c.Storage.ConfigDir, RuntimeImagesFileName)
	
	// Check if runtime_images.yaml exists
	if _, err := os.Stat(confPath); err == nil {
		// File exists, load it
		return c.loadRuntimeImagesConfig(confPath)
	}
	
	// File doesn't exist, create with defaults
	config := GetDefaultRuntimeImagesConfig()
	
	// Write to file
	if err := c.writeRuntimeImagesConfig(confPath, config); err != nil {
		return nil, fmt.Errorf("failed to write runtime images config: %w", err)
	}
	
	return config, nil
}

// loadRuntimeImagesConfig loads runtime images configuration from YAML file.
//
// Parameters:
//   - path: Path to the runtime_images.yaml file
//
// Returns:
//   - Loaded RuntimeImagesConfig
//   - Error if file reading or YAML parsing fails
func (c *Config) loadRuntimeImagesConfig(path string) (RuntimeImagesConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime images config: %w", err)
	}
	
	var config RuntimeImagesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse runtime images config: %w", err)
	}
	
	return config, nil
}

// writeRuntimeImagesConfig writes runtime images configuration to YAML file.
//
// The YAML file is written with:
//   - 0644 permissions (readable by all, writable by owner)
//   - Proper indentation for readability
//   - Comments explaining the structure
//
// Parameters:
//   - path: Path to write the runtime_images.yaml file
//   - config: RuntimeImagesConfig to write
//
// Returns:
//   - Error if file writing or YAML marshaling fails
func (c *Config) writeRuntimeImagesConfig(path string, config RuntimeImagesConfig) error {
	// Marshal to YAML
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal runtime images config: %w", err)
	}
	
	// Prepare file content with header comments
	header := `# XW Runtime Images Configuration
# This file maps chip models to their inference engine Docker images
# for different CPU architectures.
#
# Structure:
#   <chip-model>:
#     <engine-name>:
#       <architecture>: <docker-image>
#
# Supported chip models:
#   - ascend-910b: Huawei Ascend 910B (training and inference)
#   - ascend-310p: Huawei Ascend 310P (inference only)
#
# Supported engines:
#   - vllm: vLLM inference engine
#   - mindie: MindIE inference engine (Huawei's optimized engine)
#
# Supported architectures:
#   - arm64: ARM 64-bit (aarch64)
#   - amd64: x86 64-bit (x86_64)
#
# You can customize these images based on your deployment requirements.
# The server will automatically select the correct image based on the
# current system's architecture.
#

`
	content := header + string(data)
	
	// Write to file
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write runtime images config file: %w", err)
	}
	
	return nil
}

// GetImageForChipAndEngine returns the Docker image for a specific chip model, engine, and architecture.
//
// This function looks up the appropriate Docker image based on the chip model
// (e.g., "ascend-910b", "ascend-310p"), inference engine (e.g., "vllm", "mindie"),
// and CPU architecture (e.g., "arm64", "amd64").
//
// Parameters:
//   - config: RuntimeImagesConfig to query
//   - chipModel: Chip model identifier (e.g., "ascend-910b", "ascend-310p")
//   - engine: Inference engine name (e.g., "vllm", "mindie")
//   - arch: CPU architecture (e.g., "arm64", "amd64")
//
// Returns:
//   - Docker image URL if found
//   - Error if chip model, engine, or architecture is not configured
//
// Example:
//   image, err := GetImageForChipAndEngine(config, "ascend-910b", "vllm", "arm64")
//   // Returns: "quay.io/ascend/vllm-ascend:v0.11.0rc0-arm64"
func GetImageForChipAndEngine(config RuntimeImagesConfig, chipModel, engine, arch string) (string, error) {
	engineMap, ok := config[chipModel]
	if !ok {
		return "", fmt.Errorf("chip model %s not found in configuration", chipModel)
	}
	
	archMap, ok := engineMap[engine]
	if !ok {
		return "", fmt.Errorf("engine %s not found for chip model %s", engine, chipModel)
	}
	
	image, ok := archMap[arch]
	if !ok {
		return "", fmt.Errorf("architecture %s not found for chip model %s and engine %s", arch, chipModel, engine)
	}
	
	return image, nil
}

// GetImageForChipAndEngineAuto automatically detects the system architecture
// and returns the appropriate Docker image.
//
// This is a convenience function that calls GetImageForChipAndEngine with
// the current system's architecture.
//
// Parameters:
//   - config: RuntimeImagesConfig to query
//   - chipModel: Chip model identifier
//   - engine: Inference engine name
//
// Returns:
//   - Docker image URL for the current architecture
//   - Error if lookup fails or architecture is not supported
//
// Example:
//   image, err := GetImageForChipAndEngineAuto(config, "ascend-910b", "vllm")
//   // Automatically uses "arm64" or "amd64" based on system
func GetImageForChipAndEngineAuto(config RuntimeImagesConfig, chipModel, engine string) (string, error) {
	arch, err := GetSystemArchitecture()
	if err != nil {
		return "", fmt.Errorf("failed to detect system architecture: %w", err)
	}
	
	return GetImageForChipAndEngine(config, chipModel, engine, arch)
}

// SetImageForChipAndEngine sets the Docker image for a specific chip model, engine, and architecture.
//
// This function allows runtime modification of the image configuration.
// Note: This only modifies the in-memory configuration; call writeRuntimeImagesConfig
// to persist changes to disk.
//
// Parameters:
//   - config: RuntimeImagesConfig to modify
//   - chipModel: Chip model identifier
//   - engine: Inference engine name
//   - arch: CPU architecture
//   - image: Docker image URL to set
func SetImageForChipAndEngine(config RuntimeImagesConfig, chipModel, engine, arch, image string) {
	if config == nil {
		return
	}
	
	if config[chipModel] == nil {
		config[chipModel] = make(map[string]map[string]string)
	}
	
	if config[chipModel][engine] == nil {
		config[chipModel][engine] = make(map[string]string)
	}
	
	config[chipModel][engine][arch] = image
}

// GetSupportedChipModels returns a list of all configured chip models.
//
// Parameters:
//   - config: RuntimeImagesConfig to query
//
// Returns:
//   - Slice of chip model identifiers
func GetSupportedChipModels(config RuntimeImagesConfig) []string {
	models := make([]string, 0, len(config))
	for model := range config {
		models = append(models, model)
	}
	return models
}

// GetSupportedEnginesForChip returns a list of configured engines for a chip model.
//
// Parameters:
//   - config: RuntimeImagesConfig to query
//   - chipModel: Chip model identifier
//
// Returns:
//   - Slice of engine names
//   - Error if chip model is not configured
func GetSupportedEnginesForChip(config RuntimeImagesConfig, chipModel string) ([]string, error) {
	engineMap, ok := config[chipModel]
	if !ok {
		return nil, fmt.Errorf("chip model %s not found in configuration", chipModel)
	}
	
	engines := make([]string, 0, len(engineMap))
	for engine := range engineMap {
		engines = append(engines, engine)
	}
	return engines, nil
}

// GetSystemArchitecture returns the current system's CPU architecture
// in a format compatible with Docker image tags.
//
// This function uses Go's runtime.GOARCH to determine the architecture
// and maps it to the standard Docker platform naming:
//   - "arm64": ARM 64-bit (aarch64)
//   - "amd64": x86 64-bit (x86_64)
//
// Returns:
//   - Architecture string ("arm64" or "amd64")
//   - Error if the architecture is not supported
//
// Example:
//   arch, err := GetSystemArchitecture()
//   // On ARM64 system: returns "arm64", nil
//   // On x86_64 system: returns "amd64", nil
func GetSystemArchitecture() (string, error) {
	switch runtime.GOARCH {
	case "arm64", "aarch64":
		return "arm64", nil
	case "amd64", "x86_64":
		return "amd64", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
}

