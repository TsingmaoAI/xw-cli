package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// ShowOptions holds options for the show command
type ShowOptions struct {
	*GlobalOptions

	// Model is the model name to show information for
	Model string

	// Modelfile displays the Modelfile (model configuration)
	Modelfile bool

	// Parameters displays model parameters
	Parameters bool

	// Template displays the prompt template
	Template bool

	// System displays the system prompt
	System bool

	// License displays the model license
	License bool
}

// NewShowCommand creates the show command.
func NewShowCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &ShowOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "show MODEL",
		Short: "Show information about a model",
		Long: `Display detailed information about an AI model in Ollama-compatible format.

Without any flags, shows complete model information including architecture,
parameters, capabilities, system prompt, and license.`,
		Example: `  # Show all information
  xw show qwen2-0.5b

  # Show only the Modelfile
  xw show qwen2-0.5b --modelfile

  # Show only parameters
  xw show qwen2-0.5b --parameters

  # Show only system prompt
  xw show qwen2-0.5b --system`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Model = args[0]
			return runShow(opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Modelfile, "modelfile", false, "show Modelfile")
	cmd.Flags().BoolVar(&opts.Parameters, "parameters", false, "show parameters")
	cmd.Flags().BoolVar(&opts.Template, "template", false, "show template")
	cmd.Flags().BoolVar(&opts.System, "system", false, "show system prompt")
	cmd.Flags().BoolVar(&opts.License, "license", false, "show license")

	return cmd
}

// runShow executes the show command logic.
func runShow(opts *ShowOptions) error {
	client := getClient(opts.GlobalOptions)

	// Get model info from server
	result, err := client.GetModel(opts.Model)
	if err != nil {
		return fmt.Errorf("failed to get model: %w", err)
	}

	// Check if specific flag is set
	hasModelfile, _ := result["has_modelfile"].(bool)
	modelfileContent, _ := result["modelfile"].(string)

	// Handle specific flags
	if opts.Modelfile {
		showModelfileOnly(result, hasModelfile, modelfileContent)
		return nil
	}

	if opts.Parameters {
		showParametersOnly(hasModelfile, modelfileContent)
		return nil
	}

	if opts.Template {
		showTemplateOnly(hasModelfile, modelfileContent)
		return nil
	}

	if opts.System {
		showSystemOnly(hasModelfile, modelfileContent)
		return nil
	}

	if opts.License {
		showLicenseOnly(result)
		return nil
	}

	// Default: show all information in Ollama format
	showAllInformation(result, hasModelfile, modelfileContent)
	return nil
}

// showAllInformation displays complete model information (default mode)
func showAllInformation(result map[string]interface{}, hasModelfile bool, modelfileContent string) {
	// Extract basic info
	family, _ := result["family"].(string)
	params, _ := result["parameters"].(float64)
	ctxLen, _ := result["context_length"].(float64)
	embeddingLen, _ := result["embedding_length"].(float64)
	tag, _ := result["tag"].(string)
	license, _ := result["license"].(string)
	capabilities, _ := result["capabilities"].([]interface{})

	// Parse Modelfile if exists
	var systemPrompt string
	var inferenceParams map[string]string

	if hasModelfile {
		systemPrompt = parseSystemFromModelfile(modelfileContent)
		inferenceParams = parseParametersFromModelfile(modelfileContent)
	}

	// Use defaults if not found in Modelfile
	if systemPrompt == "" {
		systemPrompt = "You are a helpful AI assistant."
	}

	// Display format follows Ollama exactly
	fmt.Println()

	// 1. Basic model information
	if family != "" {
		fmt.Printf("  %-25s %s\n", "architecture", family)
	}
	if params > 0 {
		fmt.Printf("  %-25s %.1fB\n", "parameters", params)
	}
	if ctxLen > 0 {
		fmt.Printf("  %-25s %.0f\n", "context length", ctxLen)
	}
	if embeddingLen > 0 {
		fmt.Printf("  %-25s %.0f\n", "embedding length", embeddingLen)
	}
	if tag != "" {
		fmt.Printf("  %-25s %s\n", "quantization", strings.ToUpper(tag))
	}

	fmt.Println()

	// 2. Capabilities
	if len(capabilities) > 0 {
		fmt.Println("Capabilities")
		for _, cap := range capabilities {
			if capStr, ok := cap.(string); ok {
				fmt.Printf("  %s\n", capStr)
			}
		}
	} else {
		fmt.Println("Capabilities")
		fmt.Println("  completion")
	}
	fmt.Println()

	// 3. Parameters (only if Modelfile exists and has parameters)
	if len(inferenceParams) > 0 {
		fmt.Println("Parameters")
		for key, value := range inferenceParams {
			fmt.Printf("  %-25s %s\n", key, value)
		}
		fmt.Println()
	}

	// 4. System prompt (only if Modelfile exists)
	if hasModelfile && systemPrompt != "" {
		fmt.Println("System")
		fmt.Println(systemPrompt)
		fmt.Println()
	}

	// 5. License
	if license != "" {
		fmt.Println("License")
		fmt.Println(license)
		fmt.Println()
	}
}

