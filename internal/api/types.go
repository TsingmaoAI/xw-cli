// Package api defines the API types and contracts for the xw application.
//
// This package contains all the data structures used for communication between
// the CLI client and the HTTP server. It defines:
//   - Request and response types for all API endpoints
//   - Model and device type definitions
//   - Error response structures
//
// All types in this package are designed to be JSON-serializable for easy
// HTTP transmission. The API follows RESTful principles where applicable.
package api

// BackendType represents the inference engine type
type BackendType string

const (
	// BackendTypeMindIE is Huawei's MindIE inference engine for Ascend chips
	BackendTypeMindIE BackendType = "mindie"
	
	// BackendTypeVLLM is the vLLM high-throughput inference engine
	BackendTypeVLLM BackendType = "vllm"
	
	// BackendTypeMLGuider is MLGuider high-performance inference engine for large language models
	BackendTypeMLGuider BackendType = "mlguider"
	
	// BackendTypeOmniInfer is Omni-Infer inference engine optimized for Ascend chips
	BackendTypeOmniInfer BackendType = "omni-infer"
)

// DeploymentMode represents how a backend can be deployed
type DeploymentMode string

const (
	// DeploymentModeDocker indicates containerized deployment using Docker
	DeploymentModeDocker DeploymentMode = "docker"
	
	// DeploymentModeNative indicates direct installation on host system
	DeploymentModeNative DeploymentMode = "native"
)

// DeviceType represents specific AI chip model identifiers.
//
// Device types use precise chip model identifiers (e.g., "ascend-910b", "ascend-310p")
// rather than brand-level abstractions. This is necessary because inference engine
// compatibility is tied to specific chip models, not brands.
//
// These values are defined in device.ConfigKey constants.
type DeviceType string

const (
	// DeviceTypeAll is a special value representing all device types.
	// Used in queries to retrieve models compatible with any device.
	DeviceTypeAll DeviceType = "all"
)

// Model represents an AI model with its metadata and capabilities.
//
// A Model contains all information needed to identify, describe, and utilize
// an AI model within the xw system. Models are pre-configured and optimized
// for specific domestic chip architectures.
type Model struct {
	// Name is the unique identifier for the model.
	// Examples: "llama3-8b", "qwen-7b", "chatglm3-6b"
	Name string `json:"name"`

	// Description is a human-readable description of the model.
	// Should explain the model's purpose, capabilities, and use cases.
	Description string `json:"description"`

	// Version is the model version following semantic versioning.
	// Format: "major.minor.patch" (e.g., "1.0.0", "2.1.3")
	Version string `json:"version"`

	// Size is the total model size in bytes.
	// Includes all model files, weights, and necessary assets.
	Size int64 `json:"size"`

	// SupportedDevices lists all device types compatible with this model.
	// A model may support multiple device types if optimized for each.
	SupportedDevices []DeviceType `json:"supported_devices"`

	// Format specifies the model file format.
	// Common formats: "ONNX", "TensorRT", "GGUF", "Paddle"
	Format string `json:"format"`

	// CreatedAt is the model creation timestamp in RFC3339 format.
	// Example: "2026-01-01T00:00:00Z"
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the last modification timestamp in RFC3339 format.
	// Updated when the model is modified or re-optimized.
	UpdatedAt string `json:"updated_at"`
	
	// Status indicates the download status of the model.
	// Values: "not_downloaded", "downloading", "downloaded"
	Status string `json:"status"`
	
	// Source is the SourceID used for downloading (e.g., "Qwen/Qwen2.5-7B-Instruct")
	// Empty for models not downloaded yet
	Source string `json:"source,omitempty"`
	
	// Tag is the model version tag (e.g., "latest")
	// Empty for models not downloaded yet
	Tag string `json:"tag,omitempty"`
	
	// DefaultEngine is the default inference engine (e.g., "vllm:docker")
	// Empty for models not downloaded yet
	DefaultEngine string `json:"default_engine,omitempty"`
	
	// ModifiedAt is the last modification time in RFC3339 format
	// Empty for models not downloaded yet
	ModifiedAt string `json:"modified_at,omitempty"`
}

// ListModelsRequest represents a request to list available models.
//
// This request supports filtering by device type to show only models
// compatible with specific hardware. The ShowAll flag can override
// device filtering to display all models in the registry.
type ListModelsRequest struct {
	// DeviceType filters models by device compatibility.
	// If set to DeviceTypeAll or empty, device filtering is not applied.
	DeviceType DeviceType `json:"device_type,omitempty"`

	// ShowAll indicates whether to show all models regardless of device type.
	// When true, the DeviceType filter is ignored.
	ShowAll bool `json:"show_all,omitempty"`
}

// ListModelsResponse represents the response containing a list of models.
//
// This response is returned by the list models endpoint and contains
// all models matching the request criteria.
type ListModelsResponse struct {
	// Models is the array of models matching the request filters.
	// May be empty if no models match the criteria.
	Models []Model `json:"models"`
	
	// TotalModels is the total number of models in the registry
	TotalModels int `json:"total_models"`
	
	// AvailableModels is the number of models available for current host
	AvailableModels int `json:"available_models"`
	
	// DetectedDevices lists the device types detected on current host
	DetectedDevices []DeviceType `json:"detected_devices"`
}

