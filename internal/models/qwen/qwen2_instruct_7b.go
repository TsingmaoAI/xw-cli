// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_Instruct_7B is the instruction-tuned version of Qwen2 7B
var Qwen2_Instruct_7B = &models.ModelSpec{
	// Model identification
	ID:       "qwen2.5-7b-instruct",
	SourceID: "Qwen/Qwen2.5-7B-Instruct",

	// Model specifications
	Parameters:      7.6,   // 7.61B parameters
	ContextLength:   131072, // 128K context
	EmbeddingLength: 3584,

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
	models.RegisterModelSpec(Qwen2_Instruct_7B)
}
