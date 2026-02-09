// Package mlguiderdocker implements MLGuider runtime with Docker deployment.
//
// # Overview
//
// MLGuider is a high-performance inference engine optimized for large language models
// on Huawei Ascend NPUs. This package provides Docker-based deployment with support for
// multi-device tensor parallelism, expert parallelism (for MoE models), and OpenAI-compatible
// API endpoints.
//
// # Configuration-Driven Architecture
//
// This runtime uses configuration-driven device support via configs/devices.yaml.
// All device-specific behavior is defined in YAML configuration.
//
// Example configuration in devices.yaml:
//
//	chip_models:
//	  - config_key: ascend-310p
//	    ext_sandboxes:
//	      mlguider:
//	        device_env: MINDIE_NPU_DEVICE_IDS
//	        devices:
//	          - /dev/davinci0
//	          - /dev/davinci1
//	          - /dev/davinci_manager
//	          - /dev/devmm_svm
//	          - /dev/hisi_hdc
//	        volumes:
//	          - /usr/local/Ascend/driver:/usr/local/Ascend/driver
//	          - /usr/local/Ascend/add-ons:/usr/local/Ascend/add-ons
//	          - /var/log/npu/slog:/var/log/npu/slog
//	          - /var/log/npu/profiling:/var/log/npu/profiling
//	        privileged: true
//	        shm_size_gb: 100
//
// # Implementing Code-Based Sandboxes
//
// For complex scenarios requiring runtime logic, you can implement a code-based
// DeviceSandbox. See package vllmdocker documentation for detailed guide:
//
//	import "github.com/tsingmaoai/xw-cli/internal/runtime/vllm-docker"
//	// See vllmdocker package doc for complete implementation examples
//
// # MLGuider-Specific Features
//
// MLGuider has unique characteristics:
//
//  1. Model Path Management:
//     - ORIGIN_MODEL_PATH: Original model files (HuggingFace format)
//     - MODEL_PATH: Converted/optimized model files (MLGuider format)
//     - Supports automatic model conversion on first run
//
//  2. Parallelism Support:
//     - TENSOR_PARALLEL: Tensor parallelism across devices
//     - EXPERT_PARALLEL: Expert parallelism for MoE models
//     - WORLD_SIZE: Must equal TENSOR_PARALLEL * EXPERT_PARALLEL
//
//  3. Device Array Format:
//     Uses JSON array format for DEVICES environment variable:
//     DEVICES='[0,1,2,3]'
//
//  4. Networking:
//     Uses host networking (--net=host) for optimal performance
//
// # Dual Model Directory Support
//
// MLGuider supports two model storage patterns:
//
//  1. Single Directory (Original Models Only):
//     - Mount original model to /mnt/model
//     - MLGuider converts on first run
//     - Converted models stored in same directory
//
//  2. Separate Directories (Original + Converted):
//     - Mount original model to /mnt/model (ORIGIN_MODEL_PATH)
//     - Mount data directory to /data (MODEL_PATH for converted models)
//     - Faster startup when using pre-converted models
//
// Example with separate directories:
//
//	params := &runtime.CreateParams{
//	    ModelPath: "/models/llama-3-70b",        // Original model
//	    DataDir: "/data/converted/llama-3-70b", // Converted model cache
//	    // ...
//	}
//
// The converted models in DataDir can be reused across container restarts,
// avoiding repeated conversion overhead.
//
// # Configuration vs Code
//
// This package previously used code-based AscendSandbox. Migration to
// configuration-driven approach provides:
//
//  - Easier updates when Ascend driver paths change
//  - Support for new 310P variants without code changes
//  - User customization of log directories and mount paths
//  - Faster deployment cycles without binary recompilation
//
// See configs/devices.yaml for current device configurations.
package mlguiderdocker

