// Package client - devices.go implements device management operations.
//
// This file provides methods for querying and managing AI accelerator devices
// available on the server. These operations help discover device capabilities
// and validate hardware compatibility.
package client

import (
	"github.com/tsingmaoai/xw-cli/internal/api"
)

// DeviceInfo represents device information returned from the server.
type DeviceInfo struct {
	VendorID            string   `json:"vendor_id"`
	DeviceID            string   `json:"device_id"`
	BusAddress          string   `json:"bus_address"`
	ModelName           string   `json:"model_name"`
	ConfigKey           string   `json:"config_key"`
	DeviceType          string   `json:"device_type"`
	Generation          string   `json:"generation"`
	Capabilities        []string `json:"capabilities"`
	PhysicalDeviceIndex int      `json:"physical_device_index"`
	ChipIndex           int      `json:"chip_index"`
	ChipsPerDevice      int      `json:"chips_per_device"`
}

// ListDevices retrieves a list of devices detected on the server machine.
//
// This method queries the server for all AI accelerator devices available
// on the server hardware. It's used by the CLI to display device information.
//
// Returns:
//   - A slice of DeviceInfo structs representing detected hardware
//   - An error if the request fails or the server returns an error
//
// Example:
//
//	devices, err := client.ListDevices()
//	if err != nil {
//	    log.Fatalf("Failed to list devices: %v", err)
//	}
//	for _, device := range devices {
//	    fmt.Printf("%s: %s (available=%v)\n", device.Type, device.Name, device.Available)
//	}
func (c *Client) ListDevices() ([]DeviceInfo, error) {
	var resp struct {
		Devices []DeviceInfo `json:"devices"`
	}
	if err := c.doRequest("GET", "/api/devices/list", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// GetSupportedDevices retrieves the list of device types supported by the server.
//
// This method queries the server for device types that are configured and
// supported based on the server's configuration files. It's used to filter
// models and validate device compatibility.
//
// Returns:
//   - A slice of device type strings (e.g., ["ascend-910b", "ascend-310p"])
//   - An error if the request fails or the server returns an error
//
// Example:
//
//	deviceTypes, err := client.GetSupportedDevices()
//	if err != nil {
//	    log.Fatalf("Failed to get supported devices: %v", err)
//	}
//	for _, dt := range deviceTypes {
//	    fmt.Printf("Supported: %s\n", dt)
//	}
func (c *Client) GetSupportedDevices() ([]string, error) {
	var resp api.SupportedDevicesResponse
	if err := c.doRequest("GET", "/api/devices/supported", nil, &resp); err != nil {
		return nil, err
	}
	return resp.DeviceTypes, nil
}

