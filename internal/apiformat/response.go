package apiformat

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

// ConvertResponse translates a non-streaming OpenAI ChatCompletion response
// body into an Anthropic MessagesResponse.
//
// Parameters:
//   - body: raw JSON response from the OpenAI-compatible backend
//   - requestModel: the model name to echo back in the Anthropic response
//     (typically the original model from the client request)
func ConvertResponse(body []byte, requestModel string) (*MessagesResponse, error) {
	var resp OpenAIChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing OpenAI response: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI response contains no choices")
	}

	choice := resp.Choices[0]
	content := buildContentBlocks(choice.Message)
	stopReason := mapFinishReason(choice.FinishReason)

	return &MessagesResponse{
		ID:           coalesce(resp.ID, generateMessageID()),
		Type:         "message",
		Role:         "assistant",
		Model:        requestModel,
		Content:      content,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}, nil
}

// buildContentBlocks extracts text and tool_use blocks from an OpenAI message.
func buildContentBlocks(msg OpenAIMessage) []ContentBlock {
	var blocks []ContentBlock

	// Text content.
	if text := messageText(msg); text != "" {
		blocks = append(blocks, ContentBlock{Type: "text", Text: text})
	}

	// Tool calls → tool_use blocks.
	for _, tc := range msg.ToolCalls {
		input := parseToolArguments(tc.Function.Arguments)
		blocks = append(blocks, ContentBlock{
			Type:  "tool_use",
			ID:    coalesce(tc.ID, generateToolID()),
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	// Anthropic requires at least one content block.
	if len(blocks) == 0 {
		blocks = append(blocks, ContentBlock{Type: "text", Text: ""})
	}

	return blocks
}

// messageText extracts the text content from an OpenAI message.
// The content field can be a string or null.
func messageText(msg OpenAIMessage) string {
	if msg.Content == nil {
		return ""
	}
	switch v := msg.Content.(type) {
	case string:
		return v
	default:
		// Attempt JSON round-trip for unexpected types.
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		var s string
		if json.Unmarshal(data, &s) == nil {
			return s
		}
		return ""
	}
}

// parseToolArguments deserialises the JSON arguments string from an OpenAI
// tool call into a map. Returns an empty map on failure.
func parseToolArguments(args string) map[string]any {
	if args == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return map[string]any{"raw": args}
	}
	return m
}

// mapFinishReason converts an OpenAI finish_reason to an Anthropic stop_reason.
//
// Mapping:
//
//	"stop"       → "end_turn"
//	"length"     → "max_tokens"
//	"tool_calls" → "tool_use"
//	<other>      → "end_turn"
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// generateMessageID produces a unique ID in Anthropic's msg_ format.
func generateMessageID() string {
	return "msg_" + randomHex(12)
}

// generateToolID produces a unique ID in Anthropic's toolu_ format.
func generateToolID() string {
	return "toolu_" + randomHex(12)
}

// randomHex returns a cryptographically random hex string of n bytes (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// coalesce returns the first non-empty string.
func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
