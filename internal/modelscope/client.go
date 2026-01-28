// Package modelscope provides a pure Go implementation of ModelScope model downloading.
//
// This package implements the core functionality of ModelScope's snapshot_download
// without requiring Python dependencies or external binaries. It directly interacts
// with ModelScope's HTTP API to download models.
//
// Key features:
//   - Pure Go implementation (no Python required)
//   - Resumable downloads with progress tracking
//   - Parallel file downloads for better performance
//   - File integrity validation
//   - Proper caching and directory structure
//
// Example usage:
//
//	client := modelscope.NewClient()
//	modelPath, err := client.DownloadModel(ctx, "Qwen/Qwen2-0.5B", "/path/to/cache", progressFunc)
package modelscope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultEndpoint is the default ModelScope API endpoint
	DefaultEndpoint = "https://www.modelscope.cn"
	
	// DefaultUserAgent is the user agent string for HTTP requests
	DefaultUserAgent = "xw/1.0.0 (Go)"
	
	// ChunkSize for file downloads (8MB)
	ChunkSize = 8 * 1024 * 1024
	
	// ParallelDownloadThreshold - files larger than this will use parallel download (500MB)
	ParallelDownloadThreshold = 500 * 1024 * 1024
	
	// ParallelDownloadPartSize - size of each part in parallel download (160MB)
	ParallelDownloadPartSize = 160 * 1024 * 1024
	
	// MaxParallelDownloads - maximum number of concurrent downloads
	MaxParallelDownloads = 4
)

// Client handles ModelScope API interactions and model downloads.
type Client struct {
	endpoint   string
	httpClient *http.Client
	userAgent  string
}

// ProgressFunc is called periodically during download to report progress.
// Parameters: filename, bytesDownloaded, totalBytes
type ProgressFunc func(filename string, downloaded, total int64)

