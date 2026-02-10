// Package client - config.go implements configuration management operations.
//
// This file provides methods for querying and modifying server configuration
// settings such as server name and registry URL.
package client

// ConfigInfo represents the server configuration information response.
type ConfigInfo struct {
	Name          string `json:"name"`
	Registry      string `json:"registry"`
	ConfigVersion string `json:"config_version"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	ConfigDir     string `json:"config_dir"`
	DataDir       string `json:"data_dir"`
}

// ConfigSetRequest represents the request body for setting configuration.
type ConfigSetRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ConfigSetResponse represents the response for setting configuration.
type ConfigSetResponse struct {
	Message string `json:"message"`
}

// ConfigGetRequest represents the request body for getting configuration.
type ConfigGetRequest struct {
	Key string `json:"key"`
}

// ConfigGetResponse represents the response for getting configuration.
type ConfigGetResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// GetConfigInfo retrieves all server configuration settings.
//
// This method queries the server for comprehensive configuration information
// including server identity, network settings, and storage paths.
//
// Returns:
//   - A pointer to ConfigInfo with server configuration details
//   - An error if the request fails
//
// Example:
//
//	info, err := client.GetConfigInfo()
//	if err != nil {
//	    log.Fatalf("Failed to get config: %v", err)
//	}
//	fmt.Printf("Server: %s\n", info.Name)
func (c *Client) GetConfigInfo() (*ConfigInfo, error) {
	var resp ConfigInfo
	if err := c.doRequest("GET", "/api/config/info", nil, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// GetConfigValue retrieves a specific configuration value.
//
// This method queries the server for the value of a specific configuration key.
//
// Parameters:
//   - key: The configuration key to retrieve (e.g., "name", "registry")
//
// Returns:
//   - The configuration value as a string
//   - An error if the request fails or the key is not found
//
// Example:
//
//	value, err := client.GetConfigValue("name")
//	if err != nil {
//	    log.Fatalf("Failed to get config value: %v", err)
//	}
//	fmt.Println(value)
func (c *Client) GetConfigValue(key string) (string, error) {
	req := ConfigGetRequest{Key: key}
	var resp ConfigGetResponse
	if err := c.doRequest("POST", "/api/config/get", req, &resp); err != nil {
		return "", err
	}

	return resp.Value, nil
}

// SetConfigValue sets a specific configuration value.
//
// This method updates a configuration value on the server. The change is
// immediately persisted to disk.
//
// Parameters:
//   - key: The configuration key to set (e.g., "name", "registry")
//   - value: The new value for the configuration key
//
// Returns:
//   - An error if the request fails or validation fails
//
// Example:
//
//	err := client.SetConfigValue("name", "xw-prod-01")
//	if err != nil {
//	    log.Fatalf("Failed to set config: %v", err)
//	}
func (c *Client) SetConfigValue(key, value string) error {
	req := ConfigSetRequest{
		Key:   key,
		Value: value,
	}
	var resp ConfigSetResponse
	if err := c.doRequest("POST", "/api/config/set", req, &resp); err != nil {
		return err
	}

	return nil
}

