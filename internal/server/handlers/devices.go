// Package handlers provides HTTP request handlers for the xw server API.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/api"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// ListDevices handles GET /api/devices/list requests.
// It returns a list of all detected devices on the server machine.
//
// Response format:
//
//	{
//	  "devices": [
//	    {
//	      "id": "0000:00:0d.0",
//	      "name": "Ascend 910B",
//	      "type": "ascend-910b",
//	      "vendor": "huawei",
//	      "status": "available"
//	    }
//	  ]
//	}
func (h *Handler) ListDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logger.Debug("Listing devices on server")

	// Get detailed chip information (one entry per physical chip)
	chips, err := h.deviceManager.ListDetectedChips()
	if err != nil {
		logger.Error("Failed to list devices: %v", err)
		h.WriteError(w, "Failed to list devices", http.StatusInternalServerError)
		return
	}

	resp := api.DeviceListResponse{
		Devices: chips,
	}

	h.WriteJSON(w, resp, http.StatusOK)
}

// GetSupportedDevices handles GET /api/devices/supported requests.
// It returns a list of device types supported by the current chip configuration.
//
// Request format:
//
//	{
//	  "type": "all" // or specific device type filter
//	}
//
// Response format:
//
//	{
//	  "device_types": ["ascend-910b", "ascend-310p"]
//	}
func (h *Handler) GetSupportedDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		h.WriteError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req api.SupportedDevicesRequest
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.WriteError(w, "Invalid request body", http.StatusBadRequest)
			return
		}
	}

	logger.Debug("Getting supported device types")

	// Get supported device types from device manager configuration
	deviceTypes := h.deviceManager.GetSupportedTypes()

	// Convert []api.DeviceType to []string
	deviceTypeStrings := make([]string, len(deviceTypes))
	for i, dt := range deviceTypes {
		deviceTypeStrings[i] = string(dt)
	}

	resp := api.SupportedDevicesResponse{
		DeviceTypes: deviceTypeStrings,
	}

	h.WriteJSON(w, resp, http.StatusOK)
}

