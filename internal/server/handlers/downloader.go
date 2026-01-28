// Package handlers - downloader.go implements streaming model downloads from ModelScope.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tsingmao/xw/internal/logger"
	"github.com/tsingmao/xw/internal/modelscope"
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
func (h *Handler) downloadModelStreaming(ctx context.Context, modelName, version string, w http.ResponseWriter, flusher http.Flusher) (string, error) {
	// Ensure the models storage directory exists
	// This directory is configured in the server config (typically ~/.xw/models/)
	modelsDir := h.config.Storage.ModelsDir
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create models directory: %w", err)
	}

	logger.Info("Starting Go-native download for model %s to %s", modelName, modelsDir)

	// Create ModelScope client
	client := modelscope.NewClient()
	
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
	// Use mutex to protect concurrent access from multiple download goroutines
	// Track progress per file since we're downloading multiple files concurrently
	var progressMutex sync.Mutex
	type fileProgress struct {
		lastPercent float64
		lastUpdate  time.Time
	}
	fileProgressMap := make(map[string]*fileProgress)
	
	progressFunc := func(filename string, downloaded, total int64) {
		// Check if context is cancelled (client disconnected)
		select {
		case <-ctx.Done():
			return // Don't try to write if connection is closed
		default:
		}
		
		progressMutex.Lock()
		defer progressMutex.Unlock()
		
		// Add panic recovery to prevent server crash on write errors
		defer func() {
			if r := recover(); r != nil {
				logger.Debug("Progress callback panic (client likely disconnected): %v", r)
			}
		}()
		
		now := time.Now()
		
		// Get or create progress tracking for this file
		fp, exists := fileProgressMap[filename]
		if !exists {
			fp = &fileProgress{
				lastPercent: -1.0,
				lastUpdate:  now,
			}
			fileProgressMap[filename] = fp
		}
		
		// Send progress updates with rate limiting
		if total > 0 {
			percent := float64(downloaded) / float64(total) * 100
			
			// Rate limit: send update only if:
			// 1. Progress increased by at least 5%, OR
			// 2. At least 1 second has passed since last update
			percentDiff := percent - fp.lastPercent
			timeSinceUpdate := now.Sub(fp.lastUpdate)
			
			if percentDiff >= 5.0 || timeSinceUpdate >= 1*time.Second {
				msg := fmt.Sprintf("Downloading %s: %.1f%%", filename, percent)
				sseMsg := map[string]string{
					"type":    "progress",
					"message": msg,
				}
				msgJSON, _ := json.Marshal(sseMsg)
				fmt.Fprintf(w, "data: %s\n\n", msgJSON)
				flusher.Flush()
				
				fp.lastPercent = percent
				fp.lastUpdate = now
			}
		} else {
			// For messages without progress (e.g., "Verifying...", "✓ Verified...")
			// Always send these messages
			msg := filename // filename contains the full message in this case
			sseMsg := map[string]string{
				"type":    "progress",
				"message": msg,
			}
			msgJSON, _ := json.Marshal(sseMsg)
			fmt.Fprintf(w, "data: %s\n\n", msgJSON)
			flusher.Flush()
		}
	}
	
	// Download model using pure Go implementation
	// The context will automatically cancel if client disconnects
	modelPath, err := client.DownloadModel(ctx, modelName, modelsDir, progressFunc)
	
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

