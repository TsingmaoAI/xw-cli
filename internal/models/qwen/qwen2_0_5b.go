// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_0_5B is the 0.5 billion parameter Qwen2 model
//
// This is the smallest model in the Qwen2 family, ideal for:
//   - Testing and development
//   - Resource-constrained environments
//   - Quick prototyping
//   - Educational purposes
//
// Hardware Requirements:
//   - Minimum VRAM: 2GB
//   - Can run on most devices including CPUs
//
// Backend Priority:
//   1. MindIE Docker (easiest deployment)
//   2. MindIE Native (best performance on Ascend)
//   3. vLLM Docker (universal fallback)
//   4. vLLM Native (if custom setup needed)
var Qwen2_0_5B = &models.ModelSpec{
	ID:              "qwen2-0.5b",
	SourceID:        "Qwen/Qwen2-0.5B",
	DisplayName:     "Qwen2 0.5B",
	Family:          "qwen",
	Version:         "2.0",
	Description:     "Qwen2 0.5B parameter model for testing and resource-constrained environments",
	Parameters:      0.5,
	RequiredVRAM:    2,
	ContextLength:   32768,
	EmbeddingLength: 896,
	Capabilities:    []string{"completion"},
	License:         "Apache-2.0",
	Homepage:        "https://github.com/QwenLM/Qwen2",
	Tag:             "", // Default full precision variant

	// Supported devices - works on most devices
	SupportedDevices: []api.DeviceType{
		api.DeviceTypeAscend,
		api.DeviceTypeKunlun,
	},

	// Backend priority list - Docker first for ease of use
	Backends: []models.BackendOption{
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeNative},
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeNative},
	},
}

func init() {
	models.RegisterModelSpec(Qwen2_0_5B)
}

