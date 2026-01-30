// Package qwen provides Qwen model family specifications.
package qwen

import (
	"github.com/tsingmao/xw/internal/api"
	"github.com/tsingmao/xw/internal/models"
)

// Qwen2_7B is the 7 billion parameter Qwen2 model
var Qwen2_7B = &models.ModelSpec{
	// Model identification
	ID:       "qwen2-7b",
	SourceID: "Qwen/Qwen2-7B",

	// Model specifications
	Parameters:      7.0,   // 7.61B parameters
	ContextLength:   131072, // 128K context
	EmbeddingLength: 3584,

	// Supported hardware
	SupportedDevices: []api.DeviceType{
		api.DeviceTypeAscend,
	},

	// Backend configuration
	Backends: []models.BackendOption{
		{Type: models.BackendTypeVLLM, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeMindIE, Mode: models.DeploymentModeDocker},
		{Type: models.BackendTypeMLGuider, Mode: models.DeploymentModeDocker},
	},
}

func init() {
	models.RegisterModelSpec(Qwen2_7B)
}
