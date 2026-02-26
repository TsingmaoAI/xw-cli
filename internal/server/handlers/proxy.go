// Package handlers implements HTTP request handlers for the xw server.
//
// This file provides the shared proxy infrastructure used by both OpenAI and
// Anthropic-compatible API handlers. It includes:
//   - ProxyCore: instance lookup, concurrency management, and HTTP forwarding
//   - concurrencyManager: semaphore-based per-instance request limiting
//   - Header filtering utilities for hop-by-hop header removal
//
// API-format-specific handlers are in separate files:
//   - proxy_openai.go:    OpenAI-compatible transparent proxy
//   - proxy_anthropic.go: Anthropic Messages API format-converting proxy
package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/runtime"
)

// ---------------------------------------------------------------------------
// Concurrency management
// ---------------------------------------------------------------------------

// concurrencyManager manages concurrent request limits for each model instance.
//
// Each instance has a semaphore based on its max_concurrent metadata value,
// which determines how many concurrent requests it can handle efficiently.
type concurrencyManager struct {
	mu         sync.RWMutex
	semaphores map[string]chan struct{} // instanceID → semaphore channel
}

// newConcurrencyManager creates a concurrency manager with an empty semaphore map.
func newConcurrencyManager() *concurrencyManager {
	return &concurrencyManager{
		semaphores: make(map[string]chan struct{}),
	}
}

// acquireSlot acquires a concurrency slot for the given instance.
// It blocks until a slot is available or the context is cancelled.
// The returned function must be called to release the slot.
func (cm *concurrencyManager) acquireSlot(ctx context.Context, instanceID string, maxConcurrency int) (func(), error) {
	cm.mu.Lock()
	sem, exists := cm.semaphores[instanceID]
	if !exists {
		sem = make(chan struct{}, maxConcurrency)
		cm.semaphores[instanceID] = sem
		logger.Debug("Created concurrency semaphore for instance %s (max: %d)", instanceID, maxConcurrency)
	}
	cm.mu.Unlock()

	select {
	case sem <- struct{}{}:
		logger.Debug("Acquired concurrency slot for instance %s", instanceID)
		return func() {
			<-sem
			logger.Debug("Released concurrency slot for instance %s", instanceID)
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("request cancelled while waiting for concurrency slot: %w", ctx.Err())
	}
}

// cleanupInstance removes the semaphore for a stopped instance.
func (cm *concurrencyManager) cleanupInstance(instanceID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if sem, exists := cm.semaphores[instanceID]; exists {
		close(sem)
		delete(cm.semaphores, instanceID)
		logger.Debug("Cleaned up concurrency semaphore for instance %s", instanceID)
	}
}

// ---------------------------------------------------------------------------
// ProxyCore — shared proxy infrastructure
// ---------------------------------------------------------------------------

// ProxyCore contains the shared infrastructure for proxying requests to
// inference engine instances. Both the OpenAI-compatible and Anthropic-compatible
// handlers embed this core to share instance lookup, concurrency management,
// and HTTP forwarding logic.
type ProxyCore struct {
	handler        *Handler
	concurrencyMgr *concurrencyManager
}

// newProxyCore creates a new ProxyCore instance.
func newProxyCore(h *Handler) *ProxyCore {
	return &ProxyCore{
		handler:        h,
		concurrencyMgr: newConcurrencyManager(),
	}
}

// FindInstanceByModel finds a running instance that serves the specified model.
//
// The lookup performs two passes:
//  1. Exact match on alias (or ModelID as fallback), case-insensitive
//  2. Prefix match for partial model names (e.g., "qwen2-7b" matches "qwen2-7b-instruct")
func (pc *ProxyCore) FindInstanceByModel(ctx context.Context, modelName string) (*runtime.Instance, error) {
	instances, err := pc.handler.runtimeManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	modelNameLower := strings.ToLower(modelName)

	// Pass 1: exact alias match.
	for _, inst := range instances {
		if inst.State != "running" {
			continue
		}
		alias := inst.Alias
		if alias == "" {
			alias = inst.ModelID
		}
		if strings.ToLower(alias) == modelNameLower {
			logger.Debug("Found exact alias match: instance %s (alias: %s) for model %s", inst.ID, alias, modelName)
			return inst, nil
		}
	}

	// Pass 2: prefix match.
	for _, inst := range instances {
		if inst.State != "running" {
			continue
		}
		alias := inst.Alias
		if alias == "" {
			alias = inst.ModelID
		}
		aliasLower := strings.ToLower(alias)
		if strings.HasPrefix(aliasLower, modelNameLower) || strings.HasPrefix(modelNameLower, aliasLower) {
			logger.Debug("Found prefix match: instance %s (alias: %s) for model %s", inst.ID, alias, modelName)
			return inst, nil
		}
	}

	return nil, fmt.Errorf("no running instance found for model: %s", modelName)
}

// AcquireConcurrency acquires a concurrency slot for the instance if
// max_concurrent is set in its metadata. Returns a release function (may be nil
// if concurrency control is not enabled) and an error.
func (pc *ProxyCore) AcquireConcurrency(ctx context.Context, instance *runtime.Instance) (release func(), err error) {
	maxConcurrency := 0
	if v, ok := instance.Metadata["max_concurrent"]; ok && v != "" {
		if mc, parseErr := strconv.Atoi(v); parseErr == nil && mc > 0 {
			maxConcurrency = mc
		}
	}

	if maxConcurrency <= 0 {
		logger.Debug("Processing request for instance %s (unlimited concurrency)", instance.ID)
		return nil, nil
	}

	slot, err := pc.concurrencyMgr.acquireSlot(ctx, instance.ID, maxConcurrency)
	if err != nil {
		return nil, err
	}
	logger.Debug("Processing request for instance %s (max concurrent: %d)", instance.ID, maxConcurrency)
	return slot, nil
}

// ForwardRequest sends an HTTP request to the given instance and returns the
// raw response. The caller owns the response body and must close it.
//
// Parameters:
//   - ctx: request context for cancellation
//   - method: HTTP method (typically POST)
//   - path: URL path to forward (e.g., "/v1/chat/completions")
//   - query: raw URL query string (may be empty)
//   - body: request body bytes
//   - srcHeaders: original request headers to copy (hop-by-hop headers are filtered)
//   - instance: target inference engine instance
func (pc *ProxyCore) ForwardRequest(ctx context.Context, method, path, query string, body []byte, srcHeaders http.Header, instance *runtime.Instance) (*http.Response, error) {
	targetURL := fmt.Sprintf("http://localhost:%d%s", instance.Port, path)
	if query != "" {
		targetURL += "?" + query
	}

	logger.Debug("Forwarding to: %s", targetURL)

	proxyReq, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating proxy request: %w", err)
	}

	copyHeaders(srcHeaders, proxyReq.Header)

	if proxyReq.Header.Get("Content-Type") == "" && len(body) > 0 {
		proxyReq.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return client.Do(proxyReq)
}

// ---------------------------------------------------------------------------
// HTTP header utilities
// ---------------------------------------------------------------------------

// hopByHopHeaders are HTTP headers that must not be forwarded by proxies
// per RFC 2616 §13.5.1.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// copyHeaders copies request headers from src to dst, filtering out
// hop-by-hop headers that must not be forwarded.
func copyHeaders(src, dst http.Header) {
	for name, values := range src {
		if hopByHopHeaders[name] {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}

// copyResponseHeaders copies response headers from src to dst, filtering
// out hop-by-hop headers that must not be forwarded.
func copyResponseHeaders(src, dst http.Header) {
	for name, values := range src {
		if hopByHopHeaders[name] {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}
