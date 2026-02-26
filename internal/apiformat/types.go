// Package apiformat provides bidirectional conversion between Anthropic Messages API
// and OpenAI Chat Completions API formats.
//
// The xw server natively speaks OpenAI-compatible API to backend inference engines
// (vLLM, MindIE, etc.). This package enables clients that use the Anthropic Messages
// API (such as Claude Code) to seamlessly interact with those same backends by
// translating requests and responses between the two formats.
//
// Conversion flow:
//
//	Client (Anthropic format)
//	  → ConvertRequest()     → OpenAI format request body
//	  → [forwarded to backend inference engine]
//	  → ConvertResponse()    → Anthropic format response body  (non-streaming)
//	  → NewStreamAdapter()   → Anthropic SSE event stream      (streaming)
package apiformat

import "encoding/json"

// ---------------------------------------------------------------------------
// Anthropic Messages API types
// Reference: https://docs.anthropic.com/en/api/messages
// ---------------------------------------------------------------------------

// MessagesRequest represents an Anthropic Messages API request.
type MessagesRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Messages      []Message         `json:"messages"`
	System        json.RawMessage   `json:"system,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	TopK          *int              `json:"top_k,omitempty"`
	Tools         []Tool            `json:"tools,omitempty"`
	ToolChoice    json.RawMessage   `json:"tool_choice,omitempty"`
	Thinking      *ThinkingConfig   `json:"thinking,omitempty"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
}

// Message is a single turn in the Anthropic conversation.
// Content may be a plain string or an array of ContentBlock objects.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ContentBlock is a polymorphic content element within a message.
// The Type field determines which other fields are populated.
type ContentBlock struct {
	Type string `json:"type"`

	// Type "text"
	Text string `json:"text,omitempty"`

	// Type "image"
	Source map[string]any `json:"source,omitempty"`

	// Type "tool_use"
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// Type "tool_result"
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string | []ContentBlock
}

// Tool defines a tool available to the model (Anthropic format).
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// ThinkingConfig controls extended thinking for supported models.
type ThinkingConfig struct {
	Enabled bool `json:"enabled"`
}

// MessagesResponse is the Anthropic Messages API response (non-streaming).
type MessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []ContentBlock `json:"content"`
	StopReason   string         `json:"stop_reason,omitempty"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// Usage reports token consumption in Anthropic format.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// TokenCountRequest is the Anthropic token counting endpoint request.
type TokenCountRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	System   json.RawMessage `json:"system,omitempty"`
	Tools    []Tool          `json:"tools,omitempty"`
}

// TokenCountResponse is the Anthropic token counting endpoint response.
type TokenCountResponse struct {
	InputTokens int `json:"input_tokens"`
}

// AnthropicError is the Anthropic API error envelope.
type AnthropicError struct {
	Type  string              `json:"type"`
	Error AnthropicErrorBody  `json:"error"`
}

// AnthropicErrorBody is the inner error object.
type AnthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// OpenAI Chat Completions API types (minimal subset for conversion)
// Only the fields required for request construction and response parsing.
// ---------------------------------------------------------------------------

// OpenAIChatRequest is the request body sent to an OpenAI-compatible backend.
type OpenAIChatRequest struct {
	Model       string           `json:"model"`
	Messages    []OpenAIMessage  `json:"messages"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	TopK        *int             `json:"top_k,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Stop        []string         `json:"stop,omitempty"`
	Tools       []OpenAITool     `json:"tools,omitempty"`
	ToolChoice  any              `json:"tool_choice,omitempty"`

	// StreamOptions requests usage stats in the final streaming chunk.
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`
}

// StreamOptions configures streaming behaviour.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// OpenAIMessage is a message in the OpenAI chat format.
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"`              // string | []OpenAIContentPart
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// OpenAIContentPart is a typed content element within a message.
type OpenAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

// OpenAIImageURL references an image for vision requests.
type OpenAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// OpenAITool is a tool definition in OpenAI function-calling format.
type OpenAITool struct {
	Type     string         `json:"type"` // "function"
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction describes a callable function.
type OpenAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// OpenAIToolCall represents a tool invocation in an assistant message.
type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function OpenAIFunctionCall `json:"function"`
	Index    *int               `json:"index,omitempty"` // present in streaming chunks
}

// OpenAIFunctionCall carries the function name and serialized arguments.
type OpenAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

// OpenAIChatResponse is the non-streaming response from an OpenAI-compatible backend.
type OpenAIChatResponse struct {
	ID      string         `json:"id"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIChoice is a single completion choice.
type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// OpenAIUsage reports token consumption in OpenAI format.
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIChatChunk is a single Server-Sent Event chunk in a streaming response.
type OpenAIChatChunk struct {
	ID      string              `json:"id"`
	Choices []OpenAIChunkChoice `json:"choices"`
	Usage   *OpenAIUsage        `json:"usage,omitempty"`
}

// OpenAIChunkChoice is a streaming delta within a chunk.
type OpenAIChunkChoice struct {
	Index        int              `json:"index"`
	Delta        OpenAIChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

// OpenAIChunkDelta carries incremental content or tool call fragments.
type OpenAIChunkDelta struct {
	Role      string           `json:"role,omitempty"`
	Content   *string          `json:"content,omitempty"`
	ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
}
