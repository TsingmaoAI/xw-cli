package apiformat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// StreamAdapter transforms an OpenAI SSE (Server-Sent Events) stream into an
// Anthropic SSE stream in real time.
//
// OpenAI streaming format:
//
//	data: {"id":"...","choices":[{"delta":{"content":"Hello"},...}]}
//	data: [DONE]
//
// Anthropic streaming format:
//
//	event: message_start
//	data: {"type":"message_start","message":{...}}
//
//	event: content_block_start
//	data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
//
//	event: content_block_delta
//	data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}
//
//	event: content_block_stop
//	data: {"type":"content_block_stop","index":0}
//
//	event: message_delta
//	data: {"type":"message_delta","delta":{"stop_reason":"end_turn",...},"usage":{...}}
//
//	event: message_stop
//	data: {"type":"message_stop"}
type StreamAdapter struct {
	requestModel string

	// State tracking across chunks.
	messageID      string
	blockIndex     int  // current Anthropic content block index
	textBlockOpen  bool // whether the initial text block has been opened
	textBlockDone  bool // whether the text block has been closed
	toolIndex      *int // OpenAI-side tool_call index of the current tool
	lastBlockIndex int  // highest Anthropic block index used so far
	inputTokens    int
	outputTokens   int
	finished       bool
}

// NewStreamAdapter creates a StreamAdapter for converting a single streaming
// response. The requestModel is echoed in the Anthropic response metadata.
func NewStreamAdapter(requestModel string) *StreamAdapter {
	return &StreamAdapter{
		requestModel: requestModel,
		messageID:    generateMessageID(),
	}
}

// Transform reads an OpenAI SSE stream from reader and writes an Anthropic
// SSE stream to the ResponseWriter, flushing after each event for low latency.
//
// The caller is responsible for setting appropriate SSE response headers
// (Content-Type: text/event-stream, etc.) before calling Transform.
func (sa *StreamAdapter) Transform(reader io.Reader, w http.ResponseWriter, flusher http.Flusher) error {
	// Emit the opening Anthropic envelope.
	sa.emitMessageStart(w, flusher)
	sa.emitContentBlockStart(w, flusher, 0, map[string]any{"type": "text", "text": ""})
	sa.textBlockOpen = true
	sa.emitPing(w, flusher)

	scanner := bufio.NewScanner(reader)
	// Increase scanner buffer for large chunks (e.g. tool call arguments).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE lines starting with "data: " carry the payload.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")

		// Terminal marker.
		if payload == "[DONE]" {
			break
		}

		var chunk OpenAIChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // skip malformed chunks
		}

		sa.processChunk(chunk, w, flusher)
	}

	// Ensure all blocks are properly closed and the message is finalized.
	sa.finalize(w, flusher)

	return scanner.Err()
}

// processChunk handles a single decoded OpenAI streaming chunk.
func (sa *StreamAdapter) processChunk(chunk OpenAIChatChunk, w http.ResponseWriter, flusher http.Flusher) {
	// Collect usage stats when available (usually in the final chunk).
	if chunk.Usage != nil {
		sa.inputTokens = chunk.Usage.PromptTokens
		sa.outputTokens = chunk.Usage.CompletionTokens
	}

	if len(chunk.Choices) == 0 {
		return
	}
	choice := chunk.Choices[0]
	delta := choice.Delta

	// --- Text content ---
	if delta.Content != nil && *delta.Content != "" {
		if sa.textBlockOpen && !sa.textBlockDone {
			sa.emitTextDelta(w, flusher, 0, *delta.Content)
		}
	}

	// --- Tool calls ---
	if len(delta.ToolCalls) > 0 {
		sa.processToolCalls(delta.ToolCalls, w, flusher)
	}

	// --- Finish reason ---
	if choice.FinishReason != nil {
		sa.handleFinish(*choice.FinishReason, w, flusher)
	}
}

// processToolCalls handles tool_call deltas. Each new tool call opens a new
// Anthropic content block; argument fragments are emitted as input_json_delta.
func (sa *StreamAdapter) processToolCalls(toolCalls []OpenAIToolCall, w http.ResponseWriter, flusher http.Flusher) {
	// Close the text block on the first tool call if it hasn't been closed yet.
	if sa.toolIndex == nil && !sa.textBlockDone {
		sa.emitContentBlockStop(w, flusher, 0)
		sa.textBlockDone = true
	}

	for _, tc := range toolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}

		// Detect new tool call (different OpenAI index than current).
		if sa.toolIndex == nil || idx != *sa.toolIndex {
			sa.toolIndex = &idx
			sa.lastBlockIndex++
			blockIdx := sa.lastBlockIndex

			toolID := tc.ID
			if toolID == "" {
				toolID = generateToolID()
			}

			sa.emitContentBlockStart(w, flusher, blockIdx, map[string]any{
				"type":  "tool_use",
				"id":    toolID,
				"name":  tc.Function.Name,
				"input": map[string]any{},
			})
		}

		// Emit argument fragment.
		if tc.Function.Arguments != "" {
			sa.emitInputJSONDelta(w, flusher, sa.lastBlockIndex, tc.Function.Arguments)
		}
	}
}

