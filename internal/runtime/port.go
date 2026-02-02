// Package runtime provides the core runtime abstraction for model lifecycle management.
package runtime

import (
	"fmt"
	"net"
	"sync"

	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// PortAllocator manages dynamic port allocation for model instances.
//
// This allocator ensures that each instance gets a unique port and handles
// port conflicts gracefully. It uses the operating system's dynamic port
// allocation mechanism to find available ports.
type PortAllocator struct {
	mu        sync.Mutex
	allocated map[int]bool // Track allocated ports
	minPort   int          // Minimum port number to allocate
	maxPort   int          // Maximum port number to allocate
}

// NewPortAllocator creates a new port allocator.
//
// The allocator will assign ports in the range [minPort, maxPort].
// By default, it uses the range [10881, 11881] for model inference services.
func NewPortAllocator(minPort, maxPort int) *PortAllocator {
	if minPort <= 0 {
		minPort = 10881
	}
	if maxPort <= minPort {
		maxPort = minPort + 1000
	}
	
	return &PortAllocator{
		allocated: make(map[int]bool),
		minPort:   minPort,
		maxPort:   maxPort,
	}
}

// GetFreePort finds and returns an available port.
//
// This method sequentially tries ports starting from minPort until
// it finds one that is available and not already allocated.
//
// Returns:
//   - Available port number
//   - Error if no ports are available
func (pa *PortAllocator) GetFreePort() (int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	
	// Try ports sequentially starting from minPort
	for p := pa.minPort; p <= pa.maxPort; p++ {
		// Skip if already allocated
		if pa.allocated[p] {
			continue
		}
		
		// Check if port is available
		if pa.isPortAvailable(p) {
			pa.allocated[p] = true
			logger.Debug("Allocated port %d", p)
			return p, nil
		}
	}
	
	return 0, fmt.Errorf("no available ports in range [%d, %d]", pa.minPort, pa.maxPort)
}

// ReleasePort marks a port as available for reuse.
//
// Parameters:
//   - port: The port number to release
func (pa *PortAllocator) ReleasePort(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	
	if pa.allocated[port] {
		delete(pa.allocated, port)
		logger.Debug("Released port %d", port)
	}
}

// MarkPortUsed marks a port as in use.
//
// This is useful when loading existing instances that already have
// ports assigned, to prevent double allocation.
//
// Parameters:
//   - port: The port number to mark as used
func (pa *PortAllocator) MarkPortUsed(port int) {
	if port <= 0 {
		return
	}
	
	pa.mu.Lock()
	defer pa.mu.Unlock()
	
	pa.allocated[port] = true
	logger.Debug("Marked port %d as used", port)
}

// isPortAvailable checks if a specific port is available.
//
// This method attempts to bind to the port temporarily to verify availability.
//
// Parameters:
//   - port: The port number to check
//
// Returns:
//   - true if the port is available, false otherwise
func (pa *PortAllocator) isPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// Global port allocator instance
var (
	globalPortAllocator     *PortAllocator
	globalPortAllocatorOnce sync.Once
)

// GetGlobalPortAllocator returns the global port allocator instance.
//
// This allocator is shared across all runtimes to prevent port conflicts.
// It uses lazy initialization to create the allocator on first access.
// Ports are allocated starting from 10881.
//
// Returns:
//   - The global PortAllocator instance
func GetGlobalPortAllocator() *PortAllocator {
	globalPortAllocatorOnce.Do(func() {
		globalPortAllocator = NewPortAllocator(10881, 11881)
		logger.Info("Initialized global port allocator (range: 10881-11881)")
	})
	return globalPortAllocator
}

