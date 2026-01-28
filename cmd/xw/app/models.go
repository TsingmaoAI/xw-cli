package app

// Import model packages to ensure they are registered
// This must be in the app package to avoid import cycles
import (
	_ "github.com/tsingmao/xw/internal/models/qwen" // Register Qwen models
	// Add more imports as models are added:
	// _ "github.com/tsingmao/xw/internal/models/llama"
	// _ "github.com/tsingmao/xw/internal/models/baichuan"
)

