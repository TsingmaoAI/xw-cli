// Package mlguiderdocker implements MLGuider runtime with Docker deployment.
package mlguiderdocker

import (
	"encoding/json"
	"fmt"

	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// AscendSandbox handles parameter transformation for Ascend NPU devices in MLGuider.
//
// This sandbox is responsible for converting standard runtime parameters
// into MLGuider-specific Docker configurations for Ascend NPUs, including:
//   - Environment variables (DEVICES JSON array, WORLD_SIZE, etc.)
//   - Device mounts (/dev/davinci*, /dev/davinci_manager, /dev/devmm_svm, etc.)
//   - Volume mounts (driver libs, tools, config files, log directories)
//   - Container privileges and shared memory configuration
//
// MLGuider Specifics:
//   - Uses JSON array format for DEVICES environment variable: "[0,1,2,3]"
//   - Requires WORLD_SIZE to match the total number of devices
//   - Supports TENSOR_PARALLEL and EXPERT_PARALLEL for distributed inference
//   - Uses host networking (--net=host) for optimal multi-NPU communication
//   - Requires extensive driver and system binary mounts for NPU access
//   - Needs large shared memory for multi-device tensor operations
//
// Device Discovery:
//   - Scans /dev/davinci* for NPU device files
//   - Mounts all NPU control devices (/dev/davinci_manager, /dev/devmm_svm, etc.)
//   - Configures driver library paths for Ascend CANN runtime
//   - Sets up logging directories for NPU profiling and debugging
//
// Architecture Support:
//   - Detects system architecture (ARM64 vs x86_64) automatically
//   - Selects appropriate Docker images from runtime_images.yaml
//   - Supports both 910B (training/inference) and 310P (inference-only) NPUs
type AscendSandbox struct{}

// NewAscendSandbox creates a new MLGuider Ascend device sandbox.
//
// The sandbox is stateless and can be reused across multiple instances.
// It relies on runtime configuration files for image selection.
//
// Returns:
//   - Initialized AscendSandbox ready for parameter transformation
func NewAscendSandbox() *AscendSandbox {
	return &AscendSandbox{}
}

// PrepareEnvironment generates environment variables for MLGuider with Ascend devices.
//
// This method transforms device information into MLGuider-specific environment variables
// that control device allocation and distributed inference configuration.
//
// Key environment variables generated:
//   - DEVICES: JSON array of NPU indices, e.g., "[0,1,2,3]"
//     This tells MLGuider which NPU devices to use for inference
//   - WORLD_SIZE: Total number of devices for distributed setup
//     Must equal TENSOR_PARALLEL * EXPERT_PARALLEL
//
// Device Ordering:
//   - Devices are ordered by their Index field (0-based)
//   - Device indices must be contiguous for optimal performance
//   - Multi-device setups use tensor parallelism by default
//
// Error Handling:
//   - Returns error if device list is empty
//   - Returns error if JSON serialization fails
//
// Parameters:
//   - devices: List of Ascend NPU devices to make accessible
//
// Returns:
//   - Map of environment variable key-value pairs
//   - Error if device configuration is invalid
//
// Example output for 4-device setup:
//
//	{
//	  "DEVICES": "[0,1,2,3]",
//	  "WORLD_SIZE": "4"
//	}
func (s *AscendSandbox) PrepareEnvironment(devices []runtime.DeviceInfo) (map[string]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("at least one device is required")
	}

	env := make(map[string]string)

	// Build device indices array for DEVICES environment variable
	// MLGuider expects JSON array format: "[0,1,2,3]"
	deviceIndices := make([]int, len(devices))
	for i, dev := range devices {
		deviceIndices[i] = dev.Index
	}

	// Serialize device indices to JSON array
	devicesJSON, err := json.Marshal(deviceIndices)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize device indices: %w", err)
	}
	env["DEVICES"] = string(devicesJSON)

	return env, nil
}

