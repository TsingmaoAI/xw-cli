package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// RuntimeParamsTemplate represents a parameter template for a specific runtime configuration.
//
// Template name format: {chip_config_key}_{model_id}_{backend_name}
// Example: ascend-910b_qwen2-72b_mlguider
type RuntimeParamsTemplate struct {
	// Name is the template identifier (chip_model_backend)
	Name string `yaml:"name"`
	
	// Params is a list of key=value parameters
	// Example: ["max_batch_size=128", "tensor_parallel=4"]
	Params []string `yaml:"params"`
}

// RuntimeParamsConfig is the root configuration for runtime parameter templates.
type RuntimeParamsConfig struct {
	// Version specifies the configuration schema version
	Version string `yaml:"version"`
	
	// Templates contains all parameter templates
	Templates []RuntimeParamsTemplate `yaml:"templates"`
}

// LoadRuntimeParamsConfig loads runtime parameter templates from the default location.
//
// Returns:
//   - Runtime parameters configuration
//   - Error if file exists but cannot be parsed (returns empty config if file doesn't exist)
func LoadRuntimeParamsConfig() (*RuntimeParamsConfig, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return &RuntimeParamsConfig{Templates: []RuntimeParamsTemplate{}}, nil
	}
	
	configPath := filepath.Join(homeDir, DefaultConfigDirName, "runtime_params.yaml")
	return LoadRuntimeParamsConfigFrom(configPath)
}

// LoadRuntimeParamsConfigFrom loads runtime parameter templates from a specific file.
//
// Parameters:
//   - configPath: Path to the runtime_params.yaml file
//
// Returns:
//   - Runtime parameters configuration
//   - Error if file exists but cannot be parsed (returns empty config if file doesn't exist)
func LoadRuntimeParamsConfigFrom(configPath string) (*RuntimeParamsConfig, error) {
	// If file doesn't exist, return empty config (this is optional)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		logger.Debug("Runtime params config not found at %s, using empty config", configPath)
		return &RuntimeParamsConfig{
			Version:   "1.0",
			Templates: []RuntimeParamsTemplate{},
		}, nil
	}
	
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime params config: %w", err)
	}
	
	var cfg RuntimeParamsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse runtime params config: %w", err)
	}
	
	// Validate the configuration
	if err := validateRuntimeParamsConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid runtime params config: %w", err)
	}
	
	logger.Debug("Loaded runtime params config with %d template(s)", len(cfg.Templates))
	return &cfg, nil
}

// validateRuntimeParamsConfig validates the runtime parameters configuration.
func validateRuntimeParamsConfig(cfg *RuntimeParamsConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	
	// Check for duplicate template names
	seen := make(map[string]bool)
	for i, tmpl := range cfg.Templates {
		if tmpl.Name == "" {
			return fmt.Errorf("template[%d]: name cannot be empty", i)
		}
		
		if seen[tmpl.Name] {
			return fmt.Errorf("duplicate template name: %s", tmpl.Name)
		}
		seen[tmpl.Name] = true
		
		// Validate each parameter
		for j, param := range tmpl.Params {
			if err := validateParamFormat(param); err != nil {
				return fmt.Errorf("template[%d].params[%d]: %w", i, j, err)
			}
		}
	}
	
	return nil
}

// validateParamFormat validates that a parameter is in key=value format.
func validateParamFormat(param string) error {
	param = strings.TrimSpace(param)
	if param == "" {
		return fmt.Errorf("parameter cannot be empty")
	}
	
	parts := strings.SplitN(param, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format: expected 'key=value', got '%s'", param)
	}
	
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return fmt.Errorf("parameter key cannot be empty")
	}
	
	// Value can be empty (e.g., "debug=")
	return nil
}

// GetTemplateParams retrieves parameters for a specific template.
//
// Parameters:
//   - cfg: Runtime parameters configuration
//   - chipConfigKey: Chip config key (e.g., "ascend-910b")
//   - modelID: Model identifier (e.g., "qwen2-72b")
//   - backendName: Backend name without mode (e.g., "mlguider", not "mlguider:docker")
//
// Returns:
//   - List of parameters in key=value format
func GetTemplateParams(cfg *RuntimeParamsConfig, chipConfigKey, modelID, backendName string) []string {
	if cfg == nil {
		return nil
	}
	
	// Construct template name: chip_model_backend
	templateName := fmt.Sprintf("%s_%s_%s", chipConfigKey, modelID, backendName)
	
	for _, tmpl := range cfg.Templates {
		if tmpl.Name == templateName {
			logger.Debug("Found template params for %s: %d parameter(s)", templateName, len(tmpl.Params))
			return tmpl.Params
		}
	}
	
	logger.Debug("No template params found for %s", templateName)
	return nil
}