// handleFinish processes an OpenAI finish_reason, closing all open blocks and
// emitting the Anthropic message_delta and message_stop events.
func (sa *StreamAdapter) handleFinish(reason string, w http.ResponseWriter, flusher http.Flusher) {
	if sa.finished {
		return
	}
	sa.finished = true

	// Close open tool call blocks.
	for i := 1; i <= sa.lastBlockIndex; i++ {
		sa.emitContentBlockStop(w, flusher, i)
	}

	// Close text block if still open.
	if !sa.textBlockDone {
		sa.emitContentBlockStop(w, flusher, 0)
		sa.textBlockDone = true
	}

	stopReason := mapFinishReason(reason)
	sa.emitMessageDelta(w, flusher, stopReason)
	sa.emitMessageStop(w, flusher)
}

// finalize ensures proper stream termination even if no finish_reason was received.
func (sa *StreamAdapter) finalize(w http.ResponseWriter, flusher http.Flusher) {
	if !sa.finished {
		sa.handleFinish("stop", w, flusher)
	}
}

// ---------------------------------------------------------------------------
// SSE event emitters
//
// Each method below emits one Anthropic SSE event and flushes immediately.
// They map directly to the event types defined in the Anthropic streaming spec:
// https://docs.anthropic.com/en/api/messages-streaming
// ---------------------------------------------------------------------------

// emitMessageStart sends the opening message_start event that contains the
// message envelope (id, model, role) and initial zeroed usage counters.
func (sa *StreamAdapter) emitMessageStart(w http.ResponseWriter, flusher http.Flusher) {
	data := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            sa.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         sa.requestModel,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":                0,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	}
	writeSSE(w, flusher, "message_start", data)
}

// emitContentBlockStart opens a new content block at the given index.
// The block parameter describes the block type (text or tool_use) and its
// initial state.
func (sa *StreamAdapter) emitContentBlockStart(w http.ResponseWriter, flusher http.Flusher, index int, block map[string]any) {
	data := map[string]any{
		"type":          "content_block_start",
		"index":         index,
		"content_block": block,
	}
	writeSSE(w, flusher, "content_block_start", data)
}

// emitTextDelta sends an incremental text fragment for the content block at
// the given index. Each delta is flushed immediately for low-latency streaming.
func (sa *StreamAdapter) emitTextDelta(w http.ResponseWriter, flusher http.Flusher, index int, text string) {
	data := map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	}
	writeSSE(w, flusher, "content_block_delta", data)
}

// emitInputJSONDelta sends a partial JSON fragment for a tool_use content block's
// input field. The client accumulates these fragments to reconstruct the full
// tool input arguments.
func (sa *StreamAdapter) emitInputJSONDelta(w http.ResponseWriter, flusher http.Flusher, index int, partialJSON string) {
	data := map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": partialJSON,
		},
	}
	writeSSE(w, flusher, "content_block_delta", data)
}

// emitContentBlockStop signals that the content block at the given index is
// complete and no further deltas will be sent for it.
func (sa *StreamAdapter) emitContentBlockStop(w http.ResponseWriter, flusher http.Flusher, index int) {
	data := map[string]any{
		"type":  "content_block_stop",
		"index": index,
	}
	writeSSE(w, flusher, "content_block_stop", data)
}

// emitMessageDelta sends the final message-level metadata including the
// stop_reason (end_turn / max_tokens / tool_use) and accumulated output token
// usage. This event immediately precedes message_stop.
func (sa *StreamAdapter) emitMessageDelta(w http.ResponseWriter, flusher http.Flusher, stopReason string) {
	data := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": sa.outputTokens,
		},
	}
	writeSSE(w, flusher, "message_delta", data)
}

// emitMessageStop sends the terminal message_stop event followed by a
// "data: [DONE]" marker for client compatibility.
func (sa *StreamAdapter) emitMessageStop(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "message_stop", map[string]string{"type": "message_stop"})
	// Send terminal [DONE] marker for compatibility with clients that expect it
	// (matches the Python claude-code-proxy behaviour).
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// emitPing sends a keep-alive ping event. Anthropic's API sends these
// periodically to prevent connection timeouts.
func (sa *StreamAdapter) emitPing(w http.ResponseWriter, flusher http.Flusher) {
	writeSSE(w, flusher, "ping", map[string]string{"type": "ping"})
}

// writeSSE writes a single SSE event and flushes immediately.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	flusher.Flush()
}