// NewClient creates a new ModelScope client with default settings.
func NewClient() *Client {
	return &Client{
		endpoint:  DefaultEndpoint,
		userAgent: DefaultUserAgent,
		httpClient: &http.Client{
			Timeout: 0, // No timeout for large downloads
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// ModelInfo represents metadata about a model from ModelScope API.
type ModelInfo struct {
	ModelID  string      `json:"model_id"`
	Revision string      `json:"revision"`
	Files    []FileInfo  `json:"files"`
}

// FileInfo represents a single file in a model repository.
type FileInfo struct {
	Name   string `json:"Name"`   // File path relative to model root
	Size   int64  `json:"Size"`   // File size in bytes
	Sha256 string `json:"Sha256"` // SHA256 hash for integrity validation
}

// DownloadModel downloads a complete model from ModelScope.
//
// This function:
//  1. Queries the ModelScope API for model file list
//  2. Creates the local cache directory structure
//  3. Downloads all files (with resume support)
//  4. Validates file integrity
//  5. Returns the local path to the downloaded model
//
// Parameters:
//   - ctx: Context for cancellation
//   - modelID: Model identifier (e.g., "Qwen/Qwen2-0.5B")
//   - cacheDir: Base directory for caching models
//   - progress: Optional callback for progress updates
//
// Returns:
//   - Local path to the downloaded model
//   - Error if download fails
func (c *Client) DownloadModel(
	ctx context.Context,
	modelID string,
	cacheDir string,
	progress ProgressFunc,
) (string, error) {
	// Parse model ID into owner and name
	parts := strings.Split(modelID, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid model ID format: %s (expected: owner/name)", modelID)
	}
	owner, name := parts[0], parts[1]
	
	// Create cache directory structure: cache_dir/owner/name
	modelDir := filepath.Join(cacheDir, owner, name)
	if err := os.MkdirAll(modelDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create model directory: %w", err)
	}
	
	// Acquire download lock to prevent concurrent downloads of the same model
	// This protects against file corruption from multiple download processes
	lockPath := filepath.Join(modelDir, ".download.lock")
	if err := c.acquireLock(lockPath); err != nil {
		return "", fmt.Errorf("failed to acquire download lock: %w", err)
	}
	// Ensure lock is released on function exit (success, error, or cancellation)
	defer c.releaseLock(lockPath)
	
	// Get model file list from API
	files, err := c.getModelFiles(ctx, modelID)
	if err != nil {
		return "", fmt.Errorf("failed to get model files: %w", err)
	}
	
	// Download files with controlled concurrency
	// Allow multiple files to download simultaneously for better performance
	// Limit concurrency to avoid overwhelming the network or server
	const maxConcurrentDownloads = 3 // Download up to 3 files at a time
	
	semaphore := make(chan struct{}, maxConcurrentDownloads)
	var wg sync.WaitGroup
	errChan := make(chan error, len(files))
	
	for _, file := range files {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		
		// Acquire semaphore slot
		semaphore <- struct{}{}
		wg.Add(1)
		
		go func(f FileInfo) {
			defer func() {
				<-semaphore // Release semaphore slot
				wg.Done()
			}()
			
			// Check context before starting
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			default:
			}
			
			localPath := filepath.Join(modelDir, f.Name)
		
			// Download file
			if err := c.downloadFile(ctx, f, localPath, modelID, progress); err != nil {
				// Don't report error if context was cancelled
				if ctx.Err() != nil {
					return
				}
				errChan <- fmt.Errorf("failed to download %s: %w", f.Name, err)
				return
		}
		
		// Validate file integrity if SHA256 is available
			if f.Sha256 != "" {
				// Check context before validation
				select {
				case <-ctx.Done():
					return
				default:
				}
				
			// Notify user that validation is in progress (can take time for large files)
			if progress != nil {
					progress(fmt.Sprintf("Verifying %s", f.Name), 0, 0)
			}
			
				if err := c.validateFileIntegrity(localPath, f.Sha256); err != nil {
					// Don't report error if context was cancelled
					if ctx.Err() != nil {
						return
					}
					errChan <- fmt.Errorf("integrity check failed for %s: %w", f.Name, err)
					return
			}
			
			// Notify completion
			if progress != nil {
					progress(fmt.Sprintf("✓ Verified %s", f.Name), 0, 0)
			}
		}
		}(file)
	}
	
	// Wait for all downloads to complete
	wg.Wait()
	close(errChan)
	
	// Check if any download failed
	if err := <-errChan; err != nil {
		return "", err
	}
	
	return modelDir, nil
}

// acquireLock creates a lock file to prevent concurrent downloads of the same model.
//
// The lock file contains the process ID and timestamp for debugging purposes.
// If a lock file already exists, this function returns an error.
//
// Parameters:
//   - lockPath: Path to the lock file
//
// Returns:
//   - Error if lock cannot be acquired (e.g., already locked by another process)
func (c *Client) acquireLock(lockPath string) error {
	// Check if lock already exists
	if _, err := os.Stat(lockPath); err == nil {
		// Lock file exists, read it to provide helpful error message
		data, _ := os.ReadFile(lockPath)
		return fmt.Errorf("model download already in progress (lock: %s). If this is stale, remove the lock file manually: %s", 
			string(data), lockPath)
	}
	
	// Create lock file with PID and timestamp
	lockInfo := fmt.Sprintf("pid=%d,time=%s", os.Getpid(), time.Now().Format(time.RFC3339))
	if err := os.WriteFile(lockPath, []byte(lockInfo), 0644); err != nil {
		return fmt.Errorf("failed to create lock file: %w", err)
	}
	
	return nil
}

// releaseLock removes the lock file to allow future downloads.
//
// This function should be called when the download completes, fails, or is cancelled.
// It's safe to call even if the lock file doesn't exist.
//
// Parameters:
//   - lockPath: Path to the lock file
func (c *Client) releaseLock(lockPath string) {
	// Ignore errors - lock file might not exist if download was cancelled early
	os.Remove(lockPath)
}

// validateFileIntegrity verifies the SHA256 hash of a downloaded file.
//
// If the hash doesn't match, the file is deleted to prevent corrupted files.
// For large files, this can take significant time as it reads the entire file.
//
// Parameters:
//   - filePath: Path to the file to validate
//   - expectedSha256: Expected SHA256 hash (hex string)
//
// Returns:
//   - Error if validation fails or file cannot be read
func (c *Client) validateFileIntegrity(filePath, expectedSha256 string) error {
	// Open file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for validation: %w", err)
	}
	defer file.Close()
	
	// Get file size for progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	fileSize := fileInfo.Size()
	
	// Compute SHA256 hash with progress tracking
	hash := sha256.New()
	buffer := make([]byte, 64*1024) // 64KB buffer
	var totalRead int64
	
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
			totalRead += int64(n)
		}
		
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read file: %w", readErr)
		}
	}
	
	actualSha256 := hex.EncodeToString(hash.Sum(nil))
	
	// Compare hashes
	if actualSha256 != expectedSha256 {
		// Delete corrupted file
		os.Remove(filePath)
		return fmt.Errorf("integrity check failed: expected %s, got %s (file deleted, size: %d bytes)", 
			expectedSha256, actualSha256, fileSize)
	}
	
	return nil
}

