package runtime

import (
	"context"
	"time"
	
	"github.com/tsingmaoai/xw-cli/internal/api"
)

// Type aliases for convenience
type BackendType = api.BackendType
type DeploymentMode = api.DeploymentMode

// Runtime defines the interface for model runtime backends.
type Runtime interface {
	Create(ctx context.Context, params *CreateParams) (*Instance, error)
	Start(ctx context.Context, instanceID string) error
	Stop(ctx context.Context, instanceID string) error
	Remove(ctx context.Context, instanceID string) error
	Get(ctx context.Context, instanceID string) (*Instance, error)
	List(ctx context.Context) ([]*Instance, error)
	Logs(ctx context.Context, instanceID string, follow bool) (LogStream, error)
	Name() string
}

// CreateParams contains standard parameters for creating an instance.
type CreateParams struct {
	InstanceID       string
	ModelID          string
	Alias            string // Instance alias for inference (defaults to ModelID)
	ModelPath        string
	ModelVersion     string
	BackendType      string // Backend type (e.g., "vllm")
	DeploymentMode   string // Deployment mode (e.g., "docker")
	ServerName       string // Server unique identifier (added as container name suffix)
	DataDir          string // Data directory for runtime files (e.g., converted models)
	Devices          []DeviceInfo
	Port             int
	Environment      map[string]string
	ExtraConfig      map[string]interface{}
	
	// Template parameters from runtime_params.yaml
	// Format: ["key=value", "tensor_parallel=4"]
	// Will be converted to environment variables (KEY=value, TENSOR_PARALLEL=4)
	TemplateParams   []string
	
	// Unified parallelism parameters (set by Manager)
	TensorParallel   int // Number of devices for tensor parallelism
	PipelineParallel int // Number of devices for pipeline parallelism (default: 1)
	WorldSize        int // Total number of devices (TENSOR_PARALLEL * PIPELINE_PARALLEL)
	
	// EventChannel for sending progress messages to client (optional, for SSE streams)
	EventChannel     chan<- string
}

// DeviceInfo contains information about a hardware device.
type DeviceInfo struct {
	Type       api.DeviceType
	Index      int
	PCIAddress string
	DevicePath string
	ModelName  string
	ConfigKey  string // Configuration key for runtime_images.yaml lookup
	Properties map[string]string
}

// Instance represents a running model instance.
type Instance struct {
	ID           string
	RuntimeName  string
	CreatedAt    time.Time
	ModelID      string
	Alias        string // Instance alias for inference
	ModelVersion string
	State        InstanceState
	StartedAt    time.Time
	StoppedAt    time.Time
	Error        string
	Port         int
	Endpoint     string
	CPUUsage     float64
	MemoryUsage  int64
	Metadata     map[string]string
}

// InstanceState represents the state of an instance.
type InstanceState string

const (
	StateCreating  InstanceState = "creating"
	StateCreated   InstanceState = "created"
	StateStarting  InstanceState = "starting"
	StateRunning   InstanceState = "running"
	StateReady     InstanceState = "ready"     // Running and endpoint is accessible
	StateUnhealthy InstanceState = "unhealthy" // Running but endpoint is not accessible
	StateStopping  InstanceState = "stopping"
	StateStopped   InstanceState = "stopped"
	StateError     InstanceState = "error"
	StateUnknown   InstanceState = "unknown"   // Unable to determine real state
)

// LogStream provides access to instance logs.
type LogStream interface {
	Read(p []byte) (n int, err error)
	Close() error
}

// RunOptions contains legacy API parameters (for handlers).
type RunOptions struct {
	ModelID          string
	Alias            string // Instance alias for inference (defaults to ModelID)
	ModelPath        string // Path to the model files on disk
	BackendType      string
	DeploymentMode   string
	Port             int
	Interactive      bool
	AdditionalConfig map[string]interface{}
	EventChannel     chan<- string // Optional: for sending progress events via SSE
}

// RunInstance represents legacy API response (for handlers).
type RunInstance struct {
	ID             string                 `json:"id"`
	ModelID        string                 `json:"model_id"`
	Alias          string                 `json:"alias"` // Instance alias for inference
	BackendType    string                 `json:"backend_type"`
	DeploymentMode string                 `json:"deployment_mode"`
	State          InstanceState          `json:"state"`
	CreatedAt      time.Time              `json:"created_at"`
	StartedAt      time.Time              `json:"started_at,omitempty"`
	Port           int                    `json:"port"`
	Error          string                 `json:"error,omitempty"`
	Config         map[string]interface{} `json:"config,omitempty"`
}

