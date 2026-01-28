// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_7B is the 7 billion parameter Qwen2 model
//
// This model offers a good balance between performance and resource usage,
// suitable for most general-purpose tasks including chat, question answering,
// and content generation.
//
// Hardware Requirements:
//   - Minimum VRAM: 16GB
//   - Recommended: Ascend 910 or equivalent
//
// Backend Priority:
//   1. MindIE Docker (easiest deployment)
//   2. MindIE Native (best performance on Ascend)
//   3. vLLM Docker (universal fallback)
//   4. vLLM Native (if custom setup needed)
var Qwen2_7B = &models.ModelSpec{
	ID:              "qwen2-7b",
	SourceID:        "Qwen/Qwen2-7B",
	DisplayName:     "Qwen2 7B",
	Family:          "qwen",
	Version:         "2.0",
	Description:     "Qwen2 7B parameter model for general-purpose tasks",
	Parameters:      7.0,
	RequiredVRAM:    16,
	ContextLength:   32768,
	EmbeddingLength: 3584,
	Capabilities:    []string{"completion"},
	License:         "Apache-2.0",
	Homepage:        "https://github.com/QwenLM/Qwen2",
	Tag:             "", // Default full precision variant

	// Supported devices - prioritized for Ascend
	SupportedDevices: []api.DeviceType{
		api.DeviceTypeAscend,
		api.DeviceTypeKunlun,
	},

	// Backend priority list - Docker first for ease of use
	Backends: []models.BackendOption{
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeNative},
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeNative},
	},
}

func init() {
	models.RegisterModelSpec(Qwen2_7B)
}

