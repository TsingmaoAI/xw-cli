// Package mindiedocker implements MindIE runtime with Docker deployment.
package mindiedocker

import (
	"fmt"
	"strings"

	"github.com/tsingmao/xw/internal/config"
	"github.com/tsingmao/xw/internal/runtime"
)

// AscendSandbox handles parameter transformation for Ascend NPU devices in MindIE.
//
// This sandbox is responsible for converting standard runtime parameters
// into MindIE-specific Docker configurations for Ascend NPUs, including:
//   - Environment variables (MINDIE_NPU_DEVICE_IDS, WORLD_SIZE, etc.)
//   - Device mounts (/dev/davinci*, /dev/davinci_manager, etc.)
//   - Volume mounts (driver libs, tools, config files, log directories)
//   - Container privileges and shared memory configuration
//
// MindIE Specifics:
//   - Uses MINDIE_NPU_DEVICE_IDS instead of ASCEND_RT_VISIBLE_DEVICES
//   - Requires WORLD_SIZE to match the number of devices
//   - Supports MAX_MODEL_LEN and MAX_INPUT_LEN for model configuration
//   - Requires extensive log directory mounts for NPU profiling and debugging
//   - Needs large shared memory (--shm-size) for multi-device communication
type AscendSandbox struct{}

// NewAscendSandbox creates a new MindIE Ascend device sandbox.
func NewAscendSandbox() *AscendSandbox {
	return &AscendSandbox{}
}

// PrepareEnvironment generates environment variables for MindIE with Ascend devices.
//
// Key environment variables:
//   - MINDIE_NPU_DEVICE_IDS: Comma-separated NPU indices visible to MindIE
//     Example: "0,1,2,3" makes NPU 0-3 accessible
//
// Additional environment variables (set by runtime.Create):
//   - MODEL_PATH: Container-internal path where model files are mounted (/mnt/model)
//   - MODEL_NAME: Model name used for inference requests (instance alias or model ID)
//   - WORLD_SIZE: Set by Manager (TENSOR_PARALLEL * PIPELINE_PARALLEL)
//   - TENSOR_PARALLEL: Set by Manager (optional, for engines that support it)
//
// Parameters:
//   - devices: List of Ascend devices to make visible
//
// Returns:
//   - Environment variable map with MindIE-specific settings
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
	deviceIDs := strings.Join(indices, ",")

	env := map[string]string{
		// Primary device visibility variable for MindIE
		"MINDIE_NPU_DEVICE_IDS": deviceIDs,
	}

	return env, nil
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

// GetAdditionalMounts returns additional volume mounts required for MindIE on Ascend.
//
// MindIE containers need extensive host directory access for:
//   - Driver libraries: Low-level NPU driver interfaces
//   - Add-ons: Additional CANN components and utilities
//   - Management tools: npu-smi for device monitoring
//   - System binaries: Additional system utilities
//   - Log directories: NPU logs, profiling data, and dump files
//   - Cache directory: For model downloads and runtime artifacts
//
// Log Mounts Rationale:
//   MindIE generates extensive logs for debugging and profiling:
//   - /var/log/npu/slog: System logs from CANN runtime
//   - /var/log/npu/profiling: Performance profiling data
//   - /var/log/npu/dump: Core dumps and error diagnostics
//   - /var/log/npu -> /usr/slog: Legacy log path compatibility
//
// All mounts are read-only except writable log and cache directories.
//
// Returns:
//   - Map of host path -> container path
func (s *AscendSandbox) GetAdditionalMounts() map[string]string {
	return map[string]string{
		// Ascend driver libraries required by CANN runtime
		"/usr/local/Ascend/driver": "/usr/local/Ascend/driver",
		
		// Ascend add-ons: Additional CANN components, utilities, and plugins
		"/usr/local/Ascend/add-ons": "/usr/local/Ascend/add-ons",
		
		// System binaries directory for additional utilities
		"/usr/local/sbin": "/usr/local/sbin",
		
		// npu-smi: NPU System Management Interface tool
		// Similar to nvidia-smi, used for querying NPU status
		"/usr/local/bin/npu-smi": "/usr/local/bin/npu-smi",
		
		// NPU system logs (CANN runtime logs)
		// Writable mount for container to write system logs
		"/var/log/npu/slog": "/var/log/npu/slog",
		
		// NPU profiling data directory
		// Writable mount for performance profiling output
		"/var/log/npu/profiling": "/var/log/npu/profiling",
		
		// NPU dump directory for core dumps and diagnostics
		// Writable mount for error dumps and crash reports
		"/var/log/npu/dump": "/var/log/npu/dump",
		
		// Legacy log path compatibility
		// Maps host /var/log/npu to container /usr/slog for older applications
		"/var/log/npu": "/usr/slog",
		
		// Cache directory for model downloads and compilation artifacts
		// Writable mount to persist downloaded models across container restarts
		"/root/.cache": "/root/.cache",
	}
}

// RequiresPrivileged indicates whether the container needs privileged mode.
//
// MindIE containers require privileged mode (--privileged=true) because:
//   1. NPU drivers need direct hardware access beyond standard device mounts
//   2. CANN runtime performs privileged operations for memory management
//   3. Distributed inference requires advanced IPC and networking capabilities
//   4. Log directory access may require elevated permissions
//
// While less secure than capability-based access, this is currently required
// for proper Ascend NPU operation with MindIE.
//
// Returns:
//   - true: MindIE containers must run in privileged mode
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
		"SYS_ADMIN",    // Required for device initialization and mount operations
		"SYS_RAWIO",    // Required for direct device I/O access
		"IPC_LOCK",     // Required for memory locking in distributed inference
		"SYS_RESOURCE", // Required for resource limit adjustments
		"NET_ADMIN",    // Required for advanced networking in distributed scenarios
	}
}

// GetDefaultImage returns the default Docker image for MindIE with Ascend support.
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
//   - Docker image URL for MindIE on Ascend NPU
//   - Error if image configuration is not found
func (s *AscendSandbox) GetDefaultImage(devices []runtime.DeviceInfo) (string, error) {
	// Load runtime images configuration
	runtimeImages, err := config.LoadRuntimeImagesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load runtime images config: %w", err)
	}
	
	return runtime.GetImageForEngine(runtimeImages, devices, "mindie")
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

// GetSharedMemorySize returns the shared memory size required for MindIE.
//
// MindIE requires large shared memory for:
//   - Inter-process communication in distributed inference
//   - Tensor sharing between NPU processes
//   - Model parallel computation buffers
//
// The default 64MB shared memory in Docker is insufficient for multi-device
// inference workloads. MindIE typically requires 500GB for optimal performance
// with 4+ NPUs.
//
// Returns:
//   - Shared memory size in bytes (500GB = 500 * 1024^3)
func (s *AscendSandbox) GetSharedMemorySize() int64 {
	// 500GB shared memory for multi-device distributed inference
	return 500 * 1024 * 1024 * 1024
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