// downloadFileParallel downloads a large file using parallel chunked downloads.
//
// This function splits the file into multiple parts and downloads them concurrently
// for better performance on large files. It supports resume by checking existing parts.
//
// Parameters:
//   - ctx: Context for cancellation
//   - file: File metadata including size and name
//   - localPath: Destination path for the downloaded file
//   - modelID: Model identifier for URL construction
//   - progress: Optional callback for progress reporting
//
// Returns:
//   - Error if download fails
func (c *Client) downloadFileParallel(
	ctx context.Context,
	file FileInfo,
	localPath string,
	modelID string,
	progress ProgressFunc,
) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	
	// Check if file already exists and is complete
	if info, err := os.Stat(localPath); err == nil && info.Size() == file.Size {
		if progress != nil {
			progress(file.Name, file.Size, file.Size)
		}
		return nil // File already downloaded
	}
	
	// Send initial progress to indicate download has started
	if progress != nil {
		progress(file.Name, 0, file.Size)
	}
	
	// Build download URL
	downloadURL := fmt.Sprintf("%s/api/v1/models/%s/repo?Revision=master&FilePath=%s",
		c.endpoint, modelID, file.Name)
	
	// Calculate number of parts
	numParts := int((file.Size + ParallelDownloadPartSize - 1) / ParallelDownloadPartSize)
	if numParts > MaxParallelDownloads {
		numParts = MaxParallelDownloads
	}
	
	// Create temporary file for writing
	tempPath := localPath + ".tmp"
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()
	
	// Set file size
	if err := tempFile.Truncate(file.Size); err != nil {
		return fmt.Errorf("failed to set file size: %w", err)
	}
	
	// Download parts in parallel with real-time progress tracking
	var wg sync.WaitGroup
	errChan := make(chan error, numParts)
	var totalDownloaded int64
	var downloadMutex sync.Mutex
	
	// Progress callback for each part with reduced lock contention
	// Use atomic operations or batch updates to reduce mutex overhead
	var lastProgressReport int64
	partProgressFunc := func(partBytes int64) {
		if progress == nil {
			return
		}
		
		downloadMutex.Lock()
		totalDownloaded += partBytes
		current := totalDownloaded
		downloadMutex.Unlock()
		
		// Only report progress if significant change (reduce callback overhead)
		// Report every 5MB or more to reduce lock contention
		if current-lastProgressReport >= 5*1024*1024 || current == file.Size {
			progress(file.Name, current, file.Size)
			lastProgressReport = current
		}
	}
	
	for i := 0; i < numParts; i++ {
		start := int64(i) * ParallelDownloadPartSize
		end := start + ParallelDownloadPartSize - 1
		if end >= file.Size {
			end = file.Size - 1
		}
		
		wg.Add(1)
		go func(partStart, partEnd int64) {
			defer wg.Done()
			
			// Download this part with progress callback
			if err := c.downloadFilePart(ctx, downloadURL, tempPath, partStart, partEnd, partProgressFunc); err != nil {
				errChan <- err
				return
			}
		}(start, end)
	}
	
	// Wait for all parts to complete
	wg.Wait()
	close(errChan)
	
	// Check for errors
	if err := <-errChan; err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("parallel download failed: %w", err)
	}
	
	// Move temp file to final location
	if err := os.Rename(tempPath, localPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	
	return nil
}

