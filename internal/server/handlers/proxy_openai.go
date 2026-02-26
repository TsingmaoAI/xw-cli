package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// ---------------------------------------------------------------------------
// ProxyHandler — OpenAI-compatible transparent proxy
// ---------------------------------------------------------------------------

// ProxyHandler handles proxying OpenAI-compatible API requests to inference
// service instances. It operates at the HTTP protocol level, forwarding
// requests and responses without parsing or modifying message content,
// which minimises overhead for latency-sensitive inference workloads.
type ProxyHandler struct {
	*ProxyCore
}

// NewProxyHandler creates a new OpenAI-compatible proxy handler.
// The handler owns a ProxyCore that can be shared with other API-format
// handlers (e.g. AnthropicHandler) via the exported ProxyCore field.
func NewProxyHandler(h *Handler) *ProxyHandler {
	return &ProxyHandler{
		ProxyCore: newProxyCore(h),
	}
}

// minimalRequest extracts only the fields needed for routing and stream
// detection, avoiding full request body parsing.
type minimalRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream,omitempty"`
}

// ProxyRequest handles proxying an OpenAI-compatible request to an inference service.
//
// Supported endpoints:
//   - POST /v1/chat/completions — Chat completions (streaming/non-streaming)
//   - POST /v1/completions      — Text completions (streaming/non-streaming)
//   - POST /v1/embeddings       — Embeddings (non-streaming only)
//
// The proxy preserves HTTP semantics including request/response headers,
// status codes, and streaming vs buffered transfer modes.
func (p *ProxyHandler) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.Error(w, "Invalid API path. Expected OpenAI-compatible format: /v1/{endpoint}", http.StatusBadRequest)
		return
	}

	logger.Debug("Proxying OpenAI API request: %s %s", r.Method, r.URL.Path)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var minReq minimalRequest
	if len(bodyBytes) > 0 {
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&minReq); err != nil {
			logger.Error("Failed to parse request body: %v", err)
			http.Error(w, "Invalid request body: must be valid JSON", http.StatusBadRequest)
			return
		}
	}

	if minReq.Model == "" {
		http.Error(w, "Missing required field: model", http.StatusBadRequest)
		return
	}

	logger.Debug("Request model: %s, streaming: %v", minReq.Model, minReq.Stream)

	instance, err := p.FindInstanceByModel(r.Context(), minReq.Model)
	if err != nil {
		logger.Error("No running instance found for model %s: %v", minReq.Model, err)
		http.Error(w, fmt.Sprintf("No running instance found for model: %s", minReq.Model), http.StatusNotFound)
		return
	}

	if instance.State != "running" {
		logger.Warn("Instance %s is not running (state: %s)", instance.ID, instance.State)
		http.Error(w, fmt.Sprintf("Model instance is not running (state: %s)", instance.State), http.StatusServiceUnavailable)
		return
	}

	logger.Debug("Routing to instance %s on port %d", instance.ID, instance.Port)

	release, err := p.AcquireConcurrency(r.Context(), instance)
	if err != nil {
		logger.Warn("Failed to acquire concurrency slot for instance %s: %v", instance.ID, err)
		http.Error(w, "Service temporarily unavailable (concurrency limit reached)", http.StatusServiceUnavailable)
		return
	}
	if release != nil {
		defer release()
	}

	resp, err := p.ForwardRequest(r.Context(), r.Method, r.URL.Path, r.URL.RawQuery, bodyBytes, r.Header, instance)
	if err != nil {
		logger.Error("Proxy request failed: %v", err)
		http.Error(w, fmt.Sprintf("Failed to forward request: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(resp.Header, w.Header())
	w.WriteHeader(resp.StatusCode)

	if minReq.Stream {
		handleOpenAIStreamingResponse(w, resp.Body)
	} else {
		handleOpenAIBufferedResponse(w, resp.Body)
	}

	logger.Debug("Proxy request completed successfully for instance: %s", instance.ID)
}

// handleOpenAIStreamingResponse forwards an OpenAI SSE stream to the client
// with immediate flushing after each chunk for low-latency delivery.
func handleOpenAIStreamingResponse(w http.ResponseWriter, body io.ReadCloser) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("Response writer does not support flushing")
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	reader := bufio.NewReader(body)
	buf := make([]byte, 4096)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				logger.Debug("Client disconnected during streaming: %v", writeErr)
				return
			}
			flusher.Flush()
		}
		if err != nil {
			if err == io.EOF {
				logger.Debug("Stream completed successfully")
			} else {
				logger.Debug("Stream interrupted: %v", err)
			}
			return
		}
	}
}

// handleOpenAIBufferedResponse copies the entire response body to the client
// in a single pass. Used for non-streaming endpoints such as embeddings.
func handleOpenAIBufferedResponse(w http.ResponseWriter, body io.ReadCloser) {
	written, err := io.Copy(w, body)
	if err != nil {
		logger.Error("Failed to write response body: %v", err)
		return
	}
	logger.Debug("Wrote %d bytes in buffered response", written)
}

// HealthCheck provides a health check endpoint for the proxy service.
// It returns a JSON response with the service status and current timestamp.
func (p *ProxyHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"service":   "xw-proxy",
	}

	json.NewEncoder(w).Encode(response)
}
