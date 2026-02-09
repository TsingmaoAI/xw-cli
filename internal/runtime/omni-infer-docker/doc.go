// Package omniinferdocker implements Omni-Infer runtime with Docker deployment.
//
// # Overview
//
// This package provides a Docker-based runtime for running Omni-Infer inference engine
// on Huawei Ascend NPUs. Omni-Infer is optimized for large language model inference
// with a simple API interface and high performance.
//
// # Configuration-Driven Architecture
//
// This runtime uses configuration-driven device support via configs/devices.yaml.
// All device-specific behavior is defined in YAML configuration.
//
// Example configuration in devices.yaml:
//
//	chip_models:
//	  - config_key: ascend-910b
//	    runtime_images:
//	      omni-infer:
//	        arm64: omniinfer-xw:latest
//	    ext_sandboxes:
//	      omni-infer:
//	        device_env: ASCEND_RT_VISIBLE_DEVICES
//	        devices:
//	          - /dev/davinci0
//	          - /dev/davinci1
//	          - /dev/davinci_manager
//	          - /dev/devmm_svm
//	          - /dev/hisi_hdc
//	        volumes:
//	          - /usr/local/Ascend/driver:/usr/local/Ascend/driver:ro
//	          - /usr/local/dcmi:/usr/local/dcmi:ro
//	        privileged: true
//	        shm_size_gb: 500
//
// # Omni-Infer-Specific Features
//
// Omni-Infer has the following characteristics:
//
//  1. Host Networking:
//     Uses --net=host for optimal performance
//     Port configuration via SERVER_PORT environment variable
//
//  2. Environment Variables:
//     - MODEL_PATH: Container-internal path to model files (/mnt/model)
//     - MODEL_NAME: Model name for API requests
//     - TENSOR_PARALLEL_SIZE: Number of devices for tensor parallelism
//     - MAX_MODEL_LEN: Maximum sequence length
//     - SERVER_PORT: HTTP server port (default: 8000)
//     - ASCEND_RT_VISIBLE_DEVICES: Set automatically based on allocated devices
//
//  3. Large Shared Memory:
//     Default 500GB shared memory (--shm-size=500g)
//     Required for large model inference workloads
//
//  4. Minimal Volume Mounts:
//     Only essential mounts: Ascend driver and DCMI (both read-only)
//     Keeps container lightweight and secure
//
// # Usage Example
//
// Creating an Omni-Infer instance:
//
//	params := &runtime.CreateParams{
//	    InstanceID: "qwen-7b-01",
//	    ModelID: "qwen2.5-7b-instruct",
//	    ModelPath: "/mnt/models/Qwen2.5-7B-Instruct",
//	    BackendType: "omni-infer",
//	    Port: 8000,
//	    Devices: []runtime.DeviceInfo{
//	        {ConfigKey: "ascend-910b", Index: 6},
//	        {ConfigKey: "ascend-910b", Index: 7},
//	        {ConfigKey: "ascend-910b", Index: 8},
//	        {ConfigKey: "ascend-910b", Index: 9},
//	    },
//	    TensorParallel: 4,
//	    ExtraConfig: map[string]interface{}{
//	        "max_model_len": 32768,
//	    },
//	}
//
//	instance, err := runtime.Create(ctx, params)
//
// # Comparison with Other Runtimes
//
// | Feature | vLLM | MindIE | MLGuider | Omni-Infer |
// |---------|------|--------|----------|------------|
// | Networking | Port mapping | Port mapping | Host | Host |
// | Shared Memory | 100GB | 100GB | 100GB | 500GB |
// | Log Mounts | Minimal | Extensive | Extensive | Minimal |
// | Complexity | Medium | High | High | Low |
//
// Omni-Infer is the simplest runtime with minimal dependencies and
// straightforward configuration.
//
// # Implementing Code-Based Sandboxes
//
// For complex scenarios requiring runtime logic, see package vllmdocker
// documentation for detailed implementation guide:
//
//	import "github.com/tsingmaoai/xw-cli/internal/runtime/vllm-docker"
//	// See vllmdocker package doc for complete examples
//
// # Configuration Updates
//
// To update Omni-Infer configuration without recompiling:
//
//  1. Edit configs/devices.yaml ext_sandboxes section
//  2. Adjust environment variables, device mounts, or volumes
//  3. Restart xw service to reload configuration
//  4. No binary recompilation needed
//
// Example: Changing shared memory size:
//
//	ext_sandboxes:
//	  omni-infer:
//	    shm_size_gb: 600  # Increase from 500GB to 600GB
//
// This flexibility allows rapid iteration on device support and
// configuration tuning based on workload requirements.
package omniinferdocker

