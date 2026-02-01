// Package model provides AI model specifications and registry management.
//
// This package defines the structure for AI model metadata, including supported
// backends, hardware requirements, and deployment configurations. Each model
// is defined in its own file under model subdirectories (e.g., qwen/, llama/).
package models

import (
	"fmt"
	
	"github.com/tsingmao/xw/internal/api"
)

// BackendOption specifies a backend choice with its deployment mode
//
// Each model maintains an ordered list of BackendOptions, tried sequentially
// until an available backend is found. Docker modes are typically listed first
// for easier deployment and better isolation.
//
// Note: Docker images are managed by the runtime implementation, not by the model spec.
// Each runtime (e.g., vllm-docker, mindie-docker) defines its own default image.
type BackendOption struct {
	// Type is the backend engine type (e.g., "mindie", "vllm")
	Type api.BackendType
	
	// Mode is the deployment mode (docker or native)
	Mode api.DeploymentMode
	
	// Command is the container command to run (optional)
	// If empty, uses image default CMD/ENTRYPOINT
	// Example: ["vllm", "serve", "/model", "--served-model-name", "default"]
	Command []string
	
	// Priority is an optional priority override (lower = higher priority)
	// If not set, list order determines priority
	Priority int
}

// String returns a human-readable representation of the backend option
func (b BackendOption) String() string {
	return fmt.Sprintf("%s (%s)", b.Type, b.Mode)
}

// ModelSpec defines the complete specification for an AI model
//
// Each model file should create a ModelSpec instance with all necessary
// configuration. The spec includes model identification, specifications,
// and deployment configuration.
type ModelSpec struct {
	// Model identification
	
	// ID is the unique model identifier (e.g., "qwen2-7b")
	ID string
	
	// SourceID is the model ID on the source platform (e.g., ModelScope ID "Qwen/Qwen2-7B")
	// This is used when downloading models from external repositories
	SourceID string
	
	// Model specifications
	
	// Parameters is the model size in billions of parameters
	Parameters float64
	
	// ContextLength is the maximum context window size in tokens
	ContextLength int
	
	// EmbeddingLength is the dimension size of the model's embedding layer
	// For example: Qwen2-7B = 3584, Llama3-8B = 4096
	EmbeddingLength int
	
	// Deployment configuration
	
	// SupportedDevices maps device types to their supported engines
	// This allows different devices to have different engine options
	// Example: map["ascend-910b"] = [vllm:docker, mindie:docker, mlguider:docker]
	//          map["ascend-310p"] = [vllm:docker, mindie:docker]
	SupportedDevices map[api.DeviceType][]BackendOption
	
	// Tag specifies the model variant, typically quantization level (e.g., "int8", "fp16", "int4")
	// Similar to Docker image tags, used as: model:tag
	// Empty string means default/full precision variant
	Tag string
	
	// Capabilities lists the model's supported features
	// Common values: "completion", "vision", "tool_use", "function_calling"
	Capabilities []string
}

// SupportsDevice checks if the model supports a specific device type
//
// Parameters:
//   - deviceType: The device type to check
//
// Returns:
//   - true if the model supports the device, false otherwise
func (m *ModelSpec) SupportsDevice(deviceType api.DeviceType) bool {
	if len(m.SupportedDevices) == 0 {
		// If no devices specified, assume universal support
		return true
	}
	
	_, exists := m.SupportedDevices[deviceType]
	return exists
}

// GetEnginesForDevice returns the list of supported engines for a specific device
//
// Parameters:
//   - deviceType: The device type to get engines for
//
// Returns:
//   - Slice of BackendOptions for the device (in priority order)
//   - Empty slice if device is not supported
func (m *ModelSpec) GetEnginesForDevice(deviceType api.DeviceType) []BackendOption {
	engines, exists := m.SupportedDevices[deviceType]
	if !exists {
		return []BackendOption{}
	}
	return engines
}

// GetAllSupportedDevices returns all device types that this model supports
//
// Returns:
//   - Slice of DeviceType values
func (m *ModelSpec) GetAllSupportedDevices() []api.DeviceType {
	devices := make([]api.DeviceType, 0, len(m.SupportedDevices))
	for device := range m.SupportedDevices {
		devices = append(devices, device)
	}
	return devices
}

// Validate checks if the model specification is valid
//
// Only validates essential fields that cannot be read from model files.
// Metadata like DisplayName, Parameters, etc. will be read from config.json.
//
// Returns:
//   - Error if validation fails, nil otherwise
func (m *ModelSpec) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("model ID cannot be empty")
	}
	if len(m.SupportedDevices) == 0 {
		return fmt.Errorf("model %s must specify at least one supported device", m.ID)
	}
	// Verify each device has at least one engine
	for device, engines := range m.SupportedDevices {
		if len(engines) == 0 {
			return fmt.Errorf("model %s: device %s must have at least one engine", m.ID, device)
		}
	}
	return nil
}

