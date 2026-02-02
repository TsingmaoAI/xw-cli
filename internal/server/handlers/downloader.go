// Package handlers - downloader.go implements streaming model downloads from ModelScope.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/tsingmaoai/xw-cli/internal/logger"
	"github.com/tsingmaoai/xw-cli/internal/models"
)

// downloadModelStreaming downloads an AI model with real-time SSE progress streaming.
//
// This method orchestrates the complete model download process:
//  1. Creates the models storage directory if it doesn't exist
//  2. Generates a Python script using ModelScope's snapshot_download API
//  3. Executes the script as a subprocess with unbuffered output
//  4. Captures stdout and stderr from the download process
//  5. Streams each output line as an SSE progress message
//  6. Sends periodic heartbeat messages to keep the connection alive
//  7. Waits for download completion and returns the model path
//
// The function uses Python's ModelScope library to handle the actual download,
// leveraging its robust support for resumable downloads, integrity checking,
// and efficient HTTP transfers. All output from the Python process is captured
// and forwarded to the client in real-time via SSE.
//
// Heartbeat Mechanism:
// During download, a heartbeat is sent every 5 seconds to prevent client-side
// timeouts when the Python process is working but not producing output (e.g.,
// during file extraction or verification).
//
// Parameters:
//   - modelName: The ModelScope model identifier (e.g., "Qwen/Qwen2-7B")
//   - version: Model version or git branch (currently unused, defaults to "main")
//   - w: HTTP response writer for sending SSE messages
//   - flusher: HTTP flusher to immediately push SSE data to client
//
// Returns:
//   - string: The local filesystem path where the model was downloaded
//   - error: Any error that occurred during download
//
// SSE Message Format:
// All messages are sent as JSON objects with a "type" field:
//   - {"type":"progress","message":"..."}  - Download progress updates
//   - {"type":"heartbeat","message":"..."}  - Keep-alive messages
//
// Example:
//
//	path, err := h.downloadModelStreaming("Qwen/Qwen2-7B", "main", w, flusher)
//	if err != nil {
//	    logger.Error("Download failed: %v", err)
//	    return
//	}
//	logger.Info("Model downloaded to: %s", path)
func (h *Handler) downloadModelStreaming(ctx context.Context, modelName, modelID, version string, w http.ResponseWriter, flusher http.Flusher) (string, error) {
	// Ensure the models storage directory exists
	// This directory is configured in the server config (typically ~/.xw/models/)
	modelsDir := h.config.Storage.GetModelsDir()
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create models directory: %w", err)
	}

	logger.Info("Starting Go-native download for model %s (ID: %s, tag: %s) to %s", modelName, modelID, version, modelsDir)

	// Create ModelScope client
	client := models.NewClient()
	
	// Use the request context - it will be cancelled when client disconnects
	// This ensures downloads are stopped when the client disconnects (Ctrl+C)
	
	// Heartbeat ticker to keep connection alive
	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()
	
	// Start heartbeat in goroutine
	// Stop heartbeat when context is cancelled or download completes
	heartbeatDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-heartbeatTicker.C:
				msg := map[string]string{
					"type":    "heartbeat",
					"message": "Download in progress...",
				}
				msgJSON, _ := json.Marshal(msg)
				fmt.Fprintf(w, "data: %s\n\n", msgJSON)
				flusher.Flush()
			case <-ctx.Done():
				// Client disconnected, stop heartbeat
				return
			case <-heartbeatDone:
				return
			}
		}
	}()
	
	// Progress callback for real-time SSE updates
	// Simple passthrough - modelscope.DownloadModel handles formatting
	progressFunc := func(filename string, downloaded, total int64) {
		// Check if context is cancelled (client disconnected)
		select {
		case <-ctx.Done():
			return // Don't try to write if connection is closed
		default:
		}
		
		// Add panic recovery to prevent server crash on write errors
		defer func() {
			if r := recover(); r != nil {
				logger.Debug("Progress callback panic (client likely disconnected): %v", r)
			}
		}()
		
		// Send progress message directly from modelscope
		// The message is already formatted by overallProgressFunc
		sseMsg := map[string]string{
			"type":    "progress",
			"message": filename, // This is actually the formatted message from overallProgressFunc
		}
		msgJSON, _ := json.Marshal(sseMsg)
		fmt.Fprintf(w, "data: %s\n\n", msgJSON)
		flusher.Flush()
	}
	
	// Download model using pure Go implementation
	// The context will automatically cancel if client disconnects
	// Pass modelID (user-friendly name) and tag for proper directory structure
	modelPath, err := client.DownloadModel(ctx, modelName, modelID, version, modelsDir, progressFunc)
	
	// Stop heartbeat
	close(heartbeatDone)
	heartbeatTicker.Stop()
	
	if err != nil {
		// Check if error is due to context cancellation (client disconnect)
		if ctx.Err() == context.Canceled {
			logger.Info("Download of %s cancelled by client disconnect", modelName)
			return "", fmt.Errorf("download cancelled")
		}
		return "", fmt.Errorf("download failed: %w", err)
	}
	
	logger.Info("Model %s downloaded successfully to %s", modelName, modelPath)
	return modelPath, nil
}

