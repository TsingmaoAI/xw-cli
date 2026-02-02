package hooks

import (
	"context"
	"fmt"
)

// DockerHook implements hooks.Hook for Docker availability checking and installation.
//
// This hook ensures Docker is installed and running before attempting to
// create Docker-based model instances. If Docker is not available, it
// attempts automatic installation (Ubuntu only).
type DockerHook struct {
	installer *DockerInstaller
	eventCh   chan<- string
}

// NewDockerHook creates a new Docker hook.
//
// Parameters:
//   - eventCh: Channel for sending progress events
//
// Returns:
//   - Hook instance
func NewDockerHook(eventCh chan<- string) Hook {
	return &DockerHook{
		installer: NewDockerInstaller(eventCh),
		eventCh:   eventCh,
	}
}

// Name returns the hook identifier.
//
// Returns:
//   - Hook name
func (h *DockerHook) Name() string {
	return "docker"
}

// Check verifies if Docker is installed and running.
//
// This method checks:
//   - Docker CLI is available
//   - Docker daemon is responsive
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error if Docker is not available
func (h *DockerHook) Check(ctx context.Context) error {
	dockerOK, err := h.installer.CheckDocker()
	if err != nil {
		return err
	}
	
	if !dockerOK {
		return fmt.Errorf("Docker is not installed or not running")
	}
	
	return nil
}

// Install attempts to install Docker.
//
// This method:
//   - Detects the operating system
//   - Installs Docker if on Ubuntu
//   - Returns error if OS is not supported
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error if installation fails
func (h *DockerHook) Install(ctx context.Context) error {
	return h.installer.InstallDocker()
}

// Message returns a description of what this hook does.
//
// Returns:
//   - Human-readable hook description
func (h *DockerHook) Message() string {
	return "Docker is required for running models in container mode. " +
		"The system will attempt to install Docker automatically (Ubuntu only)."
}

// Interactive indicates whether user confirmation is recommended.
//
// Docker installation can be intrusive (system packages), so we recommend
// user confirmation before proceeding.
//
// Returns:
//   - true (user confirmation recommended)
func (h *DockerHook) Interactive() bool {
	return true
}

// DockerImageHook implements hooks.Hook for Docker image availability checking.
//
// This hook ensures the required Docker image is available locally before
// attempting to create a container. If the image is not present, it pulls
// the image from the registry.
type DockerImageHook struct {
	installer *DockerInstaller
	imageName string
	eventCh   chan<- string
}

// NewDockerImageHook creates a new Docker image hook.
//
// Parameters:
//   - imageName: Docker image to check/pull
//   - eventCh: Channel for sending progress events
//
// Returns:
//   - Hook instance
func NewDockerImageHook(imageName string, eventCh chan<- string) Hook {
	return &DockerImageHook{
		installer: NewDockerInstaller(eventCh),
		imageName: imageName,
		eventCh:   eventCh,
	}
}

// Name returns the hook identifier.
//
// Returns:
//   - Hook name including image name
func (h *DockerImageHook) Name() string {
	return fmt.Sprintf("docker-image:%s", h.imageName)
}

// Check verifies if the Docker image exists locally.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error if image is not available locally
func (h *DockerImageHook) Check(ctx context.Context) error {
	exists, err := h.installer.CheckDockerImage(ctx, h.imageName)
	if err != nil {
		return err
	}
	
	if !exists {
		return fmt.Errorf("Docker image %s not found", h.imageName)
	}
	
	return nil
}

// Install pulls the Docker image from the registry.
//
// This method uses PTY to capture Docker's native progress output,
// including progress bars for each layer being downloaded.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - error if pull fails
func (h *DockerImageHook) Install(ctx context.Context) error {
	return h.installer.PullDockerImage(ctx, h.imageName)
}

// Message returns a description of what this hook does.
//
// Returns:
//   - Human-readable hook description
func (h *DockerImageHook) Message() string {
	return fmt.Sprintf("Docker image '%s' will be pulled from the registry.", h.imageName)
}

// Interactive indicates whether user confirmation is recommended.
//
// Image pulling is usually automatic and expected, so no confirmation needed.
//
// Returns:
//   - false (no user confirmation needed)
func (h *DockerImageHook) Interactive() bool {
	return false
}

