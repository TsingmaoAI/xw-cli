// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_72B is the 72 billion parameter Qwen2 model
var Qwen2_72B = &models.ModelSpec{
	// Model identification
	ID:       "qwen2-72b",
	SourceID: "Qwen/Qwen2-72B",

	// Model specifications
	Parameters:      72.0,   // 72.7B parameters
	ContextLength:   131072, // 128K context
	EmbeddingLength: 8192,

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
	models.RegisterModelSpec(Qwen2_72B)
}
