package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/internal/api"
)

// StartOptions holds options for the start command
type StartOptions struct {
	*GlobalOptions
	
	// Model is the model name to start
	Model string
	
	// Alias is the instance alias for inference (defaults to model name)
	Alias string
	
	// Engine is the inference engine in format "backend:mode" (e.g., "vllm:docker", "mindie:native")
	Engine string

	// Device is the device list (e.g., "0", "0,1,2,3")
	Device string
	
	// TensorParallel is the tensor parallelism degree (must be 1/2/4/8)
	TensorParallel int

	// MaxConcurrent is the maximum number of concurrent requests (0 for unlimited)
	MaxConcurrent int
	
	// Detach runs the instance in the background (default: false, run in foreground with logs)
	Detach bool
}

// NewStartCommand creates the start command.
//
// The start command starts a model instance for inference.
//
// Usage:
//
//	xw start MODEL [OPTIONS]
//
// Examples:
//
//	# Start a model with auto-selected backend
//	xw start qwen2-0.5b
//
//	# Start with specific engine (backend:mode)
//	xw start qwen2-7b --engine vllm:docker
//
//	# Start with custom alias
//	xw start qwen2-7b --alias my-model
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for starting models
func NewStartCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &StartOptions{
		GlobalOptions: globalOpts,
	}
	
	cmd := &cobra.Command{
		Use:   "start MODEL",
		Short: "Start a model instance",
		Long: `Start a model instance for inference.

The start command manages the lifecycle of model instances, supporting both Docker
and native deployment modes. Starting the same model multiple times will return 
the existing instance rather than creating duplicates.

Engine Selection:
  Engine is specified as "backend:mode" (e.g., "vllm:docker", "mindie:native").
  If not specified, xw will automatically select the best available engine
  based on the model's preferences and your system configuration.
  
  Available engines:
    - vllm:docker   - vLLM in Docker container (recommended)
    - vllm:native   - vLLM native installation
    - mindie:docker - MindIE in Docker container
    - mindie:native - MindIE native installation

Device Selection:
  Specify which AI accelerator devices to use (e.g., --device 0 or --device 0,1,2,3)
  If not specified, the system will automatically allocate available devices.

Concurrency Control:
  Use --max-concurrent to limit concurrent inference requests per instance.
  Default: 0 (unlimited). Useful for controlling load on the inference service.

Foreground vs Background:
  By default, the instance runs in foreground mode with log streaming.
  Press Ctrl+C to stop and remove the instance.
  Use -d/--detach to run in background mode (keeps running after command exits).

Examples:
  # Start in foreground (default) - shows logs, Ctrl+C to stop
  xw start qwen2-7b

  # Start in background (detached)
  xw start qwen2-7b -d

  # Start with specific engine in foreground
  xw start qwen2-7b --engine vllm:docker

  # Start on specific devices with concurrency limit
  xw start qwen2-72b --device 0,1,2,3 --max-concurrent 4`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Model = args[0]
			return runStart(opts)
		},
	}
	
	cmd.Flags().StringVar(&opts.Alias, "alias", "", 
		"instance alias for inference (defaults to model name)")
	cmd.Flags().StringVar(&opts.Engine, "engine", "", 
		"inference engine in format backend:mode (e.g., vllm:docker, mindie:native)")
	cmd.Flags().StringVar(&opts.Device, "device", "", 
		"device list (e.g., 0 or 0,1,2,3)")
	cmd.Flags().IntVar(&opts.TensorParallel, "tp", 0, 
		"tensor parallelism degree (must be 1, 2, 4, or 8)")
	cmd.Flags().IntVar(&opts.MaxConcurrent, "max-concurrent", 0, 
		"maximum concurrent requests (0 for unlimited)")
	cmd.Flags().BoolVarP(&opts.Detach, "detach", "d", false,
		"run instance in the background (default: run in foreground with logs)")
	
	return cmd
}

