package runtime

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// ContainerStateInfo holds the result of container state inspection.
type ContainerStateInfo struct {
	// State is the mapped instance state
	State InstanceState
	
	// ErrorMessage contains details if state is StateError
	ErrorMessage string
	
	// ExitCode contains the container exit code (only valid for exited containers)
	ExitCode int
	
	// IsRunning indicates if the container is currently running
	IsRunning bool
}

// InspectContainerState inspects a Docker container and maps its state to our instance state model.
//
// State Mapping Rules:
//   - Container running       -> StateRunning
//   - Container created       -> StateCreated
//   - Container exited/failed -> StateError (since xw stop now removes containers)
//   - Inspect failure         -> Returns error
//
// This function encapsulates all container-to-instance state mapping logic in one place,
// ensuring consistency across the codebase.
//
// Parameters:
//   - ctx: Context for cancellation
//   - dockerClient: Docker API client
//   - containerID: Container ID to inspect
//
// Returns:
//   - ContainerStateInfo with state details
//   - Error if inspection fails
func InspectContainerState(ctx context.Context, dockerClient *client.Client, containerID string) (*ContainerStateInfo, error) {
	inspect, err := dockerClient.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}

	return mapContainerState(&inspect), nil
}

// mapContainerState converts Docker container inspect data to our instance state model.
//
// This is the single source of truth for state mapping logic.
func mapContainerState(inspect *types.ContainerJSON) *ContainerStateInfo {
	info := &ContainerStateInfo{
		IsRunning: inspect.State.Running,
		ExitCode:  inspect.State.ExitCode,
	}

	if inspect.State.Running {
		// Container is actively running
		info.State = StateRunning
		return info
	}

	// Container is not running - determine why
	switch {
	case inspect.State.Status == "created":
		// Container created but never started
		info.State = StateCreated

	case inspect.State.Status == "exited" || inspect.State.Status == "dead":
		// Container exited or crashed
		// Since xw stop now removes containers, any exited container is unexpected
		info.State = StateError
		info.ErrorMessage = formatExitError(inspect.State)

	case inspect.State.Restarting:
		// Container is restarting (should not happen with our restart policy)
		info.State = StateError
		info.ErrorMessage = "Container is stuck in restart loop"

	default:
		// Unknown state
		info.State = StateError
		info.ErrorMessage = fmt.Sprintf("Container in unexpected state: %s", inspect.State.Status)
	}

	return info
}

// formatExitError creates a user-friendly error message for exited containers.
func formatExitError(state *container.State) string {
	if state.Error != "" {
		return fmt.Sprintf("Container exited unexpectedly with code %d: %s", 
			state.ExitCode, state.Error)
	}
	return fmt.Sprintf("Container exited unexpectedly with code %d", state.ExitCode)
}

// UpdateInstanceStateFromContainer checks the actual container state and updates the instance.
//
// This function should be called periodically to keep instance state in sync with
// Docker container reality. It only updates instances that are expected to be running
// (starting, running, or ready states).
//
// Parameters:
//   - ctx: Context for cancellation
//   - dockerClient: Docker API client
//   - instance: Instance to update (modified in place)
//
// Returns:
//   - true if state was changed
//   - false if state remained the same or instance doesn't need checking
func UpdateInstanceStateFromContainer(ctx context.Context, dockerClient *client.Client, instance *Instance) bool {
	// Only check containers that should be running
	if instance.State != StateStarting && instance.State != StateRunning && instance.State != StateReady {
		return false
	}

	containerID, ok := instance.Metadata["container_id"]
	if !ok || containerID == "" {
		return false
	}

	// Get current container state
	stateInfo, err := InspectContainerState(ctx, dockerClient, containerID)
	if err != nil {
		logger.Warn("Failed to inspect container %s (instance %s): %v", 
			containerID[:min(len(containerID), 12)], instance.ID, err)
		return false
	}

	// Check if container has unexpectedly stopped
	if !stateInfo.IsRunning && stateInfo.State == StateError {
		oldState := instance.State
		instance.State = stateInfo.State
		instance.Error = stateInfo.ErrorMessage
		
		logger.Warn("Container %s (instance %s) changed from %s to %s: %s",
			containerID[:min(len(containerID), 12)], instance.ID, 
			oldState, stateInfo.State, stateInfo.ErrorMessage)
		
		return true
	}

	return false
}

// min returns the minimum of two integers (Go < 1.21 compatibility)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

