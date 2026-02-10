// Package handlers provides HTTP request handlers for the xw server API.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// ConfigInfoResponse represents the response structure for the config info endpoint.
//
// This response provides a comprehensive view of the current server configuration,
// including both runtime and persisted settings.
type ConfigInfoResponse struct {
	// Name is the unique identifier for this server instance.
	Name string `json:"name"`

	// Registry is the URL to the configuration package registry.
	Registry string `json:"registry"`

	// ConfigVersion is the currently active configuration version.
	ConfigVersion string `json:"config_version"`

	// Host is the server host address.
	Host string `json:"host"`

	// Port is the server port number.
	Port int `json:"port"`

	// ConfigDir is the path to the configuration directory.
	ConfigDir string `json:"config_dir"`

	// DataDir is the path to the data directory.
	DataDir string `json:"data_dir"`
}

// ConfigSetRequest represents the request body for setting configuration values.
//
// This request allows clients to update specific configuration fields.
// Only the provided fields will be updated; nil/empty values are ignored.
type ConfigSetRequest struct {
	// Key is the configuration key to set (e.g., "name", "registry").
	Key string `json:"key"`

	// Value is the new value for the configuration key.
	Value string `json:"value"`
}

// ConfigGetRequest represents the request body for getting a configuration value.
//
// This request allows clients to query specific configuration fields.
type ConfigGetRequest struct {
	// Key is the configuration key to retrieve (e.g., "name", "registry").
	Key string `json:"key"`
}

// ConfigGetResponse represents the response for getting a configuration value.
//
// This response returns the requested configuration value.
type ConfigGetResponse struct {
	// Key is the configuration key that was requested.
	Key string `json:"key"`

	// Value is the current value of the configuration key.
	Value string `json:"value"`
}

// ConfigInfo handles GET /api/config/info requests.
//
// This endpoint returns comprehensive information about the current server
// configuration, including server identity, registry settings, network
// configuration, and storage paths.
//
// HTTP Method: GET
// Path: /api/config/info
//
// Response: 200 OK
//
//	{
//	  "name": "xw-a3f5b2c1",
//	  "registry": "https://xw.tsingmao.com/packages.json",
//	  "host": "localhost",
//	  "port": 11581,
//	  "config_dir": "/home/user/.xw",
//	  "data_dir": "/home/user/.xw/data"
//	}
//
// Error Responses:
//   - 500 Internal Server Error: Failed to serialize configuration
//
// Example:
//
//	curl http://localhost:11581/api/config/info
func (h *Handler) ConfigInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.WriteError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get current config version
	identity, err := h.config.GetOrCreateServerIdentity()
	if err != nil {
		logger.Error("Failed to get server identity: %v", err)
		h.WriteError(w, "failed to get server identity", http.StatusInternalServerError)
		return
	}

	response := ConfigInfoResponse{
		Name:          h.config.Server.Name,
		Registry:      h.config.Server.Registry,
		ConfigVersion: identity.ConfigVersion,
		Host:          h.config.Server.Host,
		Port:          h.config.Server.Port,
		ConfigDir:     h.config.Storage.ConfigDir,
		DataDir:       h.config.Storage.DataDir,
	}

	h.WriteJSON(w, response, http.StatusOK)
}

// ConfigSet handles POST /api/config/set requests.
//
// This endpoint allows clients to update specific configuration values.
// The changes are immediately persisted to disk in the server.conf file.
//
// Currently supported configuration keys:
//   - "name": Server instance identifier
//   - "registry": Configuration package registry URL
//
// HTTP Method: POST
// Path: /api/config/set
// Content-Type: application/json
//
// Request Body:
//
//	{
//	  "key": "name",
//	  "value": "xw-prod-01"
//	}
//
// Response: 200 OK
//
//	{
//	  "message": "Configuration updated successfully"
//	}
//
// Error Responses:
//   - 400 Bad Request: Invalid request body or missing required fields
//   - 400 Bad Request: Unsupported configuration key
//   - 400 Bad Request: Invalid value for the specified key
//   - 500 Internal Server Error: Failed to save configuration
//
// Example:
//
//	curl -X POST http://localhost:11581/api/config/set \
//	  -H "Content-Type: application/json" \
//	  -d '{"key":"name","value":"xw-prod-01"}'
func (h *Handler) ConfigSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.WriteError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Key == "" {
		h.WriteError(w, "key is required", http.StatusBadRequest)
		return
	}
	if req.Value == "" {
		h.WriteError(w, "value is required", http.StatusBadRequest)
		return
	}

	// Update configuration based on key
	switch req.Key {
	case "name":
		h.WriteError(w, "server name cannot be modified (it's tied to running instances)", http.StatusBadRequest)
		return

	case "registry":
		h.config.Server.Registry = req.Value
		logger.Info("Registry URL updated to: %s", req.Value)

	default:
		h.WriteError(w, fmt.Sprintf("unsupported configuration key: %s", req.Key), http.StatusBadRequest)
		return
	}

	// Persist configuration to disk
	if err := h.config.SaveServerConfig(); err != nil {
		logger.Error("Failed to save server configuration: %v", err)
		h.WriteError(w, fmt.Sprintf("failed to save configuration: %v", err), http.StatusInternalServerError)
		return
	}

	h.WriteJSON(w, map[string]string{
		"message": "Configuration updated successfully",
	}, http.StatusOK)
}

// ConfigGet handles POST /api/config/get requests.
//
// This endpoint allows clients to query specific configuration values.
// It returns the current value of the requested configuration key.
//
// Currently supported configuration keys:
//   - "name": Server instance identifier
//   - "registry": Configuration package registry URL
//   - "host": Server host address
//   - "port": Server port number
//   - "config_dir": Configuration directory path
//   - "data_dir": Data directory path
//
// HTTP Method: POST
// Path: /api/config/get
// Content-Type: application/json
//
// Request Body:
//
//	{
//	  "key": "name"
//	}
//
// Response: 200 OK
//
//	{
//	  "key": "name",
//	  "value": "xw-prod-01"
//	}
//
// Error Responses:
//   - 400 Bad Request: Invalid request body or missing required fields
//   - 400 Bad Request: Unsupported configuration key
//
// Example:
//
//	curl -X POST http://localhost:11581/api/config/get \
//	  -H "Content-Type: application/json" \
//	  -d '{"key":"name"}'
func (h *Handler) ConfigGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.WriteError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ConfigGetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.WriteError(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.Key == "" {
		h.WriteError(w, "key is required", http.StatusBadRequest)
		return
	}

	// Get configuration value based on key
	var value string
	switch req.Key {
	case "name":
		value = h.config.Server.Name

	case "registry":
		value = h.config.Server.Registry

	case "host":
		value = h.config.Server.Host

	case "port":
		value = fmt.Sprintf("%d", h.config.Server.Port)

	case "config_dir":
		value = h.config.Storage.ConfigDir

	case "data_dir":
		value = h.config.Storage.DataDir

	default:
		h.WriteError(w, fmt.Sprintf("unsupported configuration key: %s", req.Key), http.StatusBadRequest)
		return
	}

	response := ConfigGetResponse{
		Key:   req.Key,
		Value: value,
	}

	h.WriteJSON(w, response, http.StatusOK)
}