// runStart executes the start command logic
func runStart(opts *StartOptions) error {
	client := getClient(opts.GlobalOptions)

	// Parse engine string (format: "backend:mode")
	// Only basic format check, real validation happens on server side
	var backendType api.BackendType
	var deploymentMode api.DeploymentMode
	
	if opts.Engine != "" {
		parts := strings.Split(opts.Engine, ":")
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Error: Invalid engine format: %s\n", opts.Engine)
			fmt.Fprintf(os.Stderr, "Expected format: backend:mode (e.g., vllm:docker, mindie:native)\n")
			os.Exit(1)
		}
		backendType = api.BackendType(parts[0])
		deploymentMode = api.DeploymentMode(parts[1])
	}

	// Prepare additional config for device and concurrency
	additionalConfig := make(map[string]interface{})
	if opts.Device != "" {
		additionalConfig["device"] = opts.Device
	}
	if opts.TensorParallel > 0 {
		additionalConfig["tensor_parallel"] = opts.TensorParallel
	}
	if opts.MaxConcurrent > 0 {
		additionalConfig["max_concurrent"] = opts.MaxConcurrent
	}

	// Prepare run options as a map matching server's expected JSON structure
	runOpts := map[string]interface{}{
		"model_id":          opts.Model,
		"alias":             opts.Alias,
		"backend_type":      string(backendType),
		"deployment_mode":   string(deploymentMode),
		"interactive":       false,
		"additional_config": additionalConfig,
	}

	// Display startup message
	engineStr := string(backendType)
	if engineStr == "" {
		engineStr = "auto"
	}
	modeStr := string(deploymentMode)
	if modeStr == "" {
		modeStr = "auto"
	}
	fmt.Printf("Starting %s with %s engine (%s mode)...\n", opts.Model, engineStr, modeStr)
	if opts.Device != "" {
		fmt.Printf("Devices: %s\n", opts.Device)
	}
	if opts.MaxConcurrent > 0 {
		fmt.Printf("Max Concurrent Requests: %d\n", opts.MaxConcurrent)
	}
	fmt.Println()

	// Setup context and signal handler for Ctrl+C during startup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	// Handle Ctrl+C in background
	go func() {
		<-sigChan
		fmt.Println("\n\nReceived interrupt signal. Cancelling operation...")
		cancel()
	}()

	// Start the model instance via server API with SSE streaming
	progressDisplay := newProgressDisplay()
	instanceInfo, err := client.RunModelWithSSEContext(ctx, runOpts, func(event string) {
		progressDisplay.update(event)
	})
	progressDisplay.finish()
	
	// Stop signal handler
	signal.Stop(sigChan)
	close(sigChan)
	
	if err != nil {
		// Print error directly without "Error: " prefix
		fmt.Println()
		fmt.Println(err.Error())
		os.Exit(1)
	}
	
	// Get instance alias from response
	var instanceAlias string
	if instanceInfo != nil {
		if alias, ok := instanceInfo["alias"].(string); ok && alias != "" {
			instanceAlias = alias
		} else if instanceID, ok := instanceInfo["instance_id"].(string); ok {
			instanceAlias = instanceID
		}
	}
	if instanceAlias == "" {
		instanceAlias = opts.Alias
		if instanceAlias == "" {
			instanceAlias = opts.Model
		}
	}
	
	// Success
	fmt.Println()
	fmt.Println("✓ Model started successfully")
	fmt.Println()
	
	// If detach mode, just show info and return
	if opts.Detach {
		fmt.Println("Use 'xw ps' to view running instances")
		return nil
	}
	
	// Foreground mode: stream logs and handle Ctrl+C
	fmt.Printf("Streaming logs from %s (press Ctrl+C to stop and remove)...\n", instanceAlias)
	fmt.Println()
	
	// Setup signal handler for Ctrl+C during log streaming
	logSigChan := make(chan os.Signal, 1)
	signal.Notify(logSigChan, os.Interrupt, syscall.SIGTERM)
	
	// Start log streaming in a goroutine
	logDone := make(chan error, 1)
	go func() {
		err := client.StreamInstanceLogs(instanceAlias, true, func(logLine string) {
			fmt.Print(logLine)
			// Force flush stdout for real-time output
			os.Stdout.Sync()
		})
		logDone <- err
	}()
	
	// Wait for signal or log stream to end
	select {
	case <-logSigChan:
		fmt.Println()
		fmt.Printf("\nReceived interrupt signal. Removing %s...\n", instanceAlias)
		
		// Remove the instance directly (no need to stop first)
		if err := client.RemoveInstanceByAlias(instanceAlias, true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove instance: %v\n", err)
		} else {
			fmt.Printf("Removed instance: %s\n", instanceAlias)
		}
		
	case err := <-logDone:
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nLog stream ended with error: %v\n", err)
		} else {
			fmt.Println("\nLog stream ended")
		}
		
		// Auto cleanup when log stream ends - remove directly
		fmt.Printf("Cleaning up %s...\n", instanceAlias)
		if err := client.RemoveInstanceByAlias(instanceAlias, true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove instance: %v\n", err)
		} else {
			fmt.Printf("Removed instance: %s\n", instanceAlias)
		}
	}
	
	return nil
}


