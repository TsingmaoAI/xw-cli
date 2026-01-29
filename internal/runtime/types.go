package runtime

import (
	"context"
	"time"
	
	"github.com/tsingmao/xw/internal/api"
)

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
	InstanceID     string
	ModelID        string
	Alias          string // Instance alias for inference (defaults to ModelID)
	ModelPath      string
	ModelVersion   string
	BackendType    string // Backend type (e.g., "vllm")
	DeploymentMode string // Deployment mode (e.g., "docker")
	ServerName     string // Server unique identifier (added as container name suffix)
	Devices        []DeviceInfo
	Port           int
	Environment    map[string]string
	ExtraConfig    map[string]interface{}
}

// DeviceInfo contains information about a hardware device.
type DeviceInfo struct {
	Type       api.DeviceType
	Index      int
	PCIAddress string
	DevicePath string
	ModelName  string
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

