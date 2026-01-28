package app

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// EditOptions holds options for the edit command
type EditOptions struct {
	*GlobalOptions

	// Model is the model name to edit
	Model string
}

// EditableConfig represents the user-editable parts of a Modelfile
type EditableConfig struct {
	// System is the system prompt
	System string `yaml:"system"`
	
	// Template is the prompt template
	Template string `yaml:"template"`
	
	// Parameters are inference parameters
	Parameters map[string]interface{} `yaml:"parameters,omitempty"`
}

// NewEditCommand creates the edit command.
//
// The edit command allows users to edit the Modelfile using their default editor,
// similar to 'kubectl edit'. It fetches the current Modelfile, opens it in an
// editor, validates changes, and saves back to the server.
//
// Usage:
//
//	xw edit MODEL
//
// Examples:
//
//	# Edit the Modelfile for qwen2-0.5b
//	xw edit qwen2-0.5b
//
// Parameters:
//   - globalOpts: Global options shared across commands
//
// Returns:
//   - A configured cobra.Command for editing Modelfiles
func NewEditCommand(globalOpts *GlobalOptions) *cobra.Command {
	opts := &EditOptions{
		GlobalOptions: globalOpts,
	}

	cmd := &cobra.Command{
		Use:   "edit MODEL",
		Short: "Edit a model's Modelfile",
		Long: `Edit the Modelfile for a model using your default editor.

This command:
  1. Fetches the current Modelfile from the server
  2. Opens it in your default editor ($EDITOR or vi)
  3. Validates the changes
  4. Saves the updated Modelfile back to the server

Similar to 'kubectl edit', changes are only saved if validation passes.`,
		Example: `  # Edit qwen2-0.5b's Modelfile
  xw edit qwen2-0.5b

  # Use a specific editor
  EDITOR=nano xw edit qwen2-7b`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Model = args[0]
			return runEdit(opts)
		},
	}

	return cmd
}

// runEdit executes the edit command logic.
//
// This function:
//   1. Fetches the current Modelfile from the server
//   2. Extracts editable parts and converts to YAML
//   3. Opens the editor for the user to edit YAML
//   4. Validates the modified YAML
//   5. Merges changes back into Modelfile
//   6. Saves the updated Modelfile to the server
//
// Parameters:
//   - opts: Edit command options
//
// Returns:
//   - nil on success
//   - error if fetching, editing, validation, or saving fails
func runEdit(opts *EditOptions) error {
	client := getClient(opts.GlobalOptions)

	// 1. Fetch current Modelfile from server
	fmt.Printf("Fetching configuration for %s...\n", opts.Model)
	result, err := client.GetModel(opts.Model)
	if err != nil {
		return fmt.Errorf("failed to get model: %w", err)
	}

	// Check if model has been downloaded (has Modelfile)
	hasModelfile, _ := result["has_modelfile"].(bool)
	if !hasModelfile {
		return fmt.Errorf("model %s has not been downloaded yet. Run 'xw pull %s' first", opts.Model, opts.Model)
	}

	// Get current Modelfile content
	currentModelfile, ok := result["modelfile"].(string)
	if !ok || currentModelfile == "" {
		return fmt.Errorf("failed to retrieve Modelfile content")
	}

	// 2. Parse Modelfile and extract editable parts
	config, err := parseEditableConfig(currentModelfile)
	if err != nil {
		return fmt.Errorf("failed to parse Modelfile: %w", err)
	}

	// 3. Convert to YAML
	yamlContent, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to generate YAML: %w", err)
	}

	// Add comments to help user
	yamlWithComments := addYAMLComments(string(yamlContent))

	// 4. Create temporary YAML file
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("xw-edit-%s-*.yaml", opts.Model))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath) // Clean up

	// Write YAML to temp file
	if _, err := tmpFile.WriteString(yamlWithComments); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// 5. Open editor
	editor := getEditor()
	if err := openEditor(editor, tmpPath); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}

	// 6. Read modified YAML
	modifiedYAML, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to read modified file: %w", err)
	}

	// Parse modified YAML
	var modifiedConfig EditableConfig
	if err := yaml.Unmarshal(modifiedYAML, &modifiedConfig); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	// Check if content changed
	if configsEqual(config, &modifiedConfig) {
		fmt.Println("No changes made.")
		return nil
	}

	// 7. Validate modified config
	if err := validateEditableConfig(&modifiedConfig); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// 8. Merge changes back into Modelfile
	newModelfile, err := mergeIntoModelfile(currentModelfile, &modifiedConfig)
	if err != nil {
		return fmt.Errorf("failed to merge changes: %w", err)
	}

	// 9. Save to server (server will also validate)
	fmt.Println("Saving changes...")
	if err := client.UpdateModelfile(opts.Model, newModelfile); err != nil {
		return fmt.Errorf("failed to save Modelfile: %w", err)
	}

	fmt.Println("✓ Configuration updated successfully")
	return nil
}