// progressDisplay handles progress display
type progressDisplay struct {
	isPulling       bool
	progressShown   bool
	dockerFirstLine bool              // Track if this is the first Docker output line
	layers          map[string]string // Layer ID -> current status line
	lastLineCount   int               // Number of lines last rendered
}

// newProgressDisplay creates a new progress display
func newProgressDisplay() *progressDisplay {
	return &progressDisplay{
		layers: make(map[string]string),
	}
}

// update processes and displays an event
func (pd *progressDisplay) update(event string) {
	// Handle Docker output with \r (carriage return - layer update)
	if strings.HasPrefix(event, "DOCKER_CR|") {
		pd.isPulling = true
		line := strings.TrimPrefix(event, "DOCKER_CR|")
		pd.updateLayer(line)
		pd.renderLayers()
		return
	}
	
	// Handle Docker output with \n (line feed - new line)
	if strings.HasPrefix(event, "DOCKER_LF|") {
		pd.isPulling = true
		line := strings.TrimPrefix(event, "DOCKER_LF|")
		pd.dockerFirstLine = false
		
		// Check if it's a layer status line
		if strings.Contains(line, ":") && pd.isLayerLine(line) {
			pd.updateLayer(line)
			pd.renderLayers()
		} else if !pd.shouldSkipLine(line) {
			// Non-layer line that we want to show
			fmt.Println(line)
		}
		os.Stdout.Sync()
		return
	}
	
	// Detect start of Docker pull
	if strings.Contains(event, "Pulling Docker image:") {
		pd.isPulling = true
		pd.progressShown = false
		pd.dockerFirstLine = true
		pd.layers = make(map[string]string)
		pd.lastLineCount = 0
		fmt.Printf("\n▸ %s\n", event)
		return
	}
	
	// Detect end of Docker pull
	if strings.Contains(event, "Successfully pulled") ||
	   strings.Contains(event, "Docker pull cancelled") {
		// Clear layer rendering
		if pd.lastLineCount > 0 {
			fmt.Println() // Move past the progress lines
		}
		pd.isPulling = false
		pd.dockerFirstLine = false
		pd.layers = make(map[string]string)
		pd.lastLineCount = 0
		fmt.Printf("▸ %s\n", event)
		return
	}
	
	// Skip other messages during pull
	if pd.isPulling {
		return
	}
	
	// Non-pull events - print with bullet point
	fmt.Printf("▸ %s\n", event)
}

// isLayerLine checks if a line is a layer status line (layerID: status)
func (pd *progressDisplay) isLayerLine(line string) bool {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return false
	}
	layerID := strings.TrimSpace(parts[0])
	// Layer IDs are typically 12-character hex strings
	return len(layerID) >= 8 && len(layerID) <= 12
}

// shouldSkipLine checks if a Docker output line should be skipped
func (pd *progressDisplay) shouldSkipLine(line string) bool {
	line = strings.TrimSpace(line)
	
	// Skip empty lines
	if line == "" {
		return true
	}
	
	// Skip "Pulling from" lines (redundant with our message)
	if strings.Contains(line, "Pulling from") {
		return true
	}
	
	return false
}

// updateLayer parses and updates a layer's status
func (pd *progressDisplay) updateLayer(line string) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) == 2 {
		layerID := strings.TrimSpace(parts[0])
		if len(layerID) >= 8 && len(layerID) <= 12 {
			// Store the full line
			pd.layers[layerID] = line
		}
	}
}

// renderLayers clears previous output and renders all active layers
func (pd *progressDisplay) renderLayers() {
	if len(pd.layers) == 0 {
		return
	}
	
	// Move cursor up to start of progress area
	if pd.lastLineCount > 0 {
		fmt.Printf("\033[%dA", pd.lastLineCount)
	}
	
	// Clear and render each layer
	lines := make([]string, 0, len(pd.layers))
	for _, line := range pd.layers {
		lines = append(lines, line)
	}
	
	// Sort for consistent display
	sort.Strings(lines)
	
	for _, line := range lines {
		// Clear line and print
		fmt.Print("\r\033[K")
		fmt.Println(line)
	}
	
	pd.lastLineCount = len(lines)
	os.Stdout.Sync()
}

// finish completes the display
func (pd *progressDisplay) finish() {
	// Clear progress line if shown
	if pd.progressShown {
		fmt.Print("\r\033[K")
	}
}
