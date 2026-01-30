package app

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tsingmao/xw/internal/models"
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
	var backendType models.BackendType
	var deploymentMode models.DeploymentMode
	
	if opts.Engine != "" {
		parts := strings.Split(opts.Engine, ":")
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "invalid engine format: %s (expected format: backend:mode, e.g., vllm:docker)\n", opts.Engine)
			os.Exit(1)
		}
		backendType = models.BackendType(parts[0])
		deploymentMode = models.DeploymentMode(parts[1])
	}

	// Prepare additional config for device and concurrency
	additionalConfig := make(map[string]interface{})
	if opts.Device != "" {
		additionalConfig["device"] = opts.Device
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

	// Start the model instance via server API with SSE streaming
	progressDisplay := newProgressDisplay()
	instanceInfo, err := client.RunModelWithSSE(runOpts, func(event string) {
		progressDisplay.update(event)
	})
	progressDisplay.finish()
	
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
	
	// Setup signal handler for Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
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
	case <-sigChan:
		fmt.Println()
		fmt.Printf("\nReceived interrupt signal. Stopping and removing %s...\n", instanceAlias)
		
		// Stop the instance
		if err := client.StopInstanceByAlias(instanceAlias, false); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to stop instance: %v\n", err)
		}
		
		// Remove the instance
		if err := client.RemoveInstanceByAlias(instanceAlias, true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to remove instance: %v\n", err)
		}
		
	case err := <-logDone:
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nLog stream ended with error: %v\n", err)
		} else {
			fmt.Println("\nLog stream ended")
		}
		
		// Auto cleanup when log stream ends
		fmt.Printf("Cleaning up %s...\n", instanceAlias)
		client.StopInstanceByAlias(instanceAlias, false)
		client.RemoveInstanceByAlias(instanceAlias, true)
	}
	
	return nil
}


// progressDisplay handles Docker pull progress display with overwriting
type progressDisplay struct {
	layers        map[string]string // layer ID -> status line
	lastLineCount int               // number of lines in last display
	isPulling     bool              // whether we're in pull mode
}

// newProgressDisplay creates a new progress display
func newProgressDisplay() *progressDisplay {
	return &progressDisplay{
		layers: make(map[string]string),
	}
}

// update processes and displays an event
func (pd *progressDisplay) update(event string) {
	// DEBUG: Print raw event (can be removed later)
	// fmt.Printf("[DEBUG] event: %q\n", event)
	
	// Check if this is Docker pull output
	if strings.Contains(event, "Pulling from") {
		pd.isPulling = true
		fmt.Printf("\n%s\n\n", event)
		return
	}
	
	if strings.Contains(event, "Pulling Docker image:") || 
	   strings.Contains(event, "Successfully pulled") ||
	   strings.Contains(event, "Docker pull cancelled") {
		pd.isPulling = false
		fmt.Printf("\n▸ %s\n", event)
		return
	}
	
	// Non-pull events - just print normally
	if !pd.isPulling {
		fmt.Printf("▸ %s\n", event)
		return
	}
	
	// Parse Docker pull progress line
	// Format: "layer_id: Status [Progress] size"
	parts := strings.SplitN(event, ":", 2)
	if len(parts) != 2 {
		// Not a layer progress line, print normally
		fmt.Printf("%s\n", event)
		return
	}
	
	layerID := strings.TrimSpace(parts[0])
	status := strings.TrimSpace(parts[1])
	
	// Filter out empty status
	if status == "" {
		return
	}
	
	// Update layer status
	pd.layers[layerID] = status
	
	// Clear previous lines
	pd.clearLines()
	
	// Display all layers (sorted for stability)
	pd.lastLineCount = 0
	
	// Get sorted layer IDs
	layerIDs := make([]string, 0, len(pd.layers))
	for id := range pd.layers {
		layerIDs = append(layerIDs, id)
	}
	
	// Display in order
	for _, id := range layerIDs {
		st := pd.layers[id]
		fmt.Printf("%s: %s\n", id, st)
		pd.lastLineCount++
	}
}

// clearLines clears the previous output lines
func (pd *progressDisplay) clearLines() {
	if pd.lastLineCount > 0 {
		// Move cursor up and clear each line
		for i := 0; i < pd.lastLineCount; i++ {
			fmt.Print("\033[A\033[2K") // Move up and clear line
		}
	}
}

// finish completes the display
func (pd *progressDisplay) finish() {
	// Ensure we're on a new line
	if pd.isPulling && len(pd.layers) > 0 {
		fmt.Println()
	}
}
