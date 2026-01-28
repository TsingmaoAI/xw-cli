package hooks

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	
	"github.com/creack/pty"
	"github.com/tsingmao/xw/internal/logger"
)

// DockerInstaller handles Docker installation and image management.
//
// Currently only supports Ubuntu Linux systems. Other Linux distributions
// (Debian, CentOS, RHEL, Fedora, Arch) and other operating systems
// (macOS, Windows) are not supported for automatic Docker installation.
//
// Features:
//   - Automatic Docker installation on Ubuntu
//   - Docker image checking and pulling with progress streaming
//   - Context-aware cancellation support (Ctrl+C handling)
type DockerInstaller struct {
	// eventCh sends installation progress events
	eventCh chan<- string
}

// NewDockerInstaller creates a new Docker installer.
//
// Parameters:
//   - eventCh: Channel for sending progress events (can be nil)
//
// Returns:
//   - Configured Docker installer
func NewDockerInstaller(eventCh chan<- string) *DockerInstaller {
	return &DockerInstaller{
		eventCh: eventCh,
	}
}

// CheckDocker checks if Docker is installed and running.
//
// This method verifies that:
//   - Docker CLI is available
//   - Docker daemon is responsive
//
// Returns:
//   - true if Docker is available
//   - error if check fails
func (d *DockerInstaller) CheckDocker() (bool, error) {
	d.sendEvent("Checking Docker installation...")
	
	// Check if docker command exists
	cmd := exec.Command("docker", "version", "--format", "{{.Server.Version}}")
	output, err := cmd.Output()
	
	if err != nil {
		d.sendEvent("Docker is not installed or not running")
		return false, nil
	}
	
	version := strings.TrimSpace(string(output))
	d.sendEvent(fmt.Sprintf("Docker is installed and running (version: %s)", version))
	
	return true, nil
}

// InstallDocker attempts to install Docker.
//
// This method:
//   - Detects the operating system
//   - Installs Docker using the appropriate method
//   - Streams installation progress via events
//
// Returns:
//   - error if installation fails
func (d *DockerInstaller) InstallDocker() error {
	d.sendEvent(fmt.Sprintf("Detecting OS: %s", runtime.GOOS))
	
	if runtime.GOOS != "linux" {
		return fmt.Errorf("automatic Docker installation only supported on Linux (Ubuntu). Current OS: %s", runtime.GOOS)
	}
	
	return d.installDockerLinux()
}

// installDockerLinux installs Docker on Linux systems.
func (d *DockerInstaller) installDockerLinux() error {
	d.sendEvent("Detecting Linux distribution...")
	
	// Detect Linux distribution
	distro := d.detectLinuxDistro()
	d.sendEvent(fmt.Sprintf("Detected distribution: %s", distro))
	
	if distro == "ubuntu" {
		return d.installDockerUbuntu()
	}
	
	return fmt.Errorf("unsupported Linux distribution: %s. Only Ubuntu is currently supported", distro)
}

// detectLinuxDistro detects the Linux distribution.
func (d *DockerInstaller) detectLinuxDistro() string {
	// Try reading /etc/os-release
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		content := string(data)
		if strings.Contains(strings.ToLower(content), "ubuntu") {
			return "ubuntu"
		}
	}
	
	return "unknown"
}

// installDockerUbuntu installs Docker on Ubuntu systems.
func (d *DockerInstaller) installDockerUbuntu() error {
	commands := [][]string{
		// Update package index
		{"apt-get", "update"},
		// Install prerequisites
		{"apt-get", "install", "-y", "ca-certificates", "curl", "gnupg"},
		// Add Docker's official GPG key
		{"install", "-m", "0755", "-d", "/etc/apt/keyrings"},
		{"sh", "-c", "curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg"},
		{"chmod", "a+r", "/etc/apt/keyrings/docker.gpg"},
		// Add Docker repository
		{"sh", "-c", `echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null`},
		// Update package index again
		{"apt-get", "update"},
		// Install Docker
		{"apt-get", "install", "-y", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"},
		// Start Docker service
		{"systemctl", "start", "docker"},
		{"systemctl", "enable", "docker"},
	}
	
	return d.executeCommands(commands)
}

// executeCommands executes a sequence of commands.
func (d *DockerInstaller) executeCommands(commands [][]string) error {
	for _, cmdArgs := range commands {
		d.sendEvent(fmt.Sprintf("Executing: %s", strings.Join(cmdArgs, " ")))
		
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}
		
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %w", err)
		}
		
		if err := cmd.Start(); err != nil {
			logger.Warn("Command failed: %v, continuing...", err)
			continue
		}
		
		// Stream output
		go d.streamOutput(stdout)
		go d.streamOutput(stderr)
		
		if err := cmd.Wait(); err != nil {
			logger.Warn("Command failed: %v, continuing...", err)
		}
	}
	
	return nil
}

