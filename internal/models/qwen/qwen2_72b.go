// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_72B is the 72 billion parameter Qwen2 model
//
// This is a much larger model offering superior performance for complex
// reasoning and generation tasks, but requiring significantly more resources.
//
// Hardware Requirements:
//   - Minimum VRAM: 144GB (multiple GPUs may be required)
//   - Recommended: 8x Ascend 910 or equivalent
//
// Backend Priority:
//   Same as 7B model, but with multi-device support
var Qwen2_72B = &models.ModelSpec{
	ID:              "qwen2-72b",
	SourceID:        "Qwen/Qwen2-72B",
	DisplayName:     "Qwen2 72B",
	Family:          "qwen",
	Version:         "2.0",
	Description:     "Qwen2 72B parameter model for advanced reasoning and generation",
	Parameters:      72.0,
	RequiredVRAM:    144,
	ContextLength:   32768,
	EmbeddingLength: 8192,
	Capabilities:    []string{"completion"},
	License:         "Apache-2.0",
	Homepage:        "https://github.com/QwenLM/Qwen2",
	Tag:             "", // Default full precision variant

	SupportedDevices: []api.DeviceType{
		api.DeviceTypeAscend,
		api.DeviceTypeKunlun,
	},

	Backends: []models.BackendOption{
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeNative},
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeNative},
	},
}

func init() {
	models.RegisterModelSpec(Qwen2_72B)
}

