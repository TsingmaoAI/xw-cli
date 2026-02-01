// Package vllmdocker implements vLLM runtime with Docker deployment.
package vllmdocker

import (
	"fmt"
	"strings"

	"github.com/tsingmao/xw/internal/config"
	"github.com/tsingmao/xw/internal/runtime"
)

// AscendSandbox handles parameter transformation for Ascend NPU devices.
//
// This sandbox is responsible for converting standard runtime parameters
// into Ascend-specific Docker configurations, including:
//   - Environment variables (ASCEND_RT_VISIBLE_DEVICES, etc.)
//   - Device mounts (/dev/davinci*, /dev/davinci_manager, etc.)
//   - Volume mounts (driver libs, tools, config files)
//   - Container privileges and capabilities
//
// The transformation ensures vLLM can properly access and utilize Ascend NPUs.
type AscendSandbox struct{}

// NewAscendSandbox creates a new Ascend device sandbox.
func NewAscendSandbox() *AscendSandbox {
	return &AscendSandbox{}
}

// PrepareEnvironment generates environment variables for Ascend devices.
//
// Key environment variables:
//   - ASCEND_RT_VISIBLE_DEVICES: Comma-separated NPU indices visible to vLLM
//     Example: "0,1,2,3" makes NPU 0-3 accessible
//   - ASCEND_VISIBLE_DEVICES: Legacy variable for compatibility
//   - ASCEND_SLOG_PRINT_TO_STDOUT: Enables logging to stdout for Docker logs
//   - ASCEND_GLOBAL_LOG_LEVEL: Controls CANN runtime log verbosity
//
// Parameters:
//   - devices: List of Ascend devices to make visible
//
// Returns:
//   - Environment variable map
//   - Error if device indices are invalid
func (s *AscendSandbox) PrepareEnvironment(devices []runtime.DeviceInfo) (map[string]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no Ascend devices provided")
	}

	// Build comma-separated device index list
	indices := make([]string, 0, len(devices))
	for _, dev := range devices {
		if dev.Index < 0 {
			return nil, fmt.Errorf("invalid Ascend device index: %d", dev.Index)
		}
		indices = append(indices, fmt.Sprintf("%d", dev.Index))
	}
	visibleDevices := strings.Join(indices, ",")

	return map[string]string{
		// Primary variable used by vLLM-Ascend
		"ASCEND_RT_VISIBLE_DEVICES": visibleDevices,
		
		// Legacy compatibility for older Ascend applications
		"ASCEND_VISIBLE_DEVICES": visibleDevices,
		
		// CANN runtime configuration
		"ASCEND_SLOG_PRINT_TO_STDOUT": "1", // Enable stdout logging
		"ASCEND_GLOBAL_LOG_LEVEL":     "3", // Log level: 0=debug, 3=error
	}, nil
}

// GetDeviceMounts returns device files that must be mounted into the container.
//
// Ascend NPUs are exposed through several device files:
//   - /dev/davinci[N]: Individual NPU device (N = device index)
//   - /dev/davinci_manager: Device management interface
//   - /dev/devmm_svm: Shared virtual memory interface
//   - /dev/hisi_hdc: Host-device communication channel
//
// These devices must be mounted with read-write-mount (rwm) permissions
// to allow the container full access to the NPUs.
//
// Parameters:
//   - devices: List of Ascend devices to mount
//
// Returns:
//   - List of device paths to mount
//   - Error if devices are invalid
func (s *AscendSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no Ascend devices provided")
	}

	mounts := make([]string, 0, len(devices)+3)
	
	// Mount individual NPU devices
	for _, dev := range devices {
		if dev.Index < 0 {
			return nil, fmt.Errorf("invalid Ascend device index: %d", dev.Index)
		}
		mounts = append(mounts, fmt.Sprintf("/dev/davinci%d", dev.Index))
	}
	
	// Mount common Ascend device files required by all containers
	commonDevices := []string{
		"/dev/davinci_manager", // Device manager for NPU coordination
		"/dev/devmm_svm",       // Shared virtual memory for host-device communication
		"/dev/hisi_hdc",        // Huawei device communication channel
	}
	mounts = append(mounts, commonDevices...)
	
	return mounts, nil
}

