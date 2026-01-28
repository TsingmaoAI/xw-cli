package app

import (
	"fmt"

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
//
// The show command displays information about a model, similar to Ollama's show command.
// Information is retrieved from the Modelfile (if exists) or from the ModelSpec.
//
// Usage:
//
//	xw show MODEL [--modelfile|--parameters|--template|--system|--license]
//
// Examples:
//
//	# Show all information about a model
//	xw show qwen2.5-7b-instruct
//
//	# Show only the Modelfile
//	xw show qwen2.5-7b-instruct --modelfile
//
//	# Show only parameters
//	xw show qwen2.5-7b-instruct --parameters
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for showing model information
func NewShowCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &ShowOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "show MODEL",
		Short: "Show information about a model",
		Long: `Display information about a model in Ollama-compatible format.

Without any flags, shows complete model information including architecture,
parameters, and license. With flags, displays specific sections.

Information is retrieved from the Modelfile (user-editable) if it exists,
otherwise from the model specification (built-in configuration).`,
		Example: `  # Show all information
  xw show qwen2.5-7b-instruct

  # Show only the Modelfile
  xw show qwen2.5-7b-instruct --modelfile

  # Show only parameters
  xw show qwen2.5-7b-instruct --parameters

  # Show only system prompt
  xw show qwen2.5-7b-instruct --system

  # Show only template
  xw show qwen2.5-7b-instruct --template

  # Show only license
  xw show qwen2.5-7b-instruct --license`,
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
//
// This function fetches model information from the server and displays it
// according to the selected options.
//
// Parameters:
//   - opts: Show command options
//
// Returns:
//   - nil on success
//   - error if the model doesn't exist or fetching information fails
func runShow(opts *ShowOptions) error {
	client := getClient(opts.GlobalOptions)

	// Get model info from server
	modelInfo, err := client.GetModel(opts.Model)
	if err != nil {
		return fmt.Errorf("failed to get model info: %w", err)
	}

	// Handle specific flags
	if opts.Modelfile {
		displayModelfile(modelInfo)
		return nil
	}

	if opts.Parameters {
		displayParameters(modelInfo)
		return nil
	}

	if opts.Template {
		displayTemplate(modelInfo)
		return nil
	}

	if opts.System {
		displaySystem(modelInfo)
		return nil
	}

	if opts.License {
		displayLicense(modelInfo)
		return nil
	}

	// Default: show all information (Ollama format)
	displayAllInfo(modelInfo)
	return nil
}

// displayAllInfo displays complete model information in Ollama format
func displayAllInfo(info map[string]interface{}) {
	// Model section
	fmt.Println("  Model")
	fmt.Println()

	if arch, ok := info["architecture"].(string); ok && arch != "" {
		fmt.Printf("    %-20s%s\n", "architecture", arch)
	}

	if family, ok := info["family"].(string); ok && family != "" {
		fmt.Printf("    %-20s%s\n", "family", family)
	}

	if params, ok := info["parameters"].(float64); ok && params > 0 {
		fmt.Printf("    %-20s%.1fB\n", "parameters", params)
	}

	if ctxLen, ok := info["context_length"].(float64); ok && ctxLen > 0 {
		fmt.Printf("    %-20s%.0f\n", "context length", ctxLen)
	}

	if embLen, ok := info["embedding_length"].(float64); ok && embLen > 0 {
		fmt.Printf("    %-20s%.0f\n", "embedding length", embLen)
	}

	if quant, ok := info["quantization"].(string); ok && quant != "" {
		fmt.Printf("    %-20s%s\n", "quantization", quant)
	}

	fmt.Println()
	fmt.Println()

	// Capabilities section
	if caps, ok := info["capabilities"].([]interface{}); ok && len(caps) > 0 {
		fmt.Println("  Capabilities")
		fmt.Println()
		for _, cap := range caps {
			if capStr, ok := cap.(string); ok {
				fmt.Printf("    %s\n", capStr)
			}
		}
		fmt.Println()
		fmt.Println()
	}

	// Parameters section (inference parameters from Modelfile)
	if params, ok := info["inference_parameters"].(map[string]interface{}); ok && len(params) > 0 {
		fmt.Println("  Parameters")
		fmt.Println()
		for key, value := range params {
			fmt.Printf("    %-20s%v\n", key, value)
		}
		fmt.Println()
		fmt.Println()
	}

	// System section
	if system, ok := info["system"].(string); ok && system != "" {
		fmt.Println("  System")
		fmt.Println()
		fmt.Printf("    %s\n", system)
		fmt.Println()
		fmt.Println()
	}

	// License section
	if license, ok := info["license"].(string); ok && license != "" {
		fmt.Println("  License")
		fmt.Println()
		// Print license with indentation
		for _, line := range splitLines(license) {
			fmt.Printf("    %s\n", line)
		}
		fmt.Println()
		fmt.Println()
	}
}

// splitLines splits text into lines
func splitLines(text string) []string {
	lines := []string{}
	current := ""
	for _, ch := range text {
		if ch == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// displayModelfile displays the complete Modelfile
func displayModelfile(info map[string]interface{}) {
	// If Modelfile exists, show it directly (Ollama-compatible format)
	if modelfile, ok := info["modelfile"].(string); ok && modelfile != "" {
		fmt.Print(modelfile)
		return
	}

	// If no Modelfile exists, show a message
	fmt.Println("Error: Modelfile not found for this model")
}

// displayParameters displays only the parameters section
func displayParameters(info map[string]interface{}) {
	if params, ok := info["inference_parameters"].(map[string]interface{}); ok && len(params) > 0 {
		for key, value := range params {
			fmt.Printf("%s %v\n", key, value)
		}
		return
	}

	// No parameters defined
	// Ollama shows nothing when no parameters are set, so we do the same
}

// displayTemplate displays only the template
func displayTemplate(info map[string]interface{}) {
	if template, ok := info["template"].(string); ok && template != "" {
		fmt.Println(template)
		return
	}

	// Default template
	fmt.Println("{{ .System }}")
	fmt.Println("{{ .Prompt }}")
}

// displaySystem displays only the system prompt
func displaySystem(info map[string]interface{}) {
	if system, ok := info["system"].(string); ok && system != "" {
		fmt.Println(system)
		return
	}

	// Default system prompt
	fmt.Println("You are a helpful AI assistant.")
}

// displayLicense displays only the license
func displayLicense(info map[string]interface{}) {
	if license, ok := info["license"].(string); ok && license != "" {
		fmt.Println(license)
	}
}
