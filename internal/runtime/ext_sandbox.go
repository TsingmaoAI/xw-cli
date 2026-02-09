// Package runtime provides runtime implementations for model inference backends.
//
// This file implements the ExtSandbox, a configuration-driven device sandbox
// that enables support for additional AI accelerator chips without code changes.
package runtime

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// ExtSandbox is a configuration-driven implementation of the DeviceSandbox interface.
//
// ExtSandbox allows users to define device sandbox behavior through YAML configuration
// files, making it easy to add support for new AI accelerator chips without modifying
// source code or recompiling the application.
//
// Design philosophy:
//   - Simple, common scenarios: Use ExtSandbox with YAML configuration
//   - Complex, custom scenarios: Implement code-based sandboxes (e.g., AscendSandbox)
//
// The configuration-driven approach is ideal for:
//   - Niche accelerator chips with small user bases
//   - Rapid prototyping and testing of new devices
//   - Environment-specific customizations
//
// Limitations:
//   - Device environment variable must use comma-separated format
//   - Device paths must follow predictable naming patterns
//   - No support for dynamic runtime configuration
//
// For advanced use cases requiring custom logic, implement a dedicated sandbox
// in code (see ascend_sandbox.go, metax_sandbox.go for examples).
type ExtSandbox struct {
	// deviceType is the device config_key from devices.yaml (e.g., "kunlun-r200")
	deviceType string

	// engineName is the inference engine this sandbox is for (e.g., "vllm", "mindie")
	engineName string

	// conf contains the parsed configuration from sandboxes.yaml
	conf *config.ExtSandboxConfig
}

// devicePathPattern is a compiled regex for extracting trailing numbers from device paths.
// Matches paths like /dev/xpu0, /dev/cambricon_dev5, /dev/dri/card2
var devicePathPattern = regexp.MustCompile(`(\d+)$`)

// NewExtSandbox creates a new configuration-driven device sandbox.
//
// Parameters:
//   - deviceType: The device config_key (e.g., "kunlun-r200", "cambricon-mlu370")
//   - conf: The parsed configuration from sandboxes.yaml
//
// Returns:
//   - A fully initialized ExtSandbox implementing the DeviceSandbox interface
//
// Example usage:
//
//	cfg := &config.ExtSandboxConfig{
//	    DeviceEnv: "XPU_VISIBLE_DEVICES",
//	    Devices: []string{"/dev/xpu0", "/dev/xpu1", "/dev/xpu_ctl"},
//	    // ... other fields
//	}
//	sandbox := runtime.NewExtSandbox("kunlun-r200", cfg)
func NewExtSandbox(deviceType string, engineName string, conf *config.ExtSandboxConfig) DeviceSandbox {
	return &ExtSandbox{
		deviceType: deviceType,
		engineName: engineName,
		conf:       conf,
	}
}

// Supports checks if this sandbox supports the specified device type.
//
// This method is used by runtimes to automatically select the appropriate
// sandbox implementation for a given device type during instance creation.
//
// Parameters:
//   - deviceType: Device config_key to check (e.g., "kunlun-r200")
//
// Returns:
//   - true if this sandbox was created for the specified device type
func (s *ExtSandbox) Supports(deviceType string) bool {
	return s.deviceType == deviceType
}

// PrepareEnvironment generates environment variables for the container.
//
// This method sets up the runtime environment required for the device to
// function properly inside the container. It combines:
//  1. Static environment variables from the configuration
//  2. Dynamic device visibility variable (comma-separated device indices)
//
// The device visibility environment variable (specified by DeviceEnv in config)
// is automatically populated with a comma-separated list of allocated device
// indices. For example, if devices 0, 2, and 5 are allocated:
//   XPU_VISIBLE_DEVICES=0,2,5
//
// Parameters:
//   - devices: List of allocated devices to make visible in the container
//
// Returns:
//   - Map of environment variable names to values
//   - Error if device configuration is invalid
//
// Example:
//
//	env, err := sandbox.PrepareEnvironment([]DeviceInfo{
//	    {Index: 0, ...},
//	    {Index: 2, ...},
//	})
//	// env = {
//	//   "XPU_VISIBLE_DEVICES": "0,2",
//	//   "XPU_WORKER_MULTIPROC_METHOD": "spawn",
//	// }
func (s *ExtSandbox) PrepareEnvironment(devices []DeviceInfo) (map[string]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no devices provided for %s", s.deviceType)
	}

	env := make(map[string]string)

	// Copy static environment variables from configuration
	for k, v := range s.conf.Environment {
		env[k] = v
	}

	// Set device visibility environment variable
	// Standard format: comma-separated list of device indices (e.g., "0,1,2")
	if s.conf.DeviceEnv != "" {
		indices := make([]string, len(devices))
		for i, dev := range devices {
			indices[i] = strconv.Itoa(dev.Index)
		}
		env[s.conf.DeviceEnv] = strings.Join(indices, ",")
	}

	logger.Debug("Prepared environment for %s: %d variables, %d devices",
		s.deviceType, len(env), len(devices))

	return env, nil
}

