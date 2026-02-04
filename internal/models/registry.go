// Package models provides model registry and management functionality.
//
// This package manages the catalog of available AI models, including:
//   - Model metadata and versioning
//   - Device compatibility information
//   - Model registration and discovery
//   - Thread-safe access to the model registry
//
// The registry is pre-populated with models specifically optimized for
// domestic (Chinese) chip architectures. Models are curated and tested
// to ensure compatibility with supported hardware.
package models

import (
	"fmt"
	"sync"

	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/config"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// Registry manages the catalog of available AI models and their metadata.
//
// The Registry provides thread-safe access to model information, supporting
// concurrent queries from multiple clients. It maintains a map of model names
// to their detailed metadata, including device compatibility information.
//
// The registry is pre-loaded with default models at initialization and can
// be dynamically updated to add or remove models at runtime.
type Registry struct {
	// mu provides thread-safe access to the models map.
	// Uses RWMutex to allow multiple concurrent readers.
	mu sync.RWMutex

	// models maps model names to their metadata (legacy API).
	// Key: model name (e.g., "llama3-8b")
	// Value: pointer to Model struct with full metadata
	models map[string]*api.Model
	
	// specs maps model IDs to their detailed specifications.
	// This is the new model specification system.
	specs map[string]*ModelSpec
}

// NewRegistry creates and initializes a new model registry.
//
// The registry is created with an empty models map and immediately populated
// with default models through loadDefaultModels(). The default models are
// those officially supported and optimized for domestic chip architectures.
//
// Returns:
//   - A pointer to a fully initialized Registry with default models loaded.
//
// Example:
//
//	registry := model.NewRegistry()
//	models := registry.List(device.ConfigKeyAscend910B, false)
//	fmt.Printf("Found %d models for Ascend 910B\n", len(models))
func NewRegistry() *Registry {
	r := &Registry{
		models: make(map[string]*api.Model),
		specs:  make(map[string]*ModelSpec),
	}
	// Note: Models are now registered via RegisterModelSpec() in init() functions
	// No need to load hardcoded defaults here
	return r
}


// loadDefaultModels is deprecated and kept only for backwards compatibility
// with the old API model system. New models should use RegisterModelSpec()
// and be defined in their own packages (e.g., internal/model/qwen/)
func (r *Registry) loadDefaultModels() {
	// Legacy hardcoded models removed - all models now use the new ModelSpec system
	// and are registered via init() functions in model subdirectories
	// (see internal/model/qwen/qwen2.go for example)
}

// List returns models filtered by device compatibility and display options.
//
// This method provides flexible model listing with filtering capabilities:
//   - If showAll is true, returns all models regardless of device type
//   - If deviceType is DeviceTypeAll, returns all models
//   - Otherwise, returns only models supporting the specified device type
//
// The method is thread-safe and can be called concurrently from multiple
// goroutines. It returns copies of models, not references, to prevent
// external modification of the registry.
//
// Parameters:
//   - deviceType: The device type to filter by (or DeviceTypeAll for no filter)
//   - showAll: If true, ignores device type filtering
//
// Returns:
//   - A slice of Model structs matching the filter criteria.
//     Returns an empty slice if no models match.
//
// Example:
//
//	// Get all models for Kunlun devices
//	models := registry.List(api.DeviceTypeKunlun, false)
//
//	// Get all models in the registry
//	allModels := registry.List(api.DeviceTypeAll, true)
func (r *Registry) List(deviceType api.DeviceType, showAll bool) []api.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []api.Model

	for _, model := range r.models {
		// If showAll is true or deviceType is all, include the model
		if showAll || deviceType == api.DeviceTypeAll {
			result = append(result, *model)
			continue
		}

		// Check if the model supports the specified device type
		if r.supportsDevice(model, deviceType) {
			result = append(result, *model)
		}
	}

	return result
}

// Get retrieves a specific model by its name.
//
// This method performs a case-sensitive lookup of a model in the registry.
// It's commonly used to verify model existence before operations like
// pulling or running a model.
//
// The method is thread-safe and returns a pointer to the model metadata.
// Note that the returned pointer references the internal registry data,
// so callers should not modify the returned model.
//
// Parameters:
//   - name: The exact model name to look up (e.g., "llama3-8b")
//
// Returns:
//   - A pointer to the Model if found
//   - An error if the model doesn't exist in the registry
//
// Example:
//
//	model, err := registry.Get("qwen-7b")
//	if err != nil {
//	    log.Printf("Model not found: %v", err)
//	    return
//	}
//	fmt.Printf("Model version: %s\n", model.Version)
func (r *Registry) Get(name string) (*api.Model, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	model, exists := r.models[name]
	if !exists {
		return nil, fmt.Errorf("model %s not found", name)
	}

	return model, nil
}