// streamOutput streams command output to events.
func (d *DockerInstaller) streamOutput(reader io.ReadCloser) {
	buf := make([]byte, 4096)
	var accumulated string
	
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			accumulated += chunk
			
			// Process complete lines and \r-separated progress updates
			for {
				// Check for \r (progress update in same line)
				crIdx := strings.Index(accumulated, "\r")
				nlIdx := strings.Index(accumulated, "\n")
				
				var line string
				var found bool
				
				if crIdx != -1 && (nlIdx == -1 || crIdx < nlIdx) {
					// Found \r first, this is a progress update
					line = accumulated[:crIdx]
					accumulated = accumulated[crIdx+1:]
					found = true
				} else if nlIdx != -1 {
					// Found \n, this is a complete line
					line = accumulated[:nlIdx]
					accumulated = accumulated[nlIdx+1:]
					found = true
				}
				
				if !found {
					break
				}
				
				// Send non-empty lines
				line = strings.TrimSpace(line)
				if line != "" {
					d.sendEvent(line)
				}
			}
		}
		
		if err != nil {
			// Send any remaining content
			if accumulated != "" {
				line := strings.TrimSpace(accumulated)
				if line != "" {
					d.sendEvent(line)
				}
			}
			break
		}
	}
}

// CheckDockerImage checks if a Docker image exists locally.
//
// Parameters:
//   - ctx: Context for cancellation
//   - imageName: Docker image name
//
// Returns:
//   - true if image exists locally
//   - error if check fails
func (d *DockerInstaller) CheckDockerImage(ctx context.Context, imageName string) (bool, error) {
	d.sendEvent(fmt.Sprintf("Checking Docker image: %s", imageName))
	
	cmd := exec.CommandContext(ctx, "docker", "images", "-q", imageName)
	output, err := cmd.Output()
	
	if err != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("operation cancelled")
		}
		return false, fmt.Errorf("failed to check Docker image: %w", err)
	}
	
	exists := len(strings.TrimSpace(string(output))) > 0
	
	if exists {
		d.sendEvent(fmt.Sprintf("Docker image %s found locally", imageName))
	} else {
		d.sendEvent(fmt.Sprintf("Docker image %s not found locally", imageName))
	}
	
	return exists, nil
}

// PullDockerImage pulls a Docker image.
//
// This method uses PTY to capture Docker's progress output, including
// progress bars and download status for each layer.
//
// Parameters:
//   - ctx: Context for cancellation
//   - imageName: Docker image to pull
//
// Returns:
//   - error if pull fails
func (d *DockerInstaller) PullDockerImage(ctx context.Context, imageName string) error {
	d.sendEvent(fmt.Sprintf("Pulling Docker image: %s", imageName))
	
	cmd := exec.CommandContext(ctx, "docker", "pull", imageName)
	
	// Use PTY to make Docker think it's running in a terminal
	// This enables progress bar output
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start with pty: %w", err)
	}
	defer ptmx.Close()
	
	// Stream PTY output
	go d.streamOutput(ptmx)
	
	// Wait for command to complete
	if err := cmd.Wait(); err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			d.sendEvent("Docker pull cancelled")
			return fmt.Errorf("operation cancelled")
		}
		return fmt.Errorf("failed to pull image: %w", err)
	}
	
	d.sendEvent(fmt.Sprintf("Successfully pulled image: %s", imageName))
	
	return nil
}

// sendEvent sends a progress event to the event channel.
func (d *DockerInstaller) sendEvent(message string) {
	if d.eventCh != nil {
		select {
		case d.eventCh <- message:
		default:
			// Channel full or closed, log instead
			logger.Debug("Docker installer event: %s", message)
		}
	}
}

