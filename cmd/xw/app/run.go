package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
)

// RunOptions holds options for the run command
type RunOptions struct {
	*GlobalOptions

	// Model is the model ID to run
	Model string

	// Alias is the optional instance alias
	Alias string

	// Engine is the inference engine in format "backend:mode" (e.g., "vllm:docker", "mindie:native")
	Engine string

	// Device is the device list (e.g., "0", "0,1,2,3")
	Device string
	
	// TensorParallel is the tensor parallelism degree (must be 1/2/4/8)
	TensorParallel int
}

// NewRunCommand creates the run command.
//
// The run command is a convenience command that combines start and chat:
//  1. Check if instance exists in ps list
//  2. If exists, wait for it to be ready
//  3. If not exists, start the instance
//  4. Once ready, launch interactive chat
//
// Usage:
//
//	xw run MODEL [--alias ALIAS] [--backend TYPE] [--mode MODE]
//
// Examples:
//
//	xw run qwen2.5-7b-instruct
//	xw run qwen2.5-7b-instruct --alias my-model
func NewRunCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &RunOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "run MODEL",
		Short: "Run a model with interactive chat",
		Long: `Run a model and start an interactive chat session.

This command combines instance management and chat interaction:
- Checks if the model instance is already running
- Starts the instance if not running
- Waits for the instance to be ready
- Launches an interactive chat session

If --alias is not specified, the model ID is used as the alias.

Engine Selection:
  Engine is specified as "backend:mode" (e.g., "vllm:docker", "mindie:native").
  If not specified, xw will automatically select the best available engine.

Device Selection:
  Specify which AI accelerator devices to use (e.g., --device 0 or --device 0,1,2,3)
  If not specified, the system will automatically allocate available devices.`,
		Example: `  # Run a model with default settings
  xw run qwen2.5-7b-instruct

  # Run with a custom alias
  xw run qwen2.5-7b-instruct --alias my-chat

  # Run with specific engine
  xw run qwen2-7b --engine vllm:docker

  # Run on specific devices
  xw run qwen2.5-7b-instruct --device 0,1`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Model = args[0]
			return runRun(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Alias, "alias", "", "instance alias (defaults to model ID)")
	cmd.Flags().StringVar(&opts.Engine, "engine", "", "inference engine in format backend:mode (e.g., vllm:docker)")
	cmd.Flags().StringVar(&opts.Device, "device", "", "device list (e.g., 0 or 0,1,2,3)")
	cmd.Flags().IntVar(&opts.TensorParallel, "tp", 0, "tensor parallelism degree (must be 1, 2, 4, or 8)")

	return cmd
}

