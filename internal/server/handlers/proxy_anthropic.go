package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/tsingmaoai/xw-cli/internal/apiformat"
	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// AnthropicHandler proxies Anthropic Messages API requests to OpenAI-compatible
// inference backends, performing bidirectional format translation.
//
// This enables clients that speak the Anthropic API (such as Claude Code) to
// interact with locally running inference engines (vLLM, MindIE, etc.) that
// expose OpenAI-compatible endpoints.
//
// Request flow:
//
//	Client → POST /v1/messages (Anthropic format)
//	  → Parse and validate the Anthropic request
//	  → Convert to OpenAI chat completion format
//	  → Forward to the matching backend instance
//	  → Convert the OpenAI response back to Anthropic format
//	  → Return to client
//
// Both streaming (SSE) and non-streaming responses are supported.
type AnthropicHandler struct {
	*ProxyCore
}

// NewAnthropicHandler creates a new Anthropic API proxy handler.
// It shares the same ProxyCore infrastructure (instance lookup, concurrency
// management, HTTP forwarding) with the OpenAI proxy handler.
func NewAnthropicHandler(core *ProxyCore) *AnthropicHandler {
	return &AnthropicHandler{ProxyCore: core}
}

// HandleMessages handles POST /v1/messages requests.
//
// This is the primary endpoint for the Anthropic Messages API. It accepts
// requests in Anthropic format, translates them to OpenAI chat completion
// requests, forwards them to the appropriate backend instance, and translates
// the response back to Anthropic format.
func (ah *AnthropicHandler) HandleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ah.writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Only POST method is allowed")
		return
	}

	// Read and parse the Anthropic request body.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read Anthropic request body: %v", err)
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	var req apiformat.MessagesRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		logger.Error("Failed to parse Anthropic request: %v", err)
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	if req.Model == "" {
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Missing required field: model")
		return
	}
	if req.MaxTokens <= 0 {
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "max_tokens must be a positive integer")
		return
	}
	if len(req.Messages) == 0 {
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "messages must not be empty")
		return
	}

	logger.Debug("Anthropic API request: model=%s, stream=%v, messages=%d", req.Model, req.Stream, len(req.Messages))

	// Find the backend instance matching the requested model.
	instance, err := ah.FindInstanceByModel(r.Context(), req.Model)
	if err != nil {
		logger.Error("No running instance found for model %s: %v", req.Model, err)
		ah.writeAnthropicError(w, http.StatusNotFound, "not_found_error",
			fmt.Sprintf("No running instance found for model: %s", req.Model))
		return
	}

	if instance.State != "running" {
		ah.writeAnthropicError(w, http.StatusServiceUnavailable, "api_error",
			fmt.Sprintf("Model instance is not running (state: %s)", instance.State))
		return
	}

	// Acquire a concurrency slot if the instance has limits configured.
	release, err := ah.AcquireConcurrency(r.Context(), instance)
	if err != nil {
		logger.Warn("Concurrency limit reached for instance %s: %v", instance.ID, err)
		ah.writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error",
			"Service temporarily unavailable (concurrency limit reached)")
		return
	}
	if release != nil {
		defer release()
	}

	// Determine the model name the backend expects. Use the instance alias
	// (which matches what the inference engine loaded) rather than the
	// client's model name, which may be a Claude-style name.
	backendModel := instance.Alias
	if backendModel == "" {
		backendModel = instance.ModelID
	}

	// Convert the Anthropic request to OpenAI format.
	openaiBody, err := apiformat.ConvertRequest(&req, backendModel)
	if err != nil {
		logger.Error("Failed to convert Anthropic request to OpenAI format: %v", err)
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Failed to convert request: %v", err))
		return
	}

	logger.Debug("Forwarding to instance %s (port %d) as OpenAI request", instance.ID, instance.Port)

	// Forward the converted request to the backend's chat completions endpoint.
	resp, err := ah.ForwardRequest(
		r.Context(),
		http.MethodPost,
		"/v1/chat/completions",
		"",
		openaiBody,
		r.Header,
		instance,
	)
	if err != nil {
		logger.Error("Backend request failed: %v", err)
		ah.writeAnthropicError(w, http.StatusBadGateway, "api_error",
			fmt.Sprintf("Failed to forward request to backend: %v", err))
		return
	}
	defer resp.Body.Close()

	// Check for backend errors.
	if resp.StatusCode >= 400 {
		ah.forwardBackendError(w, resp)
		return
	}

	if req.Stream {
		ah.handleStreamingResponse(w, resp, req.Model)
	} else {
		ah.handleBufferedResponse(w, resp, req.Model)
	}
}