// showModelfileOnly displays the complete Modelfile
func showModelfileOnly(result map[string]interface{}, hasModelfile bool, modelfileContent string) {
	if hasModelfile && modelfileContent != "" {
		// Model is downloaded, show actual Modelfile
		fmt.Println(modelfileContent)
		return
	}

	// Model not downloaded, generate default Modelfile from ModelSpec
	generateDefaultModelfile(result)
}

// generateDefaultModelfile creates a default Modelfile from ModelSpec
func generateDefaultModelfile(result map[string]interface{}) {
	fmt.Println("# Modelfile")
	fmt.Println()
	
	// FROM directive
	if model, ok := result["model"].(map[string]interface{}); ok {
		if name, ok := model["name"].(string); ok {
			fmt.Printf("FROM %s", name)
			if tag, ok := result["tag"].(string); ok && tag != "" {
				fmt.Printf(":%s", tag)
			}
			fmt.Println()
			fmt.Println()
		}
		}
		
	// Description as comment
	if desc, ok := result["description"].(string); ok && desc != "" {
		fmt.Printf("# %s\n\n", desc)
		}

	// Default TEMPLATE
	fmt.Println("TEMPLATE \"\"\"{{ .System }}")
	fmt.Println("{{ .Prompt }}\"\"\"")
	fmt.Println()

	// Default SYSTEM
	fmt.Println("SYSTEM \"\"\"You are a helpful AI assistant.\"\"\"")
	fmt.Println()

	// Model info as comments
	if params, ok := result["parameters"].(float64); ok {
		fmt.Printf("# Model Size: %.1fB parameters\n", params)
	}
	if ctxLen, ok := result["context_length"].(float64); ok {
		fmt.Printf("# Context Length: %.0f tokens\n", ctxLen)
	}
	if vram, ok := result["required_vram"].(float64); ok {
		fmt.Printf("# Required VRAM: %.0f GB\n", vram)
	}
	fmt.Println()

	// Supported devices
	if devices, ok := result["supported_devices"].([]interface{}); ok && len(devices) > 0 {
		fmt.Print("# Supported Devices: ")
		deviceStrs := make([]string, 0, len(devices))
		for _, device := range devices {
			if deviceStr, ok := device.(string); ok {
				deviceStrs = append(deviceStrs, deviceStr)
	}
		}
		fmt.Println(strings.Join(deviceStrs, ", "))
	}
}

// showParametersOnly displays only the parameters section
func showParametersOnly(hasModelfile bool, modelfileContent string) {
	if !hasModelfile {
		fmt.Println("No parameters defined (model not downloaded or no Modelfile)")
		return
	}

	params := parseParametersFromModelfile(modelfileContent)
	if len(params) == 0 {
		fmt.Println("No parameters defined in Modelfile")
		return
	}

	for key, value := range params {
		fmt.Printf("%s %s\n", key, value)
	}
}