// runRun executes the run command logic
func runRun(opts *RunOptions) error {
	client := getClient(opts.GlobalOptions)

	// Use model ID as alias if not specified
	alias := opts.Alias
	if alias == "" {
		alias = opts.Model
	}

	// Step 1: Check if instance exists
	instances, err := client.ListInstances(false) // Only show running instances
	if err != nil {
		return fmt.Errorf("failed to list instances: %w", err)
	}

	var instanceExists bool
	var instanceReady bool
	var instancePort int
	for _, inst := range instances {
		instMap, ok := inst.(map[string]interface{})
		if !ok {
			continue
		}
		
		instAlias, _ := instMap["alias"].(string)
		if instAlias == alias {
			instanceExists = true
			if port, ok := instMap["port"].(float64); ok {
				instancePort = int(port)
			}
			state, _ := instMap["state"].(string)
			
			// Check if instance is already ready
			if state == "ready" {
				instanceReady = true
				fmt.Printf("Found existing instance: %s (state: %s)\n\n", alias, state)
			} else {
				fmt.Printf("Found existing instance: %s (state: %s)\n", alias, state)
			}
			break
		}
	}

	// Step 2: Start instance if not exists
	if !instanceExists {
		fmt.Printf("Starting new instance: %s\n", alias)
		
		startOpts := &StartOptions{
			GlobalOptions:  opts.GlobalOptions,
			Model:          opts.Model,
			Alias:          alias,
			Engine:         opts.Engine,
			Device:         opts.Device,
			TensorParallel: opts.TensorParallel,
			Detach:         true, // Run in background for run command
		}

		if err := runStart(startOpts); err != nil {
			return fmt.Errorf("failed to start instance: %w", err)
		}

		// Get the port of the newly started instance
		instances, err = client.ListInstances(false)
		if err != nil {
			return fmt.Errorf("failed to list instances after start: %w", err)
		}

		for _, inst := range instances {
			instMap, ok := inst.(map[string]interface{})
			if !ok {
				continue
			}
			
			instAlias, _ := instMap["alias"].(string)
			if instAlias == alias {
				if port, ok := instMap["port"].(float64); ok {
					instancePort = int(port)
				}
				break
			}
		}

		if instancePort == 0 {
			return fmt.Errorf("instance started but port not found")
		}
	}

	// Step 3: Wait for instance to be ready (no timeout, until Ctrl+C)
	if !instanceReady {
		// Loading spinner characters
		spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spinnerIdx := 0
		startTime := time.Now()
		lastCheck := time.Now()
		checkInterval := 5 * time.Second
		spinnerInterval := 100 * time.Millisecond
		
		// Channel for check results
		type checkResult struct {
			ready      bool
			err        error
			errorState bool
			errorMsg   string
		}
		resultCh := make(chan checkResult, 1)
		
		// Function to perform health check
		performCheck := func() {
			// First check instance state
			instances, err := client.ListInstances(false)
			result := checkResult{}
			
			if err == nil {
				for _, inst := range instances {
					instMap, ok := inst.(map[string]interface{})
					if !ok {
						continue
					}
					
					instAlias, _ := instMap["alias"].(string)
					if instAlias == alias {
						state, _ := instMap["state"].(string)
						if state == "error" {
							result.errorState = true
							if errMsg, ok := instMap["error"].(string); ok {
								result.errorMsg = errMsg
							}
						} else if state == "ready" {
							// Instance is already in ready state
							result.ready = true
						}
						break
					}
				}
			}
			
			if !result.errorState && !result.ready {
				// State is not error and not ready, check endpoint accessibility
				ready, err := client.CheckInstanceReady(alias)
				result.ready = ready
				result.err = err
			}
			
			resultCh <- result
		}
		
		// Start first check
		go performCheck()
		
		for {
			select {
			case result := <-resultCh:
				// Got check result
				if result.errorState {
					// Instance is in error state
					fmt.Printf("\r\033[K")  // Clear current line
					fmt.Println()
					fmt.Printf("✗ Instance failed to start (state: error)\n")
					
					if result.errorMsg != "" {
						fmt.Printf("  Error: %s\n", result.errorMsg)
					}
					fmt.Println()
					fmt.Println("Use 'xw ps -a' to view instance details")
					fmt.Println("Use 'xw stop' to remove the failed instance")
					
					return fmt.Errorf("instance failed to start")
				}
				
				if result.ready {
					// Instance is ready!
					fmt.Printf("\r\033[K")  // Clear current line
					fmt.Printf("✓ Instance is ready!\n\n")
					goto readyComplete  // Exit immediately without updating spinner
				}
				
				// Not ready yet, schedule next check after interval
				lastCheck = time.Now()
				go func() {
					time.Sleep(checkInterval)
					performCheck()
				}()
				
			case <-time.After(spinnerInterval):
				// Just update spinner animation
			}
			
			// Update spinner display
			elapsed := int(time.Since(startTime).Seconds())
			spinner := spinners[spinnerIdx%len(spinners)]
			spinnerIdx++
			
			if time.Since(lastCheck) < checkInterval {
				fmt.Printf("\r%s Waiting for instance to be ready... %ds", spinner, elapsed)
			} else {
				fmt.Printf("\r%s Waiting for instance to be ready... %ds (checking)", spinner, elapsed)
			}
		}
	}

readyComplete:
	// Use server's base URL - server has API proxy to forward requests to instances
	instanceEndpoint := client.GetBaseURL()

	// Step 4: Start interactive chat
	fmt.Println("=" + strings.Repeat("=", 60))
	fmt.Printf("Chat session started with: %s\n", alias)
	fmt.Println("Type your message and press Enter. Use '/h' or '/?' for help, '/quit' to exit.")
	fmt.Println("=" + strings.Repeat("=", 60))
	fmt.Println()

	return startInteractiveChat(alias, instanceEndpoint)
}

// chatSession holds the state of a chat session
type chatSession struct {
	alias         string
	endpoint      string
	messages      []map[string]string
	systemPrompt  string
	temperature   float64
	topP          float64
	maxTokens     int
	readline      *readline.Instance
	output        io.Writer // Store output writer for streaming
	cancelFunc    context.CancelFunc // To cancel ongoing requests
}

