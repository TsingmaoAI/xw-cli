// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_0_5B is the 0.5 billion parameter Qwen2 model
var Qwen2_0_5B = &models.ModelSpec{
	// Model identification
	ID:       "qwen2-0.5b",
	SourceID: "Qwen/Qwen2-0.5B",

	// Model specifications
	Parameters:      0.5,  // 494M parameters
	ContextLength:   32768,
	EmbeddingLength: 896,

	// Supported hardware
	SupportedDevices: []api.DeviceType{
		api.DeviceTypeAscend,
	},

	// Backend configuration
	Backends: []models.BackendOption{
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeDocker},
	},
}

func init() {
	models.RegisterModelSpec(Qwen2_0_5B)
}