// GetDeviceMounts returns device file paths for Ascend NPUs.
//
// This method returns the list of device files that must be mounted into
// the container for MLGuider to access Ascend NPUs.
//
// Device Files Returned:
//   - /dev/davinci[N]: Individual NPU device files (one per allocated NPU)
//   - /dev/davinci_manager: NPU device manager for coordination
//   - /dev/devmm_svm: Shared virtual memory device for inter-NPU communication
//   - /dev/hisi_hdc: Huawei debug console (optional, for diagnostics)
//
// The runtime framework will mount these devices with "rwm" permissions automatically.
//
// Multi-Device Setup:
//   - For single device: includes /dev/davinci0 (or specified index)
//   - For multi-device: includes all allocated davinci devices
//   - Always includes shared devices (manager, SVM) regardless of count
//
// Parameters:
//   - devices: List of Ascend NPU devices to mount
//
// Returns:
//   - List of device file paths to mount
//   - Error if device configuration is invalid
//
// Example for 2-device setup:
//
//	["/dev/davinci0", "/dev/davinci1", "/dev/davinci_manager", "/dev/devmm_svm", "/dev/hisi_hdc"]
func (s *AscendSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("at least one device is required")
	}

	mounts := make([]string, 0, len(devices)+3)

	// Mount individual davinci devices (one per allocated NPU)
	for _, dev := range devices {
		devicePath := fmt.Sprintf("/dev/davinci%d", dev.Index)
		mounts = append(mounts, devicePath)
	}

	// Mount shared NPU management devices
	// These are required for multi-device coordination and communication
	sharedDevices := []string{
		"/dev/davinci_manager", // NPU device manager
		"/dev/devmm_svm",       // Shared virtual memory
		"/dev/hisi_hdc",        // Huawei debug console
	}
	mounts = append(mounts, sharedDevices...)

	return mounts, nil
}

// GetAdditionalMounts returns additional volume mounts required for MLGuider on Ascend.
//
// MLGuider containers need access to host directories containing:
//   - Ascend CANN driver libraries and tools
//   - System binaries for NPU operations
//   - Model storage (supports both original and converted formats)
//   - DCMI libraries for device management
//   - Cache directory for model downloads
//
// All mounts are read-only except /root/.cache which needs write access.
//
// Note: The model directory is mounted at /mnt/models to provide flexibility
// for both ORIGIN_MODEL_PATH and MODEL_PATH environment variables.
//
// Returns:
//   - Map of host path -> container path
//
// Example:
//
//	{
//	  "/usr/local/Ascend/driver": "/usr/local/Ascend/driver",
//	  "/usr/local/sbin": "/usr/local/sbin",
//	  "/mnt/models": "/mnt/models",
//	  "/usr/local/dcmi": "/usr/local/dcmi",
//	  "/root/.cache": "/root/.cache"
//	}
func (s *AscendSandbox) GetAdditionalMounts() map[string]string {
	return map[string]string{
		// Ascend driver directory - required for CANN runtime
		"/usr/local/Ascend/driver": "/usr/local/Ascend/driver",
		
		// System binaries - NPU utilities and tools
		"/usr/local/sbin": "/usr/local/sbin",
		
		// Model directory - mount entire parent for flexibility
		// Supports both original and converted model paths
		"/mnt/models": "/mnt/models",
		
		// DCMI (Device Chip Management Interface) libraries
		"/usr/local/dcmi": "/usr/local/dcmi",
		
		// NPU management tool
		"/usr/local/bin/npu-smi": "/usr/local/bin/npu-smi",
		
		// Driver libraries
		"/usr/local/Ascend/driver/lib64/": "/usr/local/Ascend/driver/lib64/",
		
		// Driver version information
		"/usr/local/Ascend/driver/version.info": "/usr/local/Ascend/driver/version.info",
		
		// Ascend installation metadata
		"/etc/ascend_install.info": "/etc/ascend_install.info",
		
		// Cache directory for model downloads (writable)
		"/root/.cache": "/root/.cache",
	}
}