// startInteractiveChat starts an interactive chat session with the model
// alias is used as the model name in the API request
func startInteractiveChat(alias, endpoint string) error {
	// Create readline instance with history support
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          ">>> ",
		HistoryFile:     "", // No persistent history file
		InterruptPrompt: "^C",
		EOFPrompt:       "/quit",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer rl.Close()

	session := &chatSession{
		alias:        alias,
		endpoint:     endpoint,
		messages:     []map[string]string{},
		systemPrompt: "", // Empty by default, model will use its default
		temperature:  0.7,
		topP:         0.9,
		maxTokens:    2048,
		readline:     rl,
		output:       rl.Stdout(),
	}

	for {
		// Read line with history support (up/down arrow keys)
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				// Ctrl+C pressed - cancel any ongoing operation
				if session.cancelFunc != nil {
					session.cancelFunc()
					session.cancelFunc = nil
					fmt.Fprintln(session.output, "\nOperation cancelled.")
				}
				continue // Continue the conversation, don't exit
			}
			// io.EOF or other errors - exit
			break
		}

		userInput := strings.TrimSpace(line)

		if userInput == "" {
			continue
		}

		// Check if it's a command (starts with /)
		if strings.HasPrefix(userInput, "/") {
			if shouldExit := session.handleCommand(userInput); shouldExit {
				break
			}
			continue
		}

		// Regular message - add to history
		session.messages = append(session.messages, map[string]string{
			"role":    "user",
			"content": userInput,
		})

		// Create a cancellable context for this request
		ctx, cancel := context.WithCancel(context.Background())
		session.cancelFunc = cancel
		
		// Set up signal handler for Ctrl+C during request/streaming
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigChan
			// Cancel the request
			cancel()
		}()
		
		// Clean the line and print Assistant prompt
		session.readline.Operation.Clean()
		fmt.Fprint(session.output, "\nAssistant: ")
		
		response, err := session.sendChatRequestWithContext(ctx)
		
		// Stop signal handler and clear the cancel function
		signal.Stop(sigChan)
		close(sigChan)
		session.cancelFunc = nil
		
		if err != nil {
			if ctx.Err() != context.Canceled {
				// Only show error if it's not a cancellation
				fmt.Fprintf(session.output, "\nError: %v\n", err)
			} else {
				// Just print a newline for cancelled operations
				fmt.Fprint(session.output, "\n")
			}
			// Remove the failed user message from history
			session.messages = session.messages[:len(session.messages)-1]
			// Refresh readline state
			session.readline.Refresh()
			continue
		}

		fmt.Fprint(session.output, "\n") // New line after streaming response
		
		// Force refresh readline to restore proper state after streaming
		session.readline.Refresh()

		// Add assistant response to history
		session.messages = append(session.messages, map[string]string{
			"role":    "assistant",
			"content": response,
		})
	}

	return nil
}

// handleCommand processes slash commands
// Returns true if the session should exit
func (s *chatSession) handleCommand(cmd string) bool {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return false
	}

	command := parts[0]

	switch command {
	case "/quit":
		fmt.Println("Goodbye!")
		return true

	case "/h", "/?":
		s.showHelp()

	case "/clear":
		s.messages = []map[string]string{}
		fmt.Println("Context cleared.")

	case "/history":
		s.showHistory()

	case "/set":
		s.handleSetCommand(parts[1:])

	case "/show":
		s.showConfig()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Type /help to see available commands.")
	}

	return false
}

// handleSetCommand handles /set commands
func (s *chatSession) handleSetCommand(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: /set <parameter> <value>")
		fmt.Println("Available parameters: system-prompt, temperature, top-p, max-tokens")
		return
	}

	param := args[0]
	value := strings.Join(args[1:], " ")

	switch param {
	case "system-prompt", "system":
		s.systemPrompt = value
		fmt.Printf("System prompt set to: %s\n", value)

	case "temperature", "temp":
		temp, err := parseFloat(value)
		if err != nil || temp < 0 || temp > 2 {
			fmt.Println("Invalid temperature. Must be between 0 and 2.")
			return
		}
		s.temperature = temp
		fmt.Printf("Temperature set to: %.2f\n", temp)

	case "top-p", "top_p":
		topP, err := parseFloat(value)
		if err != nil || topP < 0 || topP > 1 {
			fmt.Println("Invalid top-p. Must be between 0 and 1.")
			return
		}
		s.topP = topP
		fmt.Printf("Top-p set to: %.2f\n", topP)

	case "max-tokens", "max_tokens":
		maxTokens, err := parseInt(value)
		if err != nil || maxTokens < 1 {
			fmt.Println("Invalid max-tokens. Must be a positive integer.")
			return
		}
		s.maxTokens = maxTokens
		fmt.Printf("Max tokens set to: %d\n", maxTokens)

	default:
		fmt.Printf("Unknown parameter: %s\n", param)
		fmt.Println("Available: system-prompt, temperature, top-p, max-tokens")
	}
}

