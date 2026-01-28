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

// DeploymentMode represents how a backend can be deployed
type DeploymentMode string

const (
	// DeploymentModeDocker indicates containerized deployment using Docker
	DeploymentModeDocker DeploymentMode = "docker"
	
	// DeploymentModeNative indicates direct installation on host system
	DeploymentModeNative DeploymentMode = "native"
)

// BackendType represents the inference engine type
type BackendType string

const (
	// BackendTypeMindIE is Huawei's MindIE inference engine for Ascend chips
	BackendTypeMindIE BackendType = "mindie"
	
	// BackendTypeVLLM is the vLLM high-throughput inference engine
	BackendTypeVLLM BackendType = "vllm"
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
	Type BackendType
	
	// Mode is the deployment mode (docker or native)
	Mode DeploymentMode
	
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
// configuration. The spec includes metadata, hardware requirements, and
// an ordered list of backend options.
type ModelSpec struct {
	// ID is the unique model identifier (e.g., "qwen2-7b")
	ID string
	
	// SourceID is the model ID on the source platform (e.g., ModelScope ID "Qwen/Qwen2-7B")
	// This is used when downloading models from external repositories
	SourceID string
	
	// DisplayName is the human-readable model name
	DisplayName string
	
	// Family groups related models (e.g., "qwen", "llama")
	Family string
	
	// Version is the model version string
	Version string
	
	// Description provides detailed information about the model
	Description string
	
	// Backends lists available backend options in priority order
	// The first available backend will be used by default
	// Docker modes should typically be listed before native modes
	Backends []BackendOption
	
	// SupportedDevices lists compatible AI chip types
	SupportedDevices []api.DeviceType
	
	// RequiredVRAM is the minimum VRAM in GB needed to run this model
	RequiredVRAM int
	
	// Parameters is the model size in billions of parameters
	Parameters float64
	
	// ContextLength is the maximum context window size in tokens
	ContextLength int
	
	// EmbeddingLength is the dimension size of the model's embedding layer
	// For example: Qwen2-7B = 3584, Llama3-8B = 4096
	EmbeddingLength int
	
	// Capabilities lists the model's supported features
	// Common values: "completion", "vision", "tool_use", "function_calling"
	Capabilities []string
	
	// License specifies the model's license (e.g., "Apache-2.0", "MIT")
	License string
	
	// Homepage is the URL to the model's official page or repository
	Homepage string
	
	// Tag specifies the model variant, typically quantization level (e.g., "int8", "fp16", "int4")
	// Similar to Docker image tags, used as: model:tag
	// Empty string means default/full precision variant
	Tag string
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
	
	for _, d := range m.SupportedDevices {
		if d == deviceType {
			return true
		}
	}
	return false
}

// GetBackendsByType filters backend options by backend type
//
// Parameters:
//   - backendType: The backend type to filter for
//
// Returns:
//   - Slice of BackendOptions matching the type
func (m *ModelSpec) GetBackendsByType(backendType BackendType) []BackendOption {
	var filtered []BackendOption
	for _, b := range m.Backends {
		if b.Type == backendType {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// GetBackendsByMode filters backend options by deployment mode
//
// Parameters:
//   - mode: The deployment mode to filter for
//
// Returns:
//   - Slice of BackendOptions matching the mode
func (m *ModelSpec) GetBackendsByMode(mode DeploymentMode) []BackendOption {
	var filtered []BackendOption
	for _, b := range m.Backends {
		if b.Mode == mode {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// GetDefaultBackend returns the first backend option (highest priority)
//
// Returns:
//   - Pointer to the default BackendOption
//   - Error if no backends are configured
func (m *ModelSpec) GetDefaultBackend() (*BackendOption, error) {
	if len(m.Backends) == 0 {
		return nil, fmt.Errorf("model %s has no backends configured", m.ID)
	}
	return &m.Backends[0], nil
}

// Validate checks if the model specification is valid
//
// Returns:
//   - Error if validation fails, nil otherwise
func (m *ModelSpec) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("model ID cannot be empty")
	}
	if m.DisplayName == "" {
		return fmt.Errorf("model display name cannot be empty")
	}
	if len(m.Backends) == 0 {
		return fmt.Errorf("model %s must have at least one backend", m.ID)
	}
	if m.RequiredVRAM <= 0 {
		return fmt.Errorf("model %s must specify required VRAM", m.ID)
	}
	return nil
}

