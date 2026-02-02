// Package vllmdocker implements vLLM runtime with Docker deployment.
package vllmdocker

import (
	"fmt"
	"strings"

	"github.com/tsingmaoai/xw-cli/internal/config"
	runtime "github.com/tsingmaoai/xw-cli/internal/runtime"
)

// MetaXSandbox provides vLLM runtime sandbox for MetaX AI accelerators.
//
// This sandbox implementation configures Docker containers for vLLM inference
// on MetaX chips, handling device access, driver dependencies, and runtime
// environment setup.
type MetaXSandbox struct{}

// NewMetaXSandbox creates a new vLLM MetaX device sandbox.
func NewMetaXSandbox() *MetaXSandbox {
	return &MetaXSandbox{}
}

// PrepareEnvironment generates environment variables for vLLM with MetaX devices.
//
// Key environment variables:
//   - CUDA_VISIBLE_DEVICES: Comma-separated device indices visible to vLLM
//     MetaX uses CUDA-compatible interface, so we use CUDA_VISIBLE_DEVICES
//   - VLLM_WORKER_MULTIPROC_METHOD: Use 'spawn' for better stability
//
// Parameters:
//   - devices: List of allocated MetaX devices
//
// Returns:
//   - Map of environment variable names to values
//   - Error if device configuration is invalid
func (s *MetaXSandbox) PrepareEnvironment(devices []runtime.DeviceInfo) (map[string]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no MetaX devices provided")
	}

	// Build comma-separated device index list
	indices := make([]string, 0, len(devices))
	for _, dev := range devices {
		if dev.Index < 0 {
			return nil, fmt.Errorf("invalid MetaX device index: %d", dev.Index)
		}
		indices = append(indices, fmt.Sprintf("%d", dev.Index))
	}
	visibleDevices := strings.Join(indices, ",")

	return map[string]string{
		// MetaX device visibility (uses CUDA-compatible interface)
		"CUDA_VISIBLE_DEVICES": visibleDevices,
		
		// vLLM configuration for MetaX
		"VLLM_WORKER_MULTIPROC_METHOD": "spawn",
	}, nil
}

// GetDeviceMounts returns device files that must be mounted into the container.
//
// MetaX accelerators are exposed through several device files:
//   - /dev/dri: DRM/DRI interface for GPU access
//   - /dev/mxcd: MetaX control device
//   - /dev/infiniband: InfiniBand for multi-device communication
//
// Parameters:
//   - devices: List of allocated MetaX devices
//
// Returns:
//   - List of device paths to mount
//   - Error if devices are invalid
func (s *MetaXSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no MetaX devices provided")
	}

	return []string{
		"/dev/dri",         // DRM/DRI interface
		"/dev/mxcd",        // MetaX control device
		"/dev/infiniband",  // InfiniBand for multi-device communication
	}, nil
}

// GetAdditionalMounts returns additional volume mounts required for MetaX runtime.
//
// MetaX containers need access to host directories containing:
//   - Driver libraries: Low-level MetaX driver interfaces
//   - System libraries: Required runtime dependencies
//   - Cache directory: For model downloads and compilation artifacts
//
// Returns:
//   - Map of host path -> container path
func (s *MetaXSandbox) GetAdditionalMounts() map[string]string {
	return map[string]string{
		// System libraries and binaries
		"/usr/local": "/usr/local",
		
		// Cache directory for model downloads and compilation artifacts
		// Writable mount to persist downloaded models across container restarts
		"/root/.cache": "/root/.cache",
	}
}

// RequiresPrivileged indicates whether the container needs privileged mode.
//
// MetaX containers require privileged mode (--privileged=true) because:
//   1. Device drivers need direct hardware access beyond standard device mounts
//   2. Runtime performs privileged operations for memory management
//   3. Some driver initialization requires elevated permissions
//
// While less secure than capability-based access, this is currently required
// for proper MetaX accelerator operation with vLLM.
//
// Returns:
//   - true: MetaX containers must run in privileged mode
func (s *MetaXSandbox) RequiresPrivileged() bool {
	return true
}

// GetCapabilities returns Linux capabilities needed by the container.
//
// Even in privileged mode, documenting required capabilities helps
// understand the security requirements and may support future transition
// to non-privileged mode with specific capabilities.
//
// Returns:
//   - List of required Linux capabilities
func (s *MetaXSandbox) GetCapabilities() []string {
	return []string{
		"SYS_ADMIN",     // Required for device initialization
		"SYS_RAWIO",     // Required for direct device I/O
		"IPC_LOCK",      // Required for memory locking
		"SYS_RESOURCE",  // Required for resource limit adjustments
	}
}

// GetDefaultImage returns the default Docker image for MetaX vLLM.
//
// This method automatically selects the appropriate image based on:
//   - Chip model from device information
//   - System architecture (x86_64 for MetaX)
//
// The image is looked up from the runtime_images.yaml configuration file.
//
// Parameters:
//   - devices: List of devices (uses first device's ConfigKey for chip identification)
//
// Returns:
//   - Docker image URL for vLLM on MetaX
//   - Error if configuration not found or architecture not supported
func (s *MetaXSandbox) GetDefaultImage(devices []runtime.DeviceInfo) (string, error) {
	// Load runtime images configuration
	runtimeImages, err := config.LoadRuntimeImagesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load runtime images config: %w", err)
	}
	
	return runtime.GetImageForEngine(runtimeImages, devices, "vllm")
}

// GetDockerRuntime returns the Docker runtime to use for MetaX devices.
//
// Returns:
//   - "runc" - Standard OCI runtime
func (s *MetaXSandbox) GetDockerRuntime() string {
	return "runc"
}

// supportedMetaXDeviceTypes lists all MetaX device types supported by this sandbox.
//
// When adding support for new MetaX devices, add their config_key here.
// Each device type must match the config_key from devices.yaml configuration.
var supportedMetaXDeviceTypes = []string{
	"metax-c550",
	// Add more MetaX device types here as they are tested and validated
}

// Supports checks if this sandbox supports the given device type.
//
// Parameters:
//   - deviceType: The device type to check (e.g., "metax-c550")
//
// Returns:
//   - true if this sandbox supports the device type
//
// Example:
//
//	sandbox := NewMetaXSandbox()
//	if sandbox.Supports("metax-c550") {
//	    fmt.Println("MetaX C550 is supported")
//	}
func (s *MetaXSandbox) Supports(deviceType string) bool {
	for _, supported := range supportedMetaXDeviceTypes {
		if deviceType == supported {
			return true
		}
	}
	return false
}

// GetSharedMemorySize returns the shared memory size required for vLLM on MetaX.
//
// vLLM requires adequate shared memory for:
//   - PyTorch DataLoader workers in multi-process mode
//   - KV cache management across processes
//   - Tensor sharing in distributed inference
//
// The default 64MB shared memory in Docker is often insufficient for LLM
// inference workloads.
//
// Returns:
//   - Shared memory size in bytes (100GB = 100 * 1024^3)
func (s *MetaXSandbox) GetSharedMemorySize() int64 {
	// 100GB shared memory for LLM inference workloads
	return 100 * 1024 * 1024 * 1024
}
