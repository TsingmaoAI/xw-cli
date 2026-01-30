package server

import (
	"fmt"
	
	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/runtime"
	mindiedocker "github.com/tsingmao/xw/internal/runtime/mindie-docker"
	mlguiderdocker "github.com/tsingmao/xw/internal/runtime/mlguider-docker"
	vllmdocker "github.com/tsingmao/xw/internal/runtime/vllm-docker"
	
	// Import model packages to trigger model registration via init()
	_ "github.com/tsingmao/xw/internal/models/qwen"
)

// InitializeRuntimeManager creates and initializes the runtime manager
// with all available runtime implementations.
//
// This function is responsible for:
//   1. Creating the runtime manager
//   2. Discovering and registering available runtimes
//   3. Starting background tasks
//
// Runtime registration failures are logged but don't cause the function
// to fail, allowing the system to operate with whatever runtimes are available.
//
// Returns:
//   - Configured runtime manager
//   - Error only if manager creation fails
func InitializeRuntimeManager() (*runtime.Manager, error) {
	// Create runtime manager
	// Server name will be set later by the caller
	mgr, err := runtime.NewManager("")
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime manager: %w", err)
	}
	
	registeredCount := 0
	
	// Register vLLM Docker runtime
	if rt, err := vllmdocker.NewRuntime(); err != nil {
		logger.Warn("vLLM Docker runtime unavailable: %v", err)
	} else {
		if err := mgr.RegisterRuntime(rt); err != nil {
			logger.Warn("Failed to register vLLM Docker runtime: %v", err)
		} else {
			registeredCount++
			logger.Info("Registered runtime: %s", rt.Name())
		}
	}
	
	// Register MindIE Docker runtime
	if rt, err := mindiedocker.NewRuntime(); err != nil {
		logger.Warn("MindIE Docker runtime unavailable: %v", err)
	} else {
		if err := mgr.RegisterRuntime(rt); err != nil {
			logger.Warn("Failed to register MindIE Docker runtime: %v", err)
		} else {
			registeredCount++
			logger.Info("Registered runtime: %s", rt.Name())
		}
	}
	
	// Register MLGuider Docker runtime
	if rt, err := mlguiderdocker.NewRuntime(); err != nil {
		logger.Warn("MLGuider Docker runtime unavailable: %v", err)
	} else {
		if err := mgr.RegisterRuntime(rt); err != nil {
			logger.Warn("Failed to register MLGuider Docker runtime: %v", err)
		} else {
			registeredCount++
			logger.Info("Registered runtime: %s", rt.Name())
		}
	}
	
	// TODO: Register additional runtimes as implemented (e.g., native deployments)
	
	if registeredCount == 0 {
		logger.Warn("No runtimes available - model execution will not be possible")
	} else {
		logger.Info("Successfully registered %d runtime(s)", registeredCount)
	}
	
	// Start background maintenance tasks
	mgr.StartBackgroundTasks()
	
	return mgr, nil
}