// GetDeviceMounts returns device node paths that must be mounted into the container.
//
// Device mounting follows an intelligent pattern-matching approach:
//   - Paths ending with a digit (e.g., /dev/xpu0, /dev/xpu5):
//     Mounted only when the corresponding device index is allocated
//   - Paths not ending with a digit (e.g., /dev/xpu_ctl, /dev/xpu_manager):
//     Mounted as shared devices for all containers
//
// This approach allows users to list all possible device paths in the configuration,
// and the system automatically selects the appropriate subset based on actual
// device allocation.
//
// Parameters:
//   - devices: List of allocated devices
//
// Returns:
//   - List of device paths to mount with "rwm" permissions
//   - Error if device configuration is invalid
//
// Example:
//
//	// Configuration lists:
//	// devices: [/dev/xpu0, /dev/xpu1, /dev/xpu2, /dev/xpu_ctl]
//	//
//	// Allocated devices: 0, 2
//	//
//	// Result:
//	mounts, _ := sandbox.GetDeviceMounts([]DeviceInfo{{Index: 0}, {Index: 2}})
//	// mounts = ["/dev/xpu0", "/dev/xpu2", "/dev/xpu_ctl"]
func (s *ExtSandbox) GetDeviceMounts(devices []DeviceInfo) ([]string, error) {
	if len(devices) == 0 {
		return nil, fmt.Errorf("no devices provided for %s", s.deviceType)
	}

	// Build set of allocated device indices for O(1) lookup
	allocatedIndices := make(map[int]bool, len(devices))
	for _, dev := range devices {
		allocatedIndices[dev.Index] = true
	}

	mounts := make([]string, 0, len(s.conf.Devices))

	// Process each configured device path
	for _, devicePath := range s.conf.Devices {
		idx := extractTrailingNumber(devicePath)

		if idx >= 0 {
			// Indexed device: mount only if this index is allocated
			if allocatedIndices[idx] {
				mounts = append(mounts, devicePath)
			}
		} else {
			// Shared device: always mount
			mounts = append(mounts, devicePath)
		}
	}

	logger.Debug("Device mounts for %s: %d paths (%d indexed, %d shared)",
		s.deviceType, len(mounts), len(devices), len(mounts)-len(devices))

	return mounts, nil
}

// GetAdditionalMounts returns volume mounts required by the device runtime.
//
// These mounts typically include:
//   - Driver libraries and shared objects
//   - Device management tools (e.g., npu-smi, xpu-smi)
//   - Configuration files and metadata
//   - Cache directories for model downloads
//
// Volume paths are specified in "host:container" format in the configuration.
// If no container path is provided (no colon), the same path is used for both.
//
// Returns:
//   - Map of host path to container path
//
// Example:
//
//	// Configuration:
//	// volumes: ["/usr/local/xpu:/usr/local/xpu", "/root/.cache"]
//	//
//	// Result:
//	mounts := sandbox.GetAdditionalMounts()
//	// mounts = {
//	//   "/usr/local/xpu": "/usr/local/xpu",
//	//   "/root/.cache": "/root/.cache",
//	// }
func (s *ExtSandbox) GetAdditionalMounts() map[string]string {
	result := make(map[string]string, len(s.conf.Volumes))

	for _, mount := range s.conf.Volumes {
		// Split on first colon to handle Windows-style paths if needed
		parts := strings.SplitN(mount, ":", 2)

		if len(parts) == 2 {
			// Explicit host:container mapping
			result[parts[0]] = parts[1]
		} else {
			// No container path specified, use same path for both
			result[parts[0]] = parts[0]
		}
	}

	return result
}

// RequiresPrivileged indicates whether the container needs privileged mode.
//
// Privileged mode (--privileged=true) grants the container extended permissions,
// including access to all devices and the ability to modify system settings.
//
// While less secure than capability-based access control, some accelerator
// drivers require privileged mode for proper operation, particularly for:
//   - Direct hardware access beyond standard device mounts
//   - Kernel module interactions
//   - Memory management operations
//
// Returns:
//   - true if privileged mode is required (from configuration)
func (s *ExtSandbox) RequiresPrivileged() bool {
	return s.conf.Privileged
}

// GetCapabilities returns Linux capabilities required by the container.
//
// Linux capabilities provide fine-grained privilege control as an alternative
// to full privileged mode. Common capabilities for AI accelerators include:
//   - SYS_ADMIN: System administration operations
//   - SYS_RAWIO: Direct device I/O access
//   - IPC_LOCK: Memory locking for device buffers
//   - SYS_RESOURCE: Resource limit adjustments
//
// Note: Even when privileged mode is used, documenting required capabilities
// helps understand security requirements and may support future migration to
// non-privileged mode.
//
// Returns:
//   - List of capability names (e.g., ["SYS_ADMIN", "SYS_RAWIO"])
func (s *ExtSandbox) GetCapabilities() []string {
	return s.conf.Capabilities
}

