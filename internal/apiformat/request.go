package apiformat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ConvertRequest translates an Anthropic MessagesRequest into an OpenAI
// ChatCompletionRequest body (JSON-encoded).
//
// The conversion covers:
//   - System prompt (string or structured content blocks → system message)
//   - Conversation messages with polymorphic content handling
//   - Tool definitions (Anthropic input_schema → OpenAI function parameters)
//   - Tool choice mapping (auto / any / specific tool)
//   - Parameter mapping (max_tokens, temperature, top_p, stop_sequences)
//
// The returned []byte is ready to be forwarded to an OpenAI-compatible backend.
// The modelOverride parameter allows replacing the model name with the backend
// instance's actual model identifier.
func ConvertRequest(req *MessagesRequest, modelOverride string) ([]byte, error) {
	model := req.Model
	if modelOverride != "" {
		model = modelOverride
	}

	messages, err := convertMessages(req.System, req.Messages)
	if err != nil {
		return nil, fmt.Errorf("converting messages: %w", err)
	}

	out := OpenAIChatRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		TopK:        req.TopK,
		Stream:      req.Stream,
		Stop:        req.StopSequences,
	}

	if req.Stream {
		out.StreamOptions = &StreamOptions{IncludeUsage: true}
	}

	if len(req.Tools) > 0 {
		out.Tools = convertTools(req.Tools)
	}

	if len(req.ToolChoice) > 0 {
		out.ToolChoice, err = convertToolChoice(req.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("converting tool_choice: %w", err)
		}
	}

	return json.Marshal(out)
}

// convertMessages builds the OpenAI messages array from an Anthropic system
// prompt and conversation history.
func convertMessages(system json.RawMessage, msgs []Message) ([]OpenAIMessage, error) {
	var out []OpenAIMessage

	// System prompt
	if len(system) > 0 {
		sysText, err := parseSystemPrompt(system)
		if err != nil {
			return nil, err
		}
		if sysText != "" {
			out = append(out, OpenAIMessage{Role: "system", Content: sysText})
		}
	}

	for _, msg := range msgs {
		converted, err := convertOneMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("message role=%q: %w", msg.Role, err)
		}
		out = append(out, converted...)
	}

	return out, nil
}

// parseSystemPrompt handles the polymorphic system field which can be either
// a JSON string or an array of {type:"text", text:"..."} objects.
func parseSystemPrompt(raw json.RawMessage) (string, error) {
	// Try string first (most common).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	// Try array of content blocks.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("system must be a string or array of text blocks: %w", err)
	}

	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// convertOneMessage converts a single Anthropic message to one or more OpenAI
// messages. A single Anthropic message may produce multiple OpenAI messages
// when tool results are involved.
func convertOneMessage(msg Message) ([]OpenAIMessage, error) {
	// Try to unmarshal content as a plain string.
	var textContent string
	if err := json.Unmarshal(msg.Content, &textContent); err == nil {
		return []OpenAIMessage{{Role: msg.Role, Content: textContent}}, nil
	}

	// Content is an array of blocks.
	var blocks []ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or array of content blocks: %w", err)
	}

	if msg.Role == "user" {
		return convertUserBlocks(blocks)
	}
	return convertAssistantBlocks(blocks)
}

// convertUserBlocks handles user messages containing text, images, and tool results.
//
// Anthropic places tool_result blocks inside user messages. For OpenAI-compatible
// backends, we flatten tool results into plain text within a single user message,
// as most backends do not support the OpenAI tool-result message type.
func convertUserBlocks(blocks []ContentBlock) ([]OpenAIMessage, error) {
	hasToolResult := false
	for _, b := range blocks {
		if b.Type == "tool_result" {
			hasToolResult = true
			break
		}
	}

	if hasToolResult {
		return convertUserToolResults(blocks)
	}

	// Standard user message with text/image blocks.
	var parts []OpenAIContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, OpenAIContentPart{Type: "text", Text: b.Text})
		case "image":
			url := buildImageURL(b.Source)
			if url != "" {
				parts = append(parts, OpenAIContentPart{
					Type:     "image_url",
					ImageURL: &OpenAIImageURL{URL: url},
				})
			}
		}
	}

	if len(parts) == 1 && parts[0].Type == "text" {
		return []OpenAIMessage{{Role: "user", Content: parts[0].Text}}, nil
	}
	return []OpenAIMessage{{Role: "user", Content: parts}}, nil
}