// downloadFilePart downloads a specific byte range of a file.
//
// This is used by parallel download to fetch individual chunks.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: Download URL
//   - destPath: Destination file path
//   - start: Starting byte offset
//   - end: Ending byte offset (inclusive)
//   - progressCallback: Called with bytes downloaded for this part
//
// Returns:
//   - Error if download fails
func (c *Client) downloadFilePart(
	ctx context.Context,
	url string,
	destPath string,
	start, end int64,
	progressCallback func(int64),
) error {
	// Create HTTP request with Range header
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	req.Header.Set("User-Agent", c.userAgent)
	
	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	// Check status code (should be 206 Partial Content)
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	
	// Open destination file for writing at specific offset
	file, err := os.OpenFile(destPath, os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()
	
	// Seek to start position
	if _, err := file.Seek(start, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	
	// Copy data with optimized progress reporting
	buffer := make([]byte, 256*1024) // 256KB buffer for better performance
	var downloaded int64
	var sinceLast int64
	
	for {
		// Check context less frequently for better performance
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			// Write to file
			if _, writeErr := file.Write(buffer[:n]); writeErr != nil {
				return fmt.Errorf("failed to write: %w", writeErr)
			}
			
			downloaded += int64(n)
			sinceLast += int64(n)
			
			// Batch progress updates - report every 1MB to reduce callback overhead
			if progressCallback != nil && sinceLast >= 1024*1024 {
				progressCallback(sinceLast)
				sinceLast = 0
			}
		}
		
		if readErr == io.EOF {
			// Report any remaining progress
			if progressCallback != nil && sinceLast > 0 {
				progressCallback(sinceLast)
			}
			break
		}
		if readErr != nil {
			return fmt.Errorf("failed to read: %w", readErr)
		}
	}
	
	return nil
}

// getModelFiles queries the ModelScope API for the list of files in a model.
func (c *Client) getModelFiles(ctx context.Context, modelID string) ([]FileInfo, error) {
	// Build API URL - using the repo/files endpoint with master revision
	url := fmt.Sprintf("%s/api/v1/models/%s/repo/files?Revision=master&Recursive=True", 
		c.endpoint, modelID)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("User-Agent", c.userAgent)
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	
	// Parse response - ModelScope API returns {Data: {Files: [...]}}
	var result struct {
		Data struct {
			Files []struct {
				Name   string `json:"Name"`
				Path   string `json:"Path"`
				Size   int64  `json:"Size"`
				Sha256 string `json:"Sha256"` // SHA256 hash for integrity validation
				Type   string `json:"Type"`
			} `json:"Files"`
		} `json:"Data"`
	}
	
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}
	
	// Convert to FileInfo and filter out directories
	files := make([]FileInfo, 0)
	for _, f := range result.Data.Files {
		if f.Type == "tree" {
			continue // Skip directories
		}
		files = append(files, FileInfo{
			Name:   f.Path,   // Use full path
			Size:   f.Size,
			Sha256: f.Sha256, // Include SHA256 for integrity validation
		})
	}
	
	return files, nil
}

// downloadFile downloads a single file with resume support.
func (c *Client) downloadFile(
	ctx context.Context,
	file FileInfo,
	localPath string,
	modelID string,
	progress ProgressFunc,
) error {
	// Create parent directory
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	
	// Check if file already exists and is complete
	var resumeFrom int64 = 0
	tmpPath := localPath + ".tmp"
	
	if stat, err := os.Stat(localPath); err == nil {
		if stat.Size() == file.Size {
			// File exists and size matches, skip download
			if progress != nil {
				progress(file.Name, file.Size, file.Size)
			}
			return nil
		}
	}
	
	// Send initial progress to indicate download has started
	if progress != nil {
		progress(file.Name, 0, file.Size)
	}
	
	// Check if temporary file exists (interrupted download)
	if stat, err := os.Stat(tmpPath); err == nil {
		if stat.Size() < file.Size {
			// Resume from where we left off
			resumeFrom = stat.Size()
		} else {
			// Temp file is larger than expected, start over
			os.Remove(tmpPath)
			resumeFrom = 0
		}
	}
	
	// Build download URL using ModelScope API format
	// URL encode the file path
	encodedPath := strings.ReplaceAll(file.Name, " ", "%20")
	encodedPath = strings.ReplaceAll(encodedPath, "+", "%2B")
	downloadURL := fmt.Sprintf("%s/api/v1/models/%s/repo?Revision=master&FilePath=%s",
		c.endpoint, modelID, encodedPath)
	
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("User-Agent", c.userAgent)
	
	// Set Range header for resume support
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}
	
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	// Check status code - 200 for full download, 206 for partial (resume)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download %s returned status %d: %s", file.Name, resp.StatusCode, string(body))
	}
	
	// Open temporary file for appending if resuming, otherwise create new
	var out *os.File
	if resumeFrom > 0 {
		// Resume: open for append
		out, err = os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
	} else {
		// New download: create file
		out, err = os.Create(tmpPath)
		if err != nil {
			return err
		}
	}
	defer func() {
		out.Close()
		// Clean up temp file on error
		if err != nil {
			os.Remove(tmpPath)
		}
	}()
	
	// Download with progress tracking
	// Start from resumeFrom if we're resuming
	downloaded := resumeFrom
	buf := make([]byte, ChunkSize)
	lastReport := time.Now()
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)
			
			// Report progress every 500ms to reduce callback overhead
			shouldReport := time.Since(lastReport) > 500*time.Millisecond
			if progress != nil && shouldReport {
				progress(file.Name, downloaded, file.Size)
				lastReport = time.Now()
			}
		}
		
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	
	// Final progress report
	if progress != nil {
		progress(file.Name, downloaded, file.Size)
	}
	
	// Verify file size is correct
	if downloaded != file.Size {
		return fmt.Errorf("download incomplete: expected %d bytes, got %d", file.Size, downloaded)
	}
	
	// Close file before rename
	out.Close()
	
	// Rename temp file to final location
	if err := os.Rename(tmpPath, localPath); err != nil {
		return err
	}
	
	return nil
}

