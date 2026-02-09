// Package mindiedocker implements MindIE runtime with Docker deployment.
//
// # Overview
//
// This package provides a Docker-based runtime for running MindIE inference engine
// on Huawei Ascend NPUs. MindIE is optimized for large language models with support
// for multi-device tensor parallelism and pipeline parallelism.
//
// # Configuration-Driven Architecture
//
// This runtime uses configuration-driven device support via configs/devices.yaml.
// All device-specific behavior (environment variables, device mounts, volumes)
// is defined in YAML configuration rather than code.
//
// Example configuration in devices.yaml:
//
//	chip_models:
//	  - config_key: ascend-910b
//	    ext_sandboxes:
//	      mindie:
//	        device_env: MINDIE_NPU_DEVICE_IDS
//	        devices:
//	          - /dev/davinci0
//	          - /dev/davinci1
//	          - /dev/davinci_manager
//	          - /dev/devmm_svm
//	          - /dev/hisi_hdc
//	        volumes:
//	          - /usr/local/Ascend/driver:/usr/local/Ascend/driver
//	          - /var/log/npu/slog:/var/log/npu/slog
//	          - /var/log/npu/profiling:/var/log/npu/profiling
//	        privileged: true
//	        shm_size_gb: 100
//
// # Implementing Code-Based Sandboxes
//
// For complex scenarios that cannot be expressed in configuration, you can
// implement a code-based DeviceSandbox. See package vllmdocker documentation
// for detailed instructions and examples:
//
//	import "github.com/tsingmaoai/xw-cli/internal/runtime/vllm-docker"
//	// See vllmdocker package doc for implementation guide
//
// Key scenarios requiring code-based sandboxes:
//  - Dynamic device path detection at runtime
//  - Complex conditional logic based on driver versions
//  - Integration with vendor-specific device management APIs
//  - Custom device allocation algorithms
//
// # MindIE-Specific Configuration
//
// MindIE has unique requirements compared to other runtimes:
//
//  1. Device Visibility:
//     Uses MINDIE_NPU_DEVICE_IDS instead of ASCEND_RT_VISIBLE_DEVICES
//
//  2. Log Directories:
//     Requires extensive log directory mounts for profiling and debugging:
//     - /var/log/npu/slog: System logs from CANN runtime
//     - /var/log/npu/profiling: Performance profiling data
//     - /var/log/npu/dump: Core dumps and diagnostics
//
//  3. Multi-Device Support:
//     WORLD_SIZE environment variable must match total device count
//     (TENSOR_PARALLEL * PIPELINE_PARALLEL)
//
// # Migration from Code to Configuration
//
// This package previously used code-based AscendSandbox. It has been migrated
// to configuration-driven approach to allow updates without recompiling.
//
// Benefits of configuration-driven approach:
//  - Add new Ascend chip variants without code changes
//  - Adjust log paths when driver updates change locations
//  - Users can customize behavior for their environment
//  - Faster iteration on device support
//
// See configs/devices.yaml for current device configurations.
package mindiedocker

