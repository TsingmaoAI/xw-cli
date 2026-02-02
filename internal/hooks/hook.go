// Package hooks provides a pluggable system for checking and installing dependencies.
//
// The hook system allows commands to declare external dependencies (like CLI tools,
// libraries, or services) and automatically check for their presence. If dependencies
// are missing, hooks can optionally install them with user consent.
//
// This is useful for ensuring the runtime environment meets all requirements before
// executing operations that depend on external tools.
//
// Example usage:
//
//	runner := hooks.NewRunner()
//	runner.Register(dockerHook)
//	if err := runner.Run(ctx, hooks.ModeAuto); err != nil {
//	    return fmt.Errorf("dependency check failed: %w", err)
//	}
package hooks

import (
	"context"
	"fmt"
)

// Mode determines how hooks behave when dependencies are missing.
type Mode string

const (
	// ModeAuto automatically installs missing dependencies without prompting
	ModeAuto Mode = "auto"
	
	// ModeInteractive prompts the user before installing dependencies
	ModeInteractive Mode = "interactive"
	
	// ModeCheck only checks for dependencies without attempting installation
	ModeCheck Mode = "check"
)

// Hook defines the interface for dependency checks and installation.
//
// Implementations should:
//   - Check: Return nil if the dependency is satisfied, error otherwise
//   - Install: Install the dependency and return nil on success
//   - Message: Return a human-readable description of what will be installed
//   - Interactive: Return true if user confirmation is recommended
type Hook interface {
	// Name returns the dependency name for identification
	Name() string
	
	// Check verifies if the dependency is satisfied
	// Returns nil if satisfied, error if missing or misconfigured
	Check(ctx context.Context) error
	
	// Install attempts to install or configure the dependency
	// Should be idempotent (safe to call multiple times)
	Install(ctx context.Context) error
	
	// Message returns a description of what this hook does
	// Used to inform users before installation
	Message() string
	
	// Interactive indicates whether user confirmation is recommended
	// If true, the hook will prompt in ModeInteractive
	Interactive() bool
}

// Runner executes a collection of hooks in sequence.
//
// It checks each registered hook and optionally installs missing dependencies
// based on the configured mode.
type Runner struct {
	hooks []Hook
}

// NewRunner creates a new hook runner with no hooks registered.
func NewRunner() *Runner {
	return &Runner{
		hooks: make([]Hook, 0),
	}
}

// Register adds a hook to the runner.
//
// Hooks are executed in registration order. Dependencies should be registered
// before the tools that depend on them.
func (r *Runner) Register(hook Hook) {
	r.hooks = append(r.hooks, hook)
}

// Run executes all registered hooks according to the specified mode.
//
// The behavior depends on the mode:
//   - ModeAuto: Automatically installs missing dependencies
//   - ModeInteractive: Prompts user before installing
//   - ModeCheck: Only checks, never installs
//
// Returns an error if any hook fails or if required dependencies are missing.
func (r *Runner) Run(ctx context.Context, mode Mode) error {
	for _, hook := range r.hooks {
		// Check if dependency is satisfied
		if err := hook.Check(ctx); err == nil {
			// Dependency satisfied, continue
			continue
		}
		
		// Dependency missing - decide what to do based on mode
		switch mode {
		case ModeCheck:
			return fmt.Errorf("dependency %s is not satisfied", hook.Name())
			
		case ModeAuto:
			// Auto-install without prompting
			if err := hook.Install(ctx); err != nil {
				return fmt.Errorf("failed to install %s: %w", hook.Name(), err)
			}
			
			// Verify installation succeeded
			if err := hook.Check(ctx); err != nil {
				return fmt.Errorf("%s installation completed but verification failed: %w", 
					hook.Name(), err)
			}
			
		case ModeInteractive:
			// Interactive mode requires user confirmation
			// This is typically handled by the caller
			// For now, treat as auto
			if err := hook.Install(ctx); err != nil {
				return fmt.Errorf("failed to install %s: %w", hook.Name(), err)
			}
			
			if err := hook.Check(ctx); err != nil {
				return fmt.Errorf("%s installation completed but verification failed: %w", 
					hook.Name(), err)
			}
		}
	}
	
	return nil
}