// Register adds a new model or updates an existing model in the registry.
//
// This method allows dynamic registration of models at runtime. If a model
// with the same name already exists, it will be replaced with the new model
// data. This is useful for:
//   - Adding custom models
//   - Updating model metadata
//   - Model version upgrades
//
// The method validates that the model has a non-empty name before registration.
// It's thread-safe and can be called concurrently.
//
// Parameters:
//   - model: Pointer to the Model to register. Must have a non-empty Name field.
//
// Returns:
//   - nil if registration succeeds
//   - error if the model name is empty
//
// Example:
//
//	newModel := &api.Model{
//	    Name: "custom-model",
//	    Version: "1.0.0",
//	    SupportedDevices: []api.DeviceType{device.ConfigKeyAscend910B, device.ConfigKeyAscend310P},
//	}
//	if err := registry.Register(newModel); err != nil {
//	    log.Fatalf("Failed to register model: %v", err)
//	}
func (r *Registry) Register(model *api.Model) error {
	if model.Name == "" {
		return fmt.Errorf("model name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.models[model.Name] = model
	return nil
}

// Unregister removes a model from the registry by name.
//
// This method permanently removes a model from the registry. This is useful
// for:
//   - Removing deprecated models
//   - Cleaning up test models
//   - Managing registry size
//
// The method verifies that the model exists before attempting removal.
// It's thread-safe and can be called concurrently.
//
// Parameters:
//   - name: The name of the model to remove
//
// Returns:
//   - nil if the model was successfully removed
//   - error if the model doesn't exist in the registry
//
// Example:
//
//	if err := registry.Unregister("old-model"); err != nil {
//	    log.Printf("Model not found: %v", err)
//	}
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[name]; !exists {
		return fmt.Errorf("model %s not found", name)
	}

	delete(r.models, name)
	return nil
}

// supportsDevice checks if a model supports a specific device type.
//
// This is a helper method used internally by List() to filter models by
// device compatibility. It supports both base model keys and variant keys.
//
// Matching logic:
//   1. Direct match: If deviceType matches model.SupportedDevices directly
//   2. Base model match: If deviceType is a variant, try matching with base model key
//
// Parameters:
//   - model: The model to check
//   - deviceType: The device type to check for (may be variant_key or base config_key)
//
// Returns:
//   - true if the model supports the specified device type
//   - false otherwise
func (r *Registry) supportsDevice(model *api.Model, deviceType api.DeviceType) bool {
	// Try direct match first
	for _, dt := range model.SupportedDevices {
		if dt == deviceType {
			return true
		}
	}
	
	// If direct match fails, try to find base config_key for variants
	// Load device config to check if deviceType is a variant
	devConfig, err := config.LoadDevicesConfig()
	if err != nil {
		return false
	}
	
	// Try to find the base model for this device type
	for _, vendor := range devConfig.Vendors {
		for _, chipModel := range vendor.ChipModels {
			// Check if deviceType is a variant of this chip model
			for _, variant := range chipModel.Variants {
				if variant.VariantKey == string(deviceType) {
					// Found the variant, check if base model is supported
					baseConfigKey := api.DeviceType(chipModel.ConfigKey)
					for _, dt := range model.SupportedDevices {
						if dt == baseConfigKey {
							return true
						}
					}
					return false
				}
			}
		}
	}
	
	return false
}

// supportsAnyDevice checks if a model supports any of the given device types.
//
// Parameters:
//   - model: The model to check
//   - deviceTypes: Slice of device types to check against
//
// Returns:
//   - true if the model supports at least one of the specified device types
//   - false otherwise
func (r *Registry) supportsAnyDevice(model *api.Model, deviceTypes []api.DeviceType) bool {
	for _, dt := range deviceTypes {
		if r.supportsDevice(model, dt) {
			return true
		}
	}
	return false
}

// ListAvailableModels returns models compatible with detected devices.
//
// This method filters models to show only those that can run on at least
// one of the detected device types.
//
// Parameters:
//   - detectedDevices: Slice of device types detected on the host
//
// Returns:
//   - A slice of Model structs that support at least one detected device
func (r *Registry) ListAvailableModels(detectedDevices []api.DeviceType) []api.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []api.Model

	for _, model := range r.models {
		if r.supportsAnyDevice(model, detectedDevices) {
			result = append(result, *model)
		}
	}

	return result
}