// convertUserToolResults extracts tool results from user message blocks and
// flattens them into a single plain-text user message. This approach maximises
// compatibility across different inference backends.
func convertUserToolResults(blocks []ContentBlock) ([]OpenAIMessage, error) {
	var sb strings.Builder

	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
			sb.WriteByte('\n')
		case "tool_result":
			sb.WriteString("Tool result for ")
			sb.WriteString(b.ToolUseID)
			sb.WriteString(":\n")
			sb.WriteString(extractToolResultContent(b.Content))
			sb.WriteByte('\n')
		}
	}

	text := strings.TrimSpace(sb.String())
	if text == "" {
		text = "..."
	}
	return []OpenAIMessage{{Role: "user", Content: text}}, nil
}

// convertAssistantBlocks handles assistant messages containing text and tool_use blocks.
// Tool use blocks are converted to OpenAI tool_calls on the assistant message.
func convertAssistantBlocks(blocks []ContentBlock) ([]OpenAIMessage, error) {
	var textParts []string
	var toolCalls []OpenAIToolCall

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		case "tool_use":
			args, err := json.Marshal(b.Input)
			if err != nil {
				args = []byte("{}")
			}
			toolCalls = append(toolCalls, OpenAIToolCall{
				ID:   b.ID,
				Type: "function",
				Function: OpenAIFunctionCall{
					Name:      b.Name,
					Arguments: string(args),
				},
			})
		}
	}

	msg := OpenAIMessage{Role: "assistant"}
	text := strings.Join(textParts, "\n")
	if text != "" {
		msg.Content = text
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}

	return []OpenAIMessage{msg}, nil
}

// extractToolResultContent normalises the polymorphic content field of a
// tool_result block into a plain string.
func extractToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	// Try plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	// Try array of content blocks.
	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	// Try arbitrary object → serialise as JSON.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		data, _ := json.Marshal(obj)
		return string(data)
	}

	return string(raw)
}

// buildImageURL constructs a data URI or URL from an Anthropic image source.
func buildImageURL(source map[string]any) string {
	if source == nil {
		return ""
	}
	srcType, _ := source["type"].(string)
	if srcType == "base64" {
		mediaType, _ := source["media_type"].(string)
		data, _ := source["data"].(string)
		if mediaType != "" && data != "" {
			return "data:" + mediaType + ";base64," + data
		}
	}
	if srcType == "url" {
		url, _ := source["url"].(string)
		return url
	}
	return ""
}

// convertTools translates Anthropic tool definitions to OpenAI function tools.
func convertTools(tools []Tool) []OpenAITool {
	out := make([]OpenAITool, 0, len(tools))
	for _, t := range tools {
		out = append(out, OpenAITool{
			Type: "function",
			Function: OpenAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return out
}

// convertToolChoice maps Anthropic tool_choice to OpenAI format.
//
// Anthropic formats:
//
//	{"type": "auto"}                        → "auto"
//	{"type": "any"}                         → "required"
//	{"type": "tool", "name": "foo"}         → {"type": "function", "function": {"name": "foo"}}
func convertToolChoice(raw json.RawMessage) (any, error) {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return "auto", nil
	}

	switch tc.Type {
	case "auto", "":
		return "auto", nil
	case "any":
		return "required", nil
	case "tool":
		if tc.Name == "" {
			return "auto", nil
		}
		return map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		}, nil
	default:
		return "auto", nil
	}
}