// GetAdditionalMounts returns additional volume mounts required for Ascend.
//
// Ascend containers need access to host directories containing:
//   - Driver libraries: Low-level NPU driver interfaces
//   - Management tools: npu-smi for device monitoring
//   - DCMI libraries: Device Chip Management Interface
//   - Configuration files: Driver version and installation info
//   - Cache directory: For HuggingFace model downloads
//
// All mounts are read-only except /root/.cache which needs write access.
//
// Returns:
//   - Map of host path -> container path
func (s *AscendSandbox) GetAdditionalMounts() map[string]string {
	return map[string]string{
		// DCMI (Device Chip Management Interface) libraries
		// Provides device discovery, monitoring, and management APIs
		"/usr/local/dcmi": "/usr/local/dcmi",
		
		// npu-smi: NPU System Management Interface tool
		// Similar to nvidia-smi, used for querying NPU status
		"/usr/local/bin/npu-smi": "/usr/local/bin/npu-smi",
		
		// Driver libraries required by CANN runtime
		"/usr/local/Ascend/driver/lib64/": "/usr/local/Ascend/driver/lib64/",
		
		// Driver version information file
		// Required for version compatibility checks
		"/usr/local/Ascend/driver/version.info": "/usr/local/Ascend/driver/version.info",
		
		// Ascend installation metadata
		// Contains installation configuration and paths
		"/etc/ascend_install.info": "/etc/ascend_install.info",
		
		// Cache directory for model downloads and compilation artifacts
		// Writable mount to persist downloaded models across container restarts
		"/root/.cache": "/root/.cache",
	}
}

// RequiresPrivileged indicates whether the container needs privileged mode.
//
// Ascend containers require privileged mode (--privileged=true) because:
//   1. NPU drivers need direct hardware access beyond standard device mounts
//   2. CANN runtime performs privileged operations for memory management
//   3. Some driver initialization requires elevated permissions
//
// While less secure than capability-based access, this is currently required
// for proper Ascend NPU operation with vLLM.
//
// Returns:
//   - true: Ascend containers must run in privileged mode
func (s *AscendSandbox) RequiresPrivileged() bool {
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
func (s *AscendSandbox) GetCapabilities() []string {
	return []string{
		"SYS_ADMIN",     // Required for device initialization
		"SYS_RAWIO",     // Required for direct device I/O
		"IPC_LOCK",      // Required for memory locking
		"SYS_RESOURCE",  // Required for resource limit adjustments
	}
}

// GetDefaultImage returns the default Docker image for Ascend vLLM.
//
// This method automatically selects the appropriate image based on:
//   - Chip model (e.g., Ascend 910B vs 310P) from device information
//   - System architecture (ARM64 vs x86_64) detected automatically
//
// The image is looked up from the runtime_images.yaml configuration file.
// If the configuration is not available or the image is not found, an error is returned.
//
// Parameters:
//   - devices: List of devices (uses first device's ModelName for chip identification)
//
// Returns:
//   - Docker image URL for vLLM on Ascend NPU
//   - Error if image configuration is not found
func (s *AscendSandbox) GetDefaultImage(devices []runtime.DeviceInfo) (string, error) {
	// Load runtime images configuration
	runtimeImages, err := config.LoadRuntimeImagesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load runtime images config: %w", err)
	}
	
	return runtime.GetImageForEngine(runtimeImages, devices, "vllm")
}

// GetDockerRuntime returns the Docker runtime to use for Ascend devices.
//
// Returns:
//   - "runc" - Standard OCI runtime (not ascend-docker-runtime)
//
// Note: We explicitly use "runc" to avoid Docker auto-selecting ascend-docker-runtime.
// We access Ascend NPUs via device mounts + privileged mode using standard runtime.
func (s *AscendSandbox) GetDockerRuntime() string {
	return "runc"
}

// supportedDeviceTypes lists all device types supported by this sandbox.
//
// When adding support for new Ascend devices, add their config_key here.
// Each device type must match the config_key from devices.yaml configuration.
var supportedDeviceTypes = []string{
	"ascend-910b",
	"ascend-310p",
	// Add more Ascend device types here as they are tested and validated
}

// Supports checks if this sandbox supports the given device type.
//
// AscendSandbox only supports explicitly listed Ascend NPU device types.
// This ensures that each device type is explicitly validated and tested
// before being enabled in production.
//
// Parameters:
//   - deviceType: Device type string (e.g., "ascend-910b", "ascend-310p")
//
// Returns:
//   - true if this device type is in the supported list
//
// Example:
//
//	sandbox := NewAscendSandbox()
//	if sandbox.Supports("ascend-910b") {
//	    // Use this sandbox
//	}
func (s *AscendSandbox) Supports(deviceType string) bool {
	for _, supported := range supportedDeviceTypes {
		if deviceType == supported {
			return true
		}
	}
	return false
}
