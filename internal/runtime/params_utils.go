package runtime

import (
	"strings"
	
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// convertTemplateParamsToEnv converts template parameters to environment variables.
//
// Template parameters are in "key=value" format. This function:
//  1. Splits each parameter into key and value
//  2. Converts key to uppercase environment variable format:
//     - camelCase -> CAMEL_CASE
//     - kebab-case -> KEBAB_CASE
//  3. Returns a map of environment variables
//
// Parameters:
//   - templateParams: List of "key=value" parameters
//
// Returns:
//   - Map of environment variables (KEY=value)
//
// Example:
//   Input: ["tensorParallel=4", "max-batch-size=128"]
//   Output: {"TENSOR_PARALLEL": "4", "MAX_BATCH_SIZE": "128"}
func convertTemplateParamsToEnv(templateParams []string) map[string]string {
	env := make(map[string]string)
	
	for _, param := range templateParams {
		// Split on first '=' to separate key and value
		parts := strings.SplitN(param, "=", 2)
		if len(parts) != 2 {
			logger.Warn("Invalid template parameter format (expected key=value): %s", param)
			continue
		}
		
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		
		if key == "" {
			logger.Warn("Empty key in template parameter: %s", param)
			continue
		}
		
		// Convert key to environment variable format
		envKey := convertToEnvVarName(key)
		env[envKey] = value
		
		logger.Debug("Template param: %s=%s -> %s=%s", key, value, envKey, value)
	}
	
	return env
}

// convertToEnvVarName converts a parameter key to environment variable format.
//
// Conversion rules:
//  1. camelCase -> snake_case (tensorParallel -> tensor_parallel)
//  2. kebab-case -> snake_case (tensor-parallel -> tensor_parallel)
//  3. Convert to uppercase (tensor_parallel -> TENSOR_PARALLEL)
//
// Parameters:
//   - key: Parameter key (e.g., "tensorParallel", "tensor-parallel")
//
// Returns:
//   - Environment variable name (e.g., "TENSOR_PARALLEL")
func convertToEnvVarName(key string) string {
	var result strings.Builder
	
	for i, ch := range key {
		if ch == '-' {
			// Replace hyphens with underscores
			result.WriteRune('_')
		} else if ch >= 'A' && ch <= 'Z' {
			// Insert underscore before uppercase letters (except at start)
			if i > 0 && key[i-1] != '-' && key[i-1] != '_' {
				result.WriteRune('_')
			}
			result.WriteRune(ch)
		} else {
			result.WriteRune(ch)
		}
	}
	
	// Convert to uppercase
	return strings.ToUpper(result.String())
}

