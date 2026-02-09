// Package config provides configuration management for the xw CLI tool.
//
// This file defines extended sandbox configuration structures, which allow
// users to define device sandbox configurations for additional chip types
// without modifying the source code. This is particularly useful for supporting
// niche accelerator chips in a flexible and maintainable way.
//
// Extended sandbox configurations are defined within devices.yaml under each
// chip model's ext_sandboxes field.
package config

// ExtSandboxConfig defines the configuration for an extended device sandbox.
//
// This configuration maps directly to the DeviceSandbox interface methods,
// providing a declarative way to define device-specific container requirements
// for AI accelerators.
//
// The configuration is designed to support common device patterns while
// maintaining simplicity. Complex scenarios (e.g., custom device initialization,
// dynamic configuration) should be implemented in code-based sandboxes.
//
// Example YAML configuration (in devices.yaml):
//
//	chip_models:
//	  - config_key: kunlun-r200
//	    model_name: Kunlun XPU R200
//	    device_id: "0x1234"
//	    ext_sandboxes:
//	      vllm:
//	        device_env: XPU_VISIBLE_DEVICES
//	        environment:
//	          XPU_WORKER_MULTIPROC_METHOD: spawn
//	        devices:
//	          - /dev/xpu0
//	          - /dev/xpu1
//	          - /dev/xpu_ctl
//	        volumes:
//	          - /usr/local/xpu:/usr/local/xpu
//	        privileged: true
//	        runtime: runc
//	        shm_size_gb: 50
//	        capabilities:
//	          - SYS_ADMIN
type ExtSandboxConfig struct {
	// DeviceEnv specifies the environment variable name for device visibility.
	// This variable will be automatically set to a comma-separated list of
	// device indices (e.g., "0,1,2") when the container is created.
	//
	// Common examples:
	//   - Ascend NPU: ASCEND_RT_VISIBLE_DEVICES
	//   - NVIDIA GPU: CUDA_VISIBLE_DEVICES
	//   - Kunlun XPU: XPU_VISIBLE_DEVICES
	//   - Cambricon MLU: MLU_VISIBLE_DEVICES
	DeviceEnv string `yaml:"device_env"`

	// Environment contains additional static environment variables to set
	// in the container. These are set as-is without any template processing.
	//
	// Example:
	//   environment:
	//     LOG_LEVEL: "3"
	//     WORKER_METHOD: spawn
	Environment map[string]string `yaml:"environment"`

	// Devices lists all device node paths that may need to be mounted.
	//
	// Device mounting behavior:
	//   - Paths ending with a digit (e.g., /dev/xpu0, /dev/xpu5):
	//     Mounted only when the corresponding device index is allocated.
	//   - Paths not ending with a digit (e.g., /dev/xpu_ctl):
	//     Mounted as shared devices for all containers.
	//
	// Example:
	//   devices:
	//     - /dev/xpu0          # Mounted only when device 0 is allocated
	//     - /dev/xpu1          # Mounted only when device 1 is allocated
	//     - /dev/xpu_ctl       # Always mounted (shared control device)
	Devices []string `yaml:"devices"`

	// Volumes defines host-to-container volume mounts in "host:container" format.
	// If no container path is specified (no colon), the same path is used for both.
	//
	// Example:
	//   volumes:
	//     - /usr/local/xpu:/usr/local/xpu
	//     - /root/.cache:/root/.cache
	Volumes []string `yaml:"volumes"`

	// Privileged indicates whether the container requires privileged mode.
	//
	// Privileged mode grants the container extended permissions, including
	// access to all devices and the ability to modify system settings.
	// While less secure, some accelerators require it for proper operation.
	Privileged bool `yaml:"privileged"`

	// Runtime specifies the Docker runtime to use (e.g., "runc", "nvidia").
	// Defaults to "runc" if not specified.
	Runtime string `yaml:"runtime"`

	// ShmSizeGB specifies the shared memory size in gigabytes.
	// If not specified or zero, defaults to 16GB.
	//
	// Shared memory is required for:
	//   - Inter-process communication in distributed inference
	//   - PyTorch DataLoader workers
	//   - Model tensor sharing
	ShmSizeGB int `yaml:"shm_size_gb,omitempty"`

	// Capabilities lists Linux capabilities required by the container.
	//
	// Common capabilities for AI accelerators:
	//   - SYS_ADMIN: System administration operations
	//   - SYS_RAWIO: Direct device I/O access
	//   - IPC_LOCK: Memory locking for device buffers
	//   - SYS_RESOURCE: Resource limit adjustments
	Capabilities []string `yaml:"capabilities"`
}

// LoadExtSandboxesFromDevices extracts extended sandbox configurations from device config.
//
// This function scans all loaded device configurations and collects extended sandbox
// definitions for the specified engine. It returns a map where keys are device
// config_keys and values are the corresponding sandbox configurations.
//
// The function expects device configuration to already be loaded via LoadDevicesConfig()
// or LoadDevicesConfigFrom().
//
// Parameters:
//   - engineName: Engine name to extract configs for ("vllm", "mindie", "mlguider")
//
// Returns:
//   - Map of device config_key -> ExtSandboxConfig for the specified engine
//   - Empty map if device config is not loaded or no extended sandboxes are defined
//
// Example usage:
//
//	// After loading device config
//	extConfigs := config.LoadExtSandboxesFromDevices("vllm")
//	for deviceType, cfg := range extConfigs {
//	    sandbox := runtime.NewExtSandbox(deviceType, cfg)
//	    // Register sandbox...
//	}
func LoadExtSandboxesFromDevices(engineName string) map[string]*ExtSandboxConfig {
	globalConfig, err := GetDevicesConfig()
	if err != nil || globalConfig == nil {
		return make(map[string]*ExtSandboxConfig)
	}

	result := make(map[string]*ExtSandboxConfig)

	// Scan all vendors and their chip models
	for _, vendor := range globalConfig.Vendors {
		for _, chipModel := range vendor.ChipModels {
			// Check if this chip model has extended sandbox configurations
			if chipModel.ExtSandboxes == nil {
				continue
			}

			// Check if there's a configuration for the specified engine
			if sandboxConfig, ok := chipModel.ExtSandboxes[engineName]; ok {
				result[chipModel.ConfigKey] = sandboxConfig
			}
		}
	}

	return result
}
