package config

import (
	"fmt"
	"runtime"
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

// LoadRuntimeImagesConfig loads runtime images configuration from devices configuration.
//
// This method extracts runtime images from the devices.yaml configuration file
// using the default configuration path.
//
// Returns:
//   - RuntimeImagesConfig instance extracted from devices config
//   - Error if devices config cannot be loaded
//
// Example:
//
//	config, err := LoadRuntimeImagesConfig()
//	if err != nil {
//	    log.Fatalf("Failed to load runtime images config: %v", err)
//	}
func LoadRuntimeImagesConfig() (RuntimeImagesConfig, error) {
	return LoadRuntimeImagesConfigFrom("")
}

// LoadRuntimeImagesConfigFrom loads runtime images configuration from a specific devices config file.
//
// This method is used when you need to load from a non-default configuration path.
//
// Parameters:
//   - devicesConfigPath: Path to the devices configuration file
//
// Returns:
//   - RuntimeImagesConfig instance extracted from devices config
//   - Error if devices config cannot be loaded
//
// Example:
//
//	config, err := LoadRuntimeImagesConfigFrom("/custom/path/devices.yaml")
//	if err != nil {
//	    log.Fatalf("Failed to load runtime images config: %v", err)
//	}
func LoadRuntimeImagesConfigFrom(devicesConfigPath string) (RuntimeImagesConfig, error) {
	// Load devices configuration
	devicesConfig, err := LoadDevicesConfigFrom(devicesConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load devices config: %w", err)
	}
	
	// Extract runtime images from devices config
	runtimeImages := make(RuntimeImagesConfig)
	
	for _, vendor := range devicesConfig.Vendors {
		for _, chipModel := range vendor.ChipModels {
			if len(chipModel.RuntimeImages) > 0 {
				// Store runtime images only for base model config_key
				// Variants share the same images, sandbox will use base config_key to look up
				runtimeImages[chipModel.ConfigKey] = chipModel.RuntimeImages
			}
		}
	}
	
	if len(runtimeImages) == 0 {
		return nil, fmt.Errorf("no runtime images found in devices configuration")
	}
	
	return runtimeImages, nil
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