// showHelp displays available commands
func (s *chatSession) showHelp() {
	fmt.Println()
	fmt.Println("  /h, /?                  Show this help")
	fmt.Println("  /quit                   Exit the chat session")
	fmt.Println("  /clear                  Clear conversation history")
	fmt.Println("  /history                Show conversation history")
	fmt.Println("  /show                   Show current configuration")
	fmt.Println("  /set <param> <value>    Set a parameter:")
	fmt.Println("    system-prompt <text>    Set system prompt")
	fmt.Println("    temperature <0-2>       Set temperature (default: 0.7)")
	fmt.Println("    top-p <0-1>             Set top-p (default: 0.9)")
	fmt.Println("    max-tokens <number>     Set max tokens (default: 2048)")
	fmt.Println()
}

// showHistory displays the conversation history
func (s *chatSession) showHistory() {
	if len(s.messages) == 0 {
		fmt.Println("No messages in history.")
		return
	}

	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("Conversation History:")
	fmt.Println(strings.Repeat("-", 60))

	for i, msg := range s.messages {
		role := msg["role"]
		content := msg["content"]

		if role == "user" {
			fmt.Printf("\n[%d] You:\n%s\n", i+1, content)
		} else {
			fmt.Printf("\n[%d] Assistant:\n%s\n", i+1, content)
		}
	}
	fmt.Println(strings.Repeat("-", 60))
}

// showConfig displays current configuration
func (s *chatSession) showConfig() {
	fmt.Println("\n" + strings.Repeat("-", 60))
	fmt.Println("Current Configuration:")
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Model:          %s\n", s.alias)
	fmt.Printf("Endpoint:       %s\n", s.endpoint)
	if s.systemPrompt != "" {
		fmt.Printf("System Prompt:  %s\n", s.systemPrompt)
	} else {
		fmt.Printf("System Prompt:  (using model default)\n")
	}
	fmt.Printf("Temperature:    %.2f\n", s.temperature)
	fmt.Printf("Top-p:          %.2f\n", s.topP)
	fmt.Printf("Max Tokens:     %d\n", s.maxTokens)
	fmt.Printf("Messages:       %d\n", len(s.messages))
	fmt.Println(strings.Repeat("-", 60))
}

// parseFloat parses a string to float64
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// parseInt parses a string to int
func parseInt(s string) (int, error) {
	var i int
	_, err := fmt.Sscanf(s, "%d", &i)
	return i, err
}

// sendChatRequestWithContext sends a streaming chat completion request to the model endpoint with cancellation support
func (s *chatSession) sendChatRequestWithContext(ctx context.Context) (string, error) {
	// Build messages with system prompt if set
	messages := s.messages
	if s.systemPrompt != "" {
		// Prepend system message
		messages = append([]map[string]string{
			{
				"role":    "system",
				"content": s.systemPrompt,
			},
		}, messages...)
	}

	// Prepare request body for OpenAI-compatible API with streaming
	reqBody := map[string]interface{}{
		"model":       s.alias,
		"messages":    messages,
		"stream":      true,
		"temperature": s.temperature,
		"top_p":       s.topP,
		"max_tokens":  s.maxTokens,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send POST request to /v1/chat/completions with context for cancellation
	url := s.endpoint + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// No timeout - rely on context cancellation
	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read streaming response (SSE format)
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	
	for scanner.Scan() {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		
		line := scanner.Text()
		
		// Skip empty lines
		if line == "" {
			continue
		}
		
		// SSE format: "data: {...}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		
		// Remove "data: " prefix
		data := strings.TrimPrefix(line, "data: ")
		
		// Check for stream end signal
		if data == "[DONE]" {
			break
		}
		
		// Parse JSON chunk
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // Skip invalid chunks
		}
		
		// Extract delta content
		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}
		
		content, ok := delta["content"].(string)
		if !ok || content == "" {
			continue
		}
		
		// Print content in real-time to the session output
		fmt.Fprint(s.output, content)
		fullContent.WriteString(content)
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading stream: %w", err)
	}

	return fullContent.String(), nil
}