// getEditor returns the editor to use, checking environment variables.
//
// Priority:
//   1. $EDITOR environment variable
//   2. vi (fallback)
//
// Returns:
//   - The editor command to execute
func getEditor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	return "vi"
}

// openEditor opens the specified file in the editor.
//
// Parameters:
//   - editor: Editor command (e.g., "vi", "nano", "emacs")
//   - filePath: Path to the file to edit
//
// Returns:
//   - error if the editor fails to run
func openEditor(editor, filePath string) error {
	cmd := exec.Command(editor, filePath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// parseEditableConfig extracts editable parts from Modelfile.
//
// This function parses the Modelfile and extracts only the parts that
// users are allowed to modify: system prompt, template, and parameters.
//
// Parameters:
//   - modelfile: The complete Modelfile content
//
// Returns:
//   - EditableConfig struct with extracted values
//   - error if parsing fails
func parseEditableConfig(modelfile string) (*EditableConfig, error) {
	config := &EditableConfig{
		System:     "You are a helpful AI assistant.",
		Template:   "{{ .System }}\n{{ .Prompt }}",
		Parameters: make(map[string]interface{}),
	}

	lines := strings.Split(modelfile, "\n")
	inSection := ""
	sectionContent := []string{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check for section headers
		if strings.HasPrefix(trimmed, "System Prompt:") {
			inSection = "system"
			sectionContent = []string{}
			continue
		} else if strings.HasPrefix(trimmed, "Template:") {
			inSection = "template"
			sectionContent = []string{}
			continue
		} else if trimmed == "" && inSection != "" {
			// Empty line ends section
			if inSection == "system" && len(sectionContent) > 0 {
				config.System = strings.TrimSpace(strings.Join(sectionContent, "\n"))
			} else if inSection == "template" && len(sectionContent) > 0 {
				config.Template = strings.TrimSpace(strings.Join(sectionContent, "\n"))
			}
			inSection = ""
			sectionContent = []string{}
			continue
		}

		// Collect section content
		if inSection != "" {
			// Remove leading spaces (usually 2 spaces for indentation)
			content := strings.TrimPrefix(line, "  ")
			sectionContent = append(sectionContent, content)
		}
	}

	// Handle last section if file doesn't end with empty line
	if inSection == "system" && len(sectionContent) > 0 {
		config.System = strings.TrimSpace(strings.Join(sectionContent, "\n"))
	} else if inSection == "template" && len(sectionContent) > 0 {
		config.Template = strings.TrimSpace(strings.Join(sectionContent, "\n"))
	}

	return config, nil
}

// addYAMLComments adds helpful comments to the YAML content.
//
// Parameters:
//   - yaml: The YAML content without comments
//
// Returns:
//   - YAML content with comments
func addYAMLComments(yamlContent string) string {
	header := `# Editable Configuration
# 
# This file contains the user-editable parts of the Modelfile.
# Read-only metadata (FROM, model size, etc.) cannot be changed here.
#
# Fields:
#   system     - System prompt that sets the AI assistant's behavior
#   template   - Prompt template for formatting user input
#   parameters - Inference parameters (temperature, top_p, etc.)
#

`
	return header + yamlContent
}

// configsEqual checks if two configs are equal.
//
// Parameters:
//   - a, b: Configs to compare
//
// Returns:
//   - true if equal
func configsEqual(a, b *EditableConfig) bool {
	if a.System != b.System || a.Template != b.Template {
		return false
	}

	// Compare parameters
	if len(a.Parameters) != len(b.Parameters) {
		return false
	}

	for k, v := range a.Parameters {
		if b.Parameters[k] != v {
			return false
		}
	}

	return true
}

// validateEditableConfig validates the edited configuration.
//
// Validation rules:
//   - System prompt cannot be empty
//   - Template cannot be empty
//   - Parameters must have valid types and values
//
// Parameters:
//   - config: The configuration to validate
//
// Returns:
//   - nil if valid
//   - error describing what is invalid
func validateEditableConfig(config *EditableConfig) error {
	if strings.TrimSpace(config.System) == "" {
		return fmt.Errorf("system prompt cannot be empty")
	}

	if strings.TrimSpace(config.Template) == "" {
		return fmt.Errorf("template cannot be empty")
	}

	// Validate parameters if present
	for key, value := range config.Parameters {
		switch key {
		case "temperature":
			if v, ok := value.(float64); ok {
				if v < 0 || v > 2 {
					return fmt.Errorf("temperature must be between 0 and 2")
				}
			}
		case "top_p":
			if v, ok := value.(float64); ok {
				if v < 0 || v > 1 {
					return fmt.Errorf("top_p must be between 0 and 1")
				}
			}
		}
	}

	return nil
}

// mergeIntoModelfile merges edited config back into the Modelfile.
//
// This function updates only the editable sections of the Modelfile
// while preserving all read-only metadata.
//
// Parameters:
//   - originalModelfile: The original Modelfile content
//   - config: The edited configuration
//
// Returns:
//   - The updated Modelfile content
//   - error if merge fails
func mergeIntoModelfile(originalModelfile string, config *EditableConfig) (string, error) {
	lines := strings.Split(originalModelfile, "\n")
	var result []string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Replace System Prompt section
		if strings.HasPrefix(trimmed, "System Prompt:") {
			result = append(result, line)
			result = append(result, "  "+config.System)
			result = append(result, "")
			// Skip old content until empty line
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				i++
			}
			continue
		}

		// Replace Template section
		if strings.HasPrefix(trimmed, "Template:") {
			result = append(result, line)
			// Handle multi-line template
			templateLines := strings.Split(config.Template, "\n")
			for _, tl := range templateLines {
				result = append(result, "  "+tl)
			}
			result = append(result, "")
			// Skip old content until empty line
			i++
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				i++
			}
			continue
		}

		// Keep other lines as-is
		result = append(result, line)
	}

	return strings.Join(result, "\n"), nil
}

// validateModelfile validates the Modelfile syntax and content.
//
// This performs client-side validation before sending to the server.
// The server will also validate to ensure data integrity.
//
// Validation rules:
//   - Must contain a FROM directive
//   - FROM directive must reference a valid model
//   - File must not be empty
//   - Basic syntax checks
//
// Parameters:
//   - content: The Modelfile content to validate
//
// Returns:
//   - nil if valid
//   - error describing what is invalid
func validateModelfile(content string) error {
	// Trim whitespace
	content = strings.TrimSpace(content)

	// Check if empty
	if content == "" {
		return fmt.Errorf("Modelfile cannot be empty")
	}

	// Check for FROM directive (required)
	lines := strings.Split(content, "\n")
	hasFrom := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "FROM ") {
			hasFrom = true
			// Validate FROM has a model name
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return fmt.Errorf("FROM directive must specify a model name")
			}
			break
		}
	}

	if !hasFrom {
		return fmt.Errorf("Modelfile must contain a FROM directive")
	}

	return nil
}