// GetDefaultImage returns the default Docker image for this device type.
//
// Note: ExtSandbox delegates image selection to the runtime_images.yaml
// configuration file. This method is not directly used by ExtSandbox but
// is required by the DeviceSandbox interface.
//
// The actual image lookup is performed by the runtime using the device's
// ConfigKey and engine name.
//
// Returns:
//   - Empty string (image selection handled by runtime)
//   - Error indicating this method is not supported
func (s *ExtSandbox) GetDefaultImage(devices []DeviceInfo) (string, error) {
	// Load runtime images configuration
	runtimeImages, err := config.LoadRuntimeImagesConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load runtime images config: %w", err)
	}
	
	return GetImageForEngine(runtimeImages, devices, s.engineName)
}

// GetDockerRuntime returns the Docker runtime to use for this device.
//
// Docker supports pluggable runtimes that can provide device-specific
// functionality. Common runtimes include:
//   - runc: Standard OCI runtime (most common)
//   - nvidia: NVIDIA Container Runtime
//   - kata-runtime: Lightweight VM isolation
//
// Returns:
//   - Runtime name from configuration, or "runc" if not specified
func (s *ExtSandbox) GetDockerRuntime() string {
	if s.conf.Runtime != "" {
		return s.conf.Runtime
	}
	return "runc"
}

// GetSharedMemorySize returns the shared memory size required for the container.
//
// Shared memory (--shm-size) is critical for:
//   - PyTorch DataLoader workers in multi-process mode
//   - KV cache management across processes
//   - Tensor sharing in distributed inference
//   - Inter-process communication in multi-device setups
//
// The default Docker shared memory size (64MB) is often insufficient for
// large model inference workloads.
//
// Returns:
//   - Shared memory size in bytes (configured value or 16GB default)
func (s *ExtSandbox) GetSharedMemorySize() int64 {
	if s.conf.ShmSizeGB > 0 {
		return int64(s.conf.ShmSizeGB) * 1024 * 1024 * 1024
	}
	// Default to 16GB (conservative default matching mindie-docker)
	return 16 * 1024 * 1024 * 1024
}

// extractTrailingNumber extracts the trailing numeric value from a device path.
//
// This helper function is used to identify indexed device paths and extract
// their corresponding device index.
//
// Examples:
//   - /dev/xpu0 -> 0
//   - /dev/cambricon_dev5 -> 5
//   - /dev/dri/card2 -> 2
//   - /dev/xpu_ctl -> -1 (no trailing number)
//
// Parameters:
//   - path: Device path to analyze
//
// Returns:
//   - Extracted device index (>= 0) if path ends with a number
//   - -1 if path does not end with a number
func extractTrailingNumber(path string) int {
	matches := devicePathPattern.FindStringSubmatch(path)
	if len(matches) > 1 {
		// matches[1] contains the captured digit group
		num, err := strconv.Atoi(matches[1])
		if err == nil {
			return num
		}
	}
	return -1
}

// LoadExtendedSandboxes loads extended sandbox configurations for a specific engine.
//
// This helper function provides a centralized way for runtimes to discover
// and load configuration-based sandboxes. It encapsulates the common logic
// of reading ext_sandboxes from devices.yaml and creating ExtSandbox instances
// for the specified engine (vllm, mindie, or mlguider).
//
// The function is designed to be called during runtime initialization (init)
// to register extended sandboxes alongside core code-based sandboxes.
//
// Configuration structure in devices.yaml:
//
//	chip_models:
//	  - config_key: kunlun-r200
//	    model_name: Kunlun XPU R200
//	    device_id: "0x1234"
//	    ext_sandboxes:
//	      vllm:
//	        device_env: XPU_VISIBLE_DEVICES
//	        devices: [/dev/xpu0, /dev/xpu1, /dev/xpu_ctl]
//	        # ... other config
//	      mindie:
//	        device_env: XPU_VISIBLE_DEVICES
//	        # ... mindie-specific config
//
// Parameters:
//   - engineName: Engine name to load configs for ("vllm", "mindie", "mlguider")
//
// Returns:
//   - List of sandbox constructor functions for the specified engine
//   - Empty list if device config is not loaded or no extended sandboxes are defined
//
// Example usage in vllm-docker runtime initialization:
//
//	func init() {
//	    extSandboxes := runtime.LoadExtendedSandboxes("vllm")
//	    if len(extSandboxes) > 0 {
//	        sandboxRegistry = append(sandboxRegistry, extSandboxes...)
//	    }
//	}
func LoadExtendedSandboxes(engineName string) []func() DeviceSandbox {
	// Load extended sandbox configurations from devices.yaml
	extConfigs := config.LoadExtSandboxesFromDevices(engineName)

	if len(extConfigs) == 0 {
		logger.Debug("No extended sandboxes configured for %s", engineName)
		return nil
	}

	// Build list of sandbox constructors for this engine
	sandboxes := make([]func() DeviceSandbox, 0, len(extConfigs))

	for deviceType, sandboxConfig := range extConfigs {
		// Capture loop variables for closure
		dt := deviceType
		eng := engineName
		cfg := sandboxConfig

		sandboxes = append(sandboxes, func() DeviceSandbox {
			return NewExtSandbox(dt, eng, cfg)
		})

		logger.Info("Loaded extended sandbox for %s: %s", engineName, deviceType)
	}

	return sandboxes
}