// HandleCountTokens handles POST /v1/messages/count_tokens requests.
//
// This endpoint estimates the token count for a given set of messages.
// Since the backend inference engines may not have a dedicated token counting
// endpoint, we return a reasonable estimate based on message length.
func (ah *AnthropicHandler) HandleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ah.writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Only POST method is allowed")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	defer r.Body.Close()

	var req apiformat.TokenCountRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		ah.writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	// Approximate token count: ~4 characters per token is a reasonable heuristic
	// for most tokenizers. This avoids requiring a tokenizer dependency.
	charCount := len(bodyBytes)
	estimatedTokens := charCount / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}

	logger.Debug("Token count estimate for model %s: ~%d tokens (%d chars)", req.Model, estimatedTokens, charCount)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(apiformat.TokenCountResponse{
		InputTokens: estimatedTokens,
	})
}

// handleStreamingResponse converts an OpenAI SSE stream to Anthropic SSE format.
func (ah *AnthropicHandler) handleStreamingResponse(w http.ResponseWriter, resp *http.Response, requestModel string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		logger.Error("Response writer does not support flushing for Anthropic streaming")
		ah.writeAnthropicError(w, http.StatusInternalServerError, "api_error", "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	adapter := apiformat.NewStreamAdapter(requestModel)
	if err := adapter.Transform(resp.Body, w, flusher); err != nil {
		logger.Error("Stream transformation error: %v", err)
	}

	logger.Debug("Anthropic streaming response completed for model: %s", requestModel)
}

// handleBufferedResponse converts a non-streaming OpenAI response to Anthropic format.
func (ah *AnthropicHandler) handleBufferedResponse(w http.ResponseWriter, resp *http.Response, requestModel string) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read backend response: %v", err)
		ah.writeAnthropicError(w, http.StatusBadGateway, "api_error", "Failed to read backend response")
		return
	}

	anthropicResp, err := apiformat.ConvertResponse(respBody, requestModel)
	if err != nil {
		logger.Error("Failed to convert OpenAI response to Anthropic format: %v", err)
		ah.writeAnthropicError(w, http.StatusInternalServerError, "api_error",
			fmt.Sprintf("Failed to convert response: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(anthropicResp)

	logger.Debug("Anthropic buffered response completed for model: %s", requestModel)
}

// forwardBackendError translates a backend HTTP error into an Anthropic-style
// error response, preserving the original error details when possible.
func (ah *AnthropicHandler) forwardBackendError(w http.ResponseWriter, resp *http.Response) {
	body, _ := io.ReadAll(resp.Body)

	errMsg := fmt.Sprintf("Backend returned HTTP %d", resp.StatusCode)
	if len(body) > 0 {
		// Try to extract a meaningful error message from the backend response.
		var backendErr struct {
			Error any `json:"error"`
		}
		if json.Unmarshal(body, &backendErr) == nil && backendErr.Error != nil {
			switch v := backendErr.Error.(type) {
			case string:
				errMsg = v
			case map[string]any:
				if msg, ok := v["message"].(string); ok {
					errMsg = msg
				}
			}
		}
	}

	logger.Error("Backend error (HTTP %d): %s", resp.StatusCode, errMsg)
	ah.writeAnthropicError(w, resp.StatusCode, "api_error", errMsg)
}

// writeAnthropicError writes an error response in Anthropic API format.
//
// Anthropic error format:
//
//	{
//	  "type": "error",
//	  "error": {
//	    "type": "invalid_request_error",
//	    "message": "..."
//	  }
//	}
func (ah *AnthropicHandler) writeAnthropicError(w http.ResponseWriter, statusCode int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(apiformat.AnthropicError{
		Type: "error",
		Error: apiformat.AnthropicErrorBody{
			Type:    errType,
			Message: message,
		},
	})
}