// showTemplateOnly displays only the template section
func showTemplateOnly(hasModelfile bool, modelfileContent string) {
	if !hasModelfile {
		fmt.Println("{{ .System }}")
		fmt.Println("{{ .Prompt }}")
		return
	}

	template := parseTemplateFromModelfile(modelfileContent)
	if template == "" {
		fmt.Println("{{ .System }}")
		fmt.Println("{{ .Prompt }}")
		return
	}

	fmt.Println(template)
}

// showSystemOnly displays only the system prompt
func showSystemOnly(hasModelfile bool, modelfileContent string) {
	if !hasModelfile {
		fmt.Println("You are a helpful AI assistant.")
		return
	}

	system := parseSystemFromModelfile(modelfileContent)
	if system == "" {
		fmt.Println("You are a helpful AI assistant.")
		return
	}

	fmt.Println(system)
}

// showLicenseOnly displays only the license
func showLicenseOnly(result map[string]interface{}) {
	if license, ok := result["license"].(string); ok && license != "" {
		fmt.Println(license)
	} else {
		fmt.Println("No license information available.")
	}
}

// parseTemplateFromModelfile extracts the TEMPLATE directive from Modelfile
func parseTemplateFromModelfile(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "TEMPLATE ") {
			rest := strings.TrimPrefix(trimmed, "TEMPLATE ")
			rest = strings.TrimSpace(rest)

			// Handle triple quotes
			if strings.HasPrefix(rest, "\"\"\"") {
				rest = strings.TrimPrefix(rest, "\"\"\"")

				// Single line
				if strings.Contains(rest, "\"\"\"") {
					return strings.Split(rest, "\"\"\"")[0]
				}

				// Multi-line
				var parts []string
				if rest != "" {
					parts = append(parts, rest)
				}

				for j := i + 1; j < len(lines); j++ {
					if strings.Contains(lines[j], "\"\"\"") {
						ending := strings.Split(lines[j], "\"\"\"")[0]
						if ending != "" {
							parts = append(parts, ending)
						}
						break
					}
					parts = append(parts, lines[j])
				}
				return strings.Join(parts, "\n")
			}

			// Handle single quotes
			if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
				return strings.Trim(rest, "\"")
			}

			return rest
		}
	}
	
	return ""
}

// parseSystemFromModelfile extracts the SYSTEM directive from Modelfile
func parseSystemFromModelfile(content string) string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "SYSTEM ") {
			rest := strings.TrimPrefix(trimmed, "SYSTEM ")
			rest = strings.TrimSpace(rest)

			// Handle triple quotes
			if strings.HasPrefix(rest, "\"\"\"") {
				rest = strings.TrimPrefix(rest, "\"\"\"")

				// Single line
				if strings.Contains(rest, "\"\"\"") {
					return strings.Split(rest, "\"\"\"")[0]
				}

				// Multi-line
				var parts []string
				if rest != "" {
					parts = append(parts, rest)
				}

				for j := i + 1; j < len(lines); j++ {
					if strings.Contains(lines[j], "\"\"\"") {
						ending := strings.Split(lines[j], "\"\"\"")[0]
						if ending != "" {
							parts = append(parts, ending)
						}
						break
					}
					parts = append(parts, lines[j])
				}
				return strings.Join(parts, "\n")
			}

			// Handle single quotes
			if strings.HasPrefix(rest, "\"") && strings.HasSuffix(rest, "\"") {
				return strings.Trim(rest, "\"")
			}

			return rest
		}
	}

	return ""
}

// parseParametersFromModelfile extracts PARAMETER directives from Modelfile
func parseParametersFromModelfile(content string) map[string]string {
	params := make(map[string]string)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "PARAMETER ") {
			rest := strings.TrimPrefix(trimmed, "PARAMETER ")
			parts := strings.Fields(rest)

			if len(parts) >= 2 {
				key := parts[0]
				value := strings.Join(parts[1:], " ")
				value = strings.Trim(value, "\"")
				params[key] = value
			}
		}
	}
	
	return params
}