// DownloadedModel represents a model that has been downloaded to local storage.
//
// This type contains information about models that are actually present in the
// models directory, as opposed to all models in the registry.
type DownloadedModel struct {
	// ID is the internal model identifier (e.g., "qwen2.5-7b-instruct")
	ID string `json:"id"`
	
	// Source is the SourceID used for downloading (e.g., "Qwen/Qwen2.5-7B-Instruct")
	// This may differ from ID and represents the actual repository path
	Source string `json:"source"`
	
	// Tag is the model version tag (currently always "latest")
	Tag string `json:"tag"`
	
	// Size is the total size of the model directory in bytes
	Size int64 `json:"size"`
	
	// DefaultEngine is the default inference engine for this model (e.g., "vllm:docker")
	DefaultEngine string `json:"default_engine"`
	
	// ModifiedAt is the last modification time in RFC3339 format
	ModifiedAt string `json:"modified"`
}

// RunRequest represents a request to execute a model with given input.
//
// This request initiates model inference, providing input data and optional
// configuration parameters. The model must be available (pulled) before
// it can be run.
type RunRequest struct {
	// Model is the name of the model to execute.
	// Must match a registered model name (e.g., "llama3-8b").
	Model string `json:"model"`

	// Input is the input data to be processed by the model.
	// Format depends on the model type (text, image path, etc.).
	Input string `json:"input"`

	// Options contains model-specific execution parameters.
	// Examples: temperature, max_tokens, top_p, etc.
	// The available options depend on the model being used.
	Options map[string]interface{} `json:"options,omitempty"`
}

// RunResponse represents the response from model execution.
//
// This response contains the model's output along with performance
// metrics and metadata about the inference run.
type RunResponse struct {
	// Output is the generated result from the model.
	// Format depends on the model type (text, structured data, etc.).
	Output string `json:"output"`

	// Metrics contains performance and execution statistics.
	// Common metrics: latency_ms, tokens_generated, memory_used, etc.
	// Useful for monitoring and optimization.
	Metrics map[string]interface{} `json:"metrics,omitempty"`
}

// PullRequest represents a request to download a model.
//
// This request initiates the download and installation of a model
// from the model registry. The model must exist in the registry
// before it can be pulled.
type PullRequest struct {
	// Model is the name of the model to download.
	// Must match a registered model name in the registry.
	Model string `json:"model"`

	// Version is the specific version to download.
	// If empty, the latest version is pulled.
	// Format: "major.minor.patch" (e.g., "1.0.0")
	Version string `json:"version,omitempty"`
}

// PullResponse represents the response from a model pull operation.
//
// This response provides status information about the download progress
// and completion state. For long-running pulls, multiple responses may
// be sent to update the client on progress.
type PullResponse struct {
	// Status indicates the current state of the pull operation.
	// Values: "downloading", "success", "failed"
	Status string `json:"status"`

	// Progress is the download completion percentage (0-100).
	// Only present during active downloads.
	Progress int `json:"progress,omitempty"`

	// Message contains human-readable status information.
	// Provides details about the operation's progress or any issues.
	Message string `json:"message,omitempty"`
}

// VersionResponse represents the server version information.
//
// This response provides build and version metadata about the running
// server instance. Useful for debugging, compatibility checking, and
// support purposes.
type VersionResponse struct {
	// Version is the semantic version of the server.
	// Format: "major.minor.patch" (e.g., "1.0.0")
	Version string `json:"version"`

	// BuildTime is the timestamp when the server binary was built.
	// Format: RFC3339 (e.g., "2026-01-26T10:00:00Z")
	BuildTime string `json:"build_time"`
}

// HealthResponse represents the server health status.
//
// This response is returned by the health check endpoint and indicates
// whether the server is operational and ready to handle requests.
// Used by monitoring systems and load balancers.
type HealthResponse struct {
	// Status indicates the overall health status.
	// Common values: "healthy", "unhealthy", "degraded"
	Status string `json:"status"`

	// Message contains additional details about the health status.
	// May include information about subsystem states or issues.
	Message string `json:"message,omitempty"`
}

// ErrorResponse represents an error response from the API.
//
// This is the standard error format returned by all API endpoints
// when an error occurs. It provides both a human-readable message
// and an optional machine-readable error code.
type ErrorResponse struct {
	// Error is the human-readable error message.
	// Should clearly describe what went wrong and, if possible, how to fix it.
	Error string `json:"error"`

	// Code is the machine-readable error code.
	// Can be used by clients for error handling logic.
	// Examples: "MODEL_NOT_FOUND", "INVALID_REQUEST", "AUTH_FAILED"
	Code string `json:"code,omitempty"`
}

// DeviceListResponse represents the response from listing devices.
//
// This response contains all devices detected on the server machine,
// including their current status and availability.
// The devices field contains device.Device objects with JSON serialization.
type DeviceListResponse struct {
	// Devices is the list of detected AI accelerator devices (device.Device type).
	Devices interface{} `json:"devices"`
}

// SupportedDevicesRequest represents a request to query supported device types.
//
// This optional request allows filtering or querying specific device
// type information from the server's configuration.
type SupportedDevicesRequest struct {
	// Type is an optional filter for device types.
	// If empty, all supported device types are returned.
	Type string `json:"type,omitempty"`
}

// SupportedDevicesResponse represents the response from querying supported devices.
//
// This response contains the list of device types that are configured
// and supported by the server, based on the loaded configuration files.
type SupportedDevicesResponse struct {
	// DeviceTypes is the list of supported device model identifiers.
	// Examples: ["ascend-910b", "ascend-310p"]
	DeviceTypes []string `json:"device_types"`
}