// CountAvailableModels counts models compatible with detected devices.
//
// This method counts how many models in the registry can run on at least
// one of the detected device types.
//
// Parameters:
//   - detectedDevices: Slice of device types detected on the host
//
// Returns:
//   - The number of models that support at least one detected device
func (r *Registry) CountAvailableModels(detectedDevices []api.DeviceType) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, model := range r.models {
		if r.supportsAnyDevice(model, detectedDevices) {
			count++
		}
	}

	return count
}

// GetSpec retrieves a model specification by its ID.
//
// Parameters:
//   - modelID: The model identifier
//
// Returns:
//   - Pointer to ModelSpec if found, nil otherwise
func (r *Registry) GetSpec(modelID string) *ModelSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	if r.specs == nil {
		return nil
	}
	
	return r.specs[modelID]
}


// defaultRegistry is the package-level singleton registry instance
var defaultRegistry = NewRegistry()

// GetDefaultRegistry returns the singleton registry instance
func GetDefaultRegistry() *Registry {
	return defaultRegistry
}
// GetModelSpec retrieves a model specification by its ID or SourceID.
//
// This function supports flexible model lookup by accepting either:
//   - Internal model ID (e.g., "qwen2-0.5b")
//   - External source ID (e.g., "Qwen/Qwen2-0.5B" from ModelScope)
//
// The lookup is performed in two steps:
//  1. First, try to find by internal ID (fast path)
//  2. If not found, search all models for matching SourceID (slower, but more flexible)
//
// This dual-lookup approach allows users to reference models using either
// format, improving usability while maintaining the internal ID system.
//
// Parameters:
//   - modelID: The model identifier (internal ID or SourceID)
//
// Returns:
//   - Pointer to ModelSpec if found by either ID or SourceID, nil otherwise
//
// Example:
//
//	// Both of these will find the same model:
//	spec1 := GetModelSpec("qwen2-0.5b")           // By internal ID
//	spec2 := GetModelSpec("Qwen/Qwen2-0.5B")      // By SourceID
func GetModelSpec(modelID string) *ModelSpec {
	if defaultRegistry.specs == nil {
		return nil
	}
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	
	// First, try to find by internal ID (fast path)
	if spec, ok := defaultRegistry.specs[modelID]; ok {
		return spec
	}
	
	// If not found, search by SourceID (slow path)
	// This allows users to use ModelScope IDs directly
	for _, spec := range defaultRegistry.specs {
		if spec.SourceID == modelID {
			return spec
		}
	}
	
	return nil
}

// ListModelSpecs returns all registered model specifications
//
// Returns:
//   - Slice of all ModelSpec pointers
func ListModelSpecs() []*ModelSpec {
	if defaultRegistry.specs == nil {
		return nil
	}
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	
	specs := make([]*ModelSpec, 0, len(defaultRegistry.specs))
	for _, spec := range defaultRegistry.specs {
		specs = append(specs, spec)
	}
	return specs
}

// RegisterModelSpec registers a model specification with the global registry
//
// This function should be called from init() functions in model packages.
// It's safe to call concurrently.
//
// Parameters:
//   - spec: The model specification to register
func RegisterModelSpec(spec *ModelSpec) {
	if spec == nil {
		return
	}
	if err := spec.Validate(); err != nil {
		logger.Warn("Invalid model spec for %s: %v", spec.ID, err)
		return
	}
	
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	
	if defaultRegistry.specs == nil {
		defaultRegistry.specs = make(map[string]*ModelSpec)
	}
	defaultRegistry.specs[spec.ID] = spec
	
	// Extract device types from the map for API model
	devices := make([]api.DeviceType, 0, len(spec.SupportedDevices))
	for device := range spec.SupportedDevices {
		devices = append(devices, device)
	}
	
	// Also create legacy API model for backwards compatibility with ls command
	apiModel := &api.Model{
		Name:             spec.ID,
		Version:          spec.Tag, // Tag is the version/variant
		Size:             int64(spec.Parameters * 2 * 1000000000), // Rough estimate: params * 2 bytes * 1B
		SupportedDevices: devices,
	}
	defaultRegistry.models[spec.ID] = apiModel
	
	logger.Debug("Registered model: %s", spec.ID)
}