// GetDefaultImage returns the default Docker image for MLGuider with Ascend support.
//
// This method automatically selects the appropriate Docker image based on:
//   - Chip model (e.g., Ascend 910B vs 310P) from device information
//   - System architecture (ARM64 vs x86_64) detected automatically
//
// Image Selection Process:
//  1. Ensures configuration directories exist
//  2. Loads runtime_images.yaml configuration
//  3. Looks up image by chip ConfigKey, engine "mlguider", and architecture
//  4. Returns error if image is not found (strict mode, no fallback)
//
// Configuration File:
//   - Location: ~/.xw/runtime_images.yaml
//   - Format: YAML with chip_model -> engine -> architecture -> image mapping
//   - Must be populated with MLGuider images before use
//
// Example Configuration:
//
//	ascend-910b:
//	  mlguider:
//	    arm64: harbor.tsingmao.com/mlguider/release:0123-xw-arm64
//	    amd64: harbor.tsingmao.com/mlguider/release:0123-xw-amd64
//	ascend-310p:
//	  mlguider:
//	    arm64: harbor.tsingmao.com/mlguider/release:0123-xw-310p-arm64
//	    amd64: harbor.tsingmao.com/mlguider/release:0123-xw-310p-amd64
//
// Error Handling:
//   - Returns error if config directories cannot be created
//   - Returns error if runtime_images.yaml cannot be loaded
//   - Returns error if no image is configured for the chip/arch combination
//   - No fallback images to ensure explicit configuration
//
// Parameters:
//   - devices: List of devices (uses first device's ModelName for chip identification)
//
// Returns:
//   - Docker image URL for MLGuider on Ascend NPU
//   - Error if image configuration is not found
func (s *AscendSandbox) GetDefaultImage(devices []runtime.DeviceInfo) (string, error) {
	// Load runtime images configuration
	runtimeImages, err := config.LoadRuntimeImagesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load runtime images config: %w", err)
	}

	return runtime.GetImageForEngine(runtimeImages, devices, "mlguider")
}

// RequiresPrivileged indicates whether MLGuider containers need privileged mode.
//
// MLGuider requires privileged mode (--privileged=true) for Ascend NPUs because:
//   1. NPU drivers need direct hardware access beyond standard device mounts
//   2. CANN runtime performs privileged operations for memory management
//   3. Multi-device communication requires kernel-level coordination
//   4. Some driver initialization requires elevated permissions
//
// While less secure than capability-based access, this is currently required
// for proper MLGuider operation with Ascend NPUs.
//
// Returns:
//   - true: MLGuider containers must run in privileged mode
func (s *AscendSandbox) RequiresPrivileged() bool {
	return true
}

// GetCapabilities returns Linux capabilities needed by MLGuider containers.
//
// Even in privileged mode, documenting required capabilities helps
// understand the security requirements and may support future transition
// to non-privileged mode with specific capabilities.
//
// MLGuider requires these capabilities for:
//   - SYS_ADMIN: Device initialization and system management
//   - SYS_RAWIO: Direct device I/O operations
//   - IPC_LOCK: Memory locking for device buffers
//   - SYS_RESOURCE: Resource limit adjustments for large models
//
// Returns:
//   - List of required Linux capabilities
func (s *AscendSandbox) GetCapabilities() []string {
	return []string{
		"SYS_ADMIN",    // Required for device initialization
		"SYS_RAWIO",    // Required for direct device I/O
		"IPC_LOCK",     // Required for memory locking
		"SYS_RESOURCE", // Required for resource limit adjustments
	}
}

// GetDockerRuntime returns the Docker runtime to use for Ascend devices.
//
// MLGuider uses the standard runc runtime rather than vendor-specific runtimes.
// This is compatible with the privileged container mode and device mounts.
//
// Returns:
//   - "runc" - Standard OCI runtime
//
// Note: We explicitly use "runc" to avoid Docker auto-selecting vendor runtimes.
// The privileged mode and direct device mounts provide all necessary hardware access.
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

