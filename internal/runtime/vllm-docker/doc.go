// Package vllmdocker implements vLLM runtime with Docker deployment.
//
// # Overview
//
// This package provides a Docker-based runtime for running vLLM inference engine
// with support for various AI accelerators (Ascend NPU, MetaX, etc.). The runtime
// manages the complete lifecycle of containerized model instances.
//
// # Architecture
//
// The runtime uses a configuration-driven approach where device-specific behavior
// is defined in YAML configuration files (configs/devices.yaml) rather than
// hard-coded implementations. This design allows:
//
//   - Adding new chip support without recompiling binaries
//   - Quick configuration updates for driver changes
//   - User customization of device behavior
//
// # Configuration-Driven Device Support
//
// All device configurations are now defined in configs/devices.yaml under the
// ext_sandboxes section. For example:
//
//	chip_models:
//	  - config_key: ascend-910b
//	    ext_sandboxes:
//	      vllm:
//	        device_env: ASCEND_RT_VISIBLE_DEVICES
//	        devices:
//	          - /dev/davinci0
//	          - /dev/davinci_manager
//	        volumes:
//	          - /usr/local/Ascend/driver:/usr/local/Ascend/driver
//	        privileged: true
//	        shm_size_gb: 100
//
// # When to Use Code-Based Sandboxes
//
// While configuration-driven approach is preferred for most cases, you may need
// to implement a code-based DeviceSandbox when:
//
//  1. Complex Logic Required:
//     - Dynamic device path generation based on system state
//     - Conditional mounting based on driver version detection
//     - Custom device index to path mapping algorithms
//
//  2. Runtime Decisions:
//     - Detect and handle different hardware variants at runtime
//     - Fallback logic when certain devices are unavailable
//     - Performance optimizations based on device capabilities
//
//  3. Integration with External Systems:
//     - Query device management APIs for configuration
//     - Interact with vendor-specific device managers
//     - Handle complex multi-step initialization sequences
//
// # Implementing a Code-Based Sandbox
//
// To implement a custom device sandbox, follow these steps:
//
// Step 1: Create a sandbox struct implementing runtime.DeviceSandbox interface:
//
//	package vllmdocker
//
//	import "github.com/tsingmaoai/xw-cli/internal/runtime"
//
//	// CustomSandbox handles device-specific configuration for CustomChip.
//	type CustomSandbox struct{}
//
//	func NewCustomSandbox() *CustomSandbox {
//	    return &CustomSandbox{}
//	}
//
// Step 2: Implement all required interface methods:
//
//	// PrepareEnvironment generates environment variables for the container.
//	func (s *CustomSandbox) PrepareEnvironment(devices []runtime.DeviceInfo) (map[string]string, error) {
//	    // Build comma-separated device indices
//	    indices := make([]string, len(devices))
//	    for i, dev := range devices {
//	        indices[i] = fmt.Sprintf("%d", dev.Index)
//	    }
//
//	    return map[string]string{
//	        "CUSTOM_VISIBLE_DEVICES": strings.Join(indices, ","),
//	        "CUSTOM_LOG_LEVEL": "3",
//	    }, nil
//	}
//
//	// GetDeviceMounts returns device files to mount into container.
//	func (s *CustomSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
//	    mounts := make([]string, 0, len(devices)+1)
//
//	    // Mount individual device nodes
//	    for _, dev := range devices {
//	        mounts = append(mounts, fmt.Sprintf("/dev/custom%d", dev.Index))
//	    }
//
//	    // Add shared control device
//	    mounts = append(mounts, "/dev/custom_manager")
//
//	    return mounts, nil
//	}
//
//	// GetAdditionalMounts returns additional volume mounts (drivers, libs, etc.).
//	func (s *CustomSandbox) GetAdditionalMounts() map[string]string {
//	    return map[string]string{
//	        "/usr/local/custom/driver": "/usr/local/custom/driver",
//	        "/usr/local/bin/custom-smi": "/usr/local/bin/custom-smi",
//	        "/root/.cache": "/root/.cache",
//	    }
//	}
//
//	// RequiresPrivileged indicates if container needs privileged mode.
//	func (s *CustomSandbox) RequiresPrivileged() bool {
//	    return true
//	}
//
//	// GetCapabilities returns Linux capabilities required.
//	func (s *CustomSandbox) GetCapabilities() []string {
//	    return []string{"SYS_ADMIN", "SYS_RAWIO", "IPC_LOCK"}
//	}
//
//	// GetDefaultImage returns Docker image for this device type.
//	func (s *CustomSandbox) GetDefaultImage(devices []runtime.DeviceInfo) (string, error) {
//	    runtimeImages, err := config.LoadRuntimeImagesConfig()
//	    if err != nil {
//	        return "", fmt.Errorf("failed to load runtime images: %w", err)
//	    }
//	    return runtime.GetImageForEngine(runtimeImages, devices, "vllm")
//	}
//
//	// GetDockerRuntime returns Docker runtime to use.
//	func (s *CustomSandbox) GetDockerRuntime() string {
//	    return "runc"
//	}
//
//	// GetSharedMemorySize returns shared memory size in bytes.
//	func (s *CustomSandbox) GetSharedMemorySize() int64 {
//	    return 100 * 1024 * 1024 * 1024 // 100GB
//	}
//
//	// Supports checks if this sandbox supports the given device type.
//	func (s *CustomSandbox) Supports(deviceType string) bool {
//	    return deviceType == "custom-chip-x100"
//	}
//
// Step 3: Register the sandbox in NewRuntime():
//
//	func NewRuntime() (*Runtime, error) {
//	    base, err := runtime.NewDockerRuntimeBase("vllm-docker")
//	    if err != nil {
//	        return nil, err
//	    }
//
//	    // Register your custom sandbox
//	    base.RegisterCoreSandboxes([]func() runtime.DeviceSandbox{
//	        func() runtime.DeviceSandbox { return NewCustomSandbox() },
//	    })
//
//	    // ... rest of initialization
//	}
//
// # Selection Priority
//
// When a device type is used, the runtime selects sandboxes in this order:
//
//  1. Extended sandboxes from configs/devices.yaml (highest priority)
//  2. Core sandboxes registered via RegisterCoreSandboxes()
//  3. Error if no sandbox found
//
// This means configuration always overrides code, allowing users to customize
// behavior without modifying binaries.
//
// # Best Practices
//
//  1. Prefer Configuration: Use ext_sandboxes in devices.yaml for simple cases
//  2. Document Behavior: Add comprehensive comments explaining device requirements
//  3. Error Handling: Provide clear error messages for configuration issues
//  4. Validate Inputs: Check device indices, paths, and other parameters
//  5. Test Thoroughly: Verify sandbox works with actual hardware
//
// # Example: Complex Logic Requiring Code
//
// Here's a scenario that requires code-based implementation:
//
//	// DynamicSandbox queries system at runtime to determine device paths
//	type DynamicSandbox struct{}
//
//	func (s *DynamicSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
//	    mounts := []string{}
//
//	    // Query vendor API to get actual device paths
//	    vendorAPI := NewVendorAPI()
//	    for _, dev := range devices {
//	        path, err := vendorAPI.GetDevicePathForIndex(dev.Index)
//	        if err != nil {
//	            return nil, fmt.Errorf("failed to query device path: %w", err)
//	        }
//	        mounts = append(mounts, path)
//	    }
//
//	    // Detect driver version and add version-specific mounts
//	    driverVersion, _ := vendorAPI.GetDriverVersion()
//	    if strings.HasPrefix(driverVersion, "2.") {
//	        // New driver requires additional control device
//	        mounts = append(mounts, "/dev/vendor_v2_ctrl")
//	    } else {
//	        // Legacy driver uses old control device
//	        mounts = append(mounts, "/dev/vendor_ctrl")
//	    }
//
//	    return mounts, nil
//	}
//
// This type of dynamic logic cannot be expressed in static YAML configuration
// and requires a code-based sandbox implementation.
//
// # Migration from Code to Config
//
// If you find your code-based sandbox contains mostly static configuration,
// consider migrating it to ext_sandboxes in devices.yaml:
//
// Before (Code):
//
//	func (s *AscendSandbox) GetDeviceMounts(devices []runtime.DeviceInfo) ([]string, error) {
//	    mounts := []string{"/dev/davinci_manager", "/dev/devmm_svm"}
//	    for _, dev := range devices {
//	        mounts = append(mounts, fmt.Sprintf("/dev/davinci%d", dev.Index))
//	    }
//	    return mounts, nil
//	}
//
// After (Config):
//
//	ext_sandboxes:
//	  vllm:
//	    devices:
//	      - /dev/davinci0  # Auto-matched by index
//	      - /dev/davinci1
//	      - /dev/davinci_manager  # Always mounted
//	      - /dev/devmm_svm
//
// The configuration-driven approach reduces code maintenance and allows
// users to adapt to new hardware without waiting for releases.
package vllmdocker

