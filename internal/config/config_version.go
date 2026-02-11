// Package config provides configuration version management functionality.
//
// This file implements configuration package management including:
//   - Fetching and parsing package registry (packages.json)
//   - Downloading configuration packages
//   - Version compatibility checking
//   - Package extraction and verification
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tsingmaoai/xw-cli/internal/logger"
)

// Package represents a configuration package version from the registry.
type Package struct {
	// Version is the package version (e.g., "v0.0.1")
	Version string `json:"version"`

	// Name is the human-readable package name
	Name string `json:"name"`

	// Description provides a brief description of this version
	Description string `json:"description"`

	// ReleaseDate is when this version was released (YYYY-MM-DD)
	ReleaseDate string `json:"release_date"`

	// DownloadURL is the URL to download the package tarball
	DownloadURL string `json:"download_url"`

	// SHA256 is the expected SHA256 checksum of the package
	SHA256 string `json:"sha256"`

	// MinXwVersion is the minimum xw binary version required
	MinXwVersion string `json:"min_xw_version"`
}

// PackageRegistry represents the structure of packages.json
type PackageRegistry struct {
	// Schema is the JSON schema URL
	Schema string `json:"$schema,omitempty"`

	// Name is the registry name
	Name string `json:"name"`

	// Description describes the registry
	Description string `json:"description"`

	// UpdatedAt is when the registry was last updated
	UpdatedAt string `json:"updated_at"`

	// Packages is the list of available configuration packages
	Packages []Package `json:"packages"`
}

// VersionManager handles configuration version operations.
type VersionManager struct {
	config   *Config
	registry *PackageRegistry
	client   *http.Client
}

// parseVersion parses a version string into major, minor, patch components.
// Supports formats like "v0.0.1", "0.0.1", "v1.2.3-alpha".
// Returns the numeric components and ignores pre-release/build metadata.
func parseVersion(version string) (major, minor, patch int, err error) {
	// Remove 'v' prefix if present
	v := strings.TrimPrefix(version, "v")
	
	// Split on '-' to ignore pre-release/build metadata
	parts := strings.Split(v, "-")
	v = parts[0]
	
	// Split version components
	components := strings.Split(v, ".")
	if len(components) < 1 {
		return 0, 0, 0, fmt.Errorf("invalid version format: %s", version)
	}
	
	// Parse major
	if len(components) >= 1 {
		major, err = strconv.Atoi(components[0])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid major version: %s", components[0])
		}
	}
	
	// Parse minor
	if len(components) >= 2 {
		minor, err = strconv.Atoi(components[1])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid minor version: %s", components[1])
		}
	}
	
	// Parse patch
	if len(components) >= 3 {
		patch, err = strconv.Atoi(components[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("invalid patch version: %s", components[2])
		}
	}
	
	return major, minor, patch, nil
}

// compareVersions compares two version strings.
// Returns:
//   -1 if v1 < v2
//    0 if v1 == v2
//    1 if v1 > v2
func compareVersions(v1, v2 string) (int, error) {
	major1, minor1, patch1, err := parseVersion(v1)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %s: %w", v1, err)
	}
	
	major2, minor2, patch2, err := parseVersion(v2)
	if err != nil {
		return 0, fmt.Errorf("failed to parse version %s: %w", v2, err)
	}
	
	// Compare major
	if major1 != major2 {
		if major1 < major2 {
			return -1, nil
		}
		return 1, nil
	}
	
	// Compare minor
	if minor1 != minor2 {
		if minor1 < minor2 {
			return -1, nil
		}
		return 1, nil
	}
	
	// Compare patch
	if patch1 != patch2 {
		if patch1 < patch2 {
			return -1, nil
		}
		return 1, nil
	}
	
	return 0, nil
}

// isVersionGreaterOrEqual checks if v1 >= v2.
func isVersionGreaterOrEqual(v1, v2 string) (bool, error) {
	cmp, err := compareVersions(v1, v2)
	if err != nil {
		return false, err
	}
	return cmp >= 0, nil
}

// NewVersionManager creates a new version manager.
func NewVersionManager(cfg *Config) *VersionManager {
	return &VersionManager{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchRegistry fetches and parses the package registry from the configured URL.
//
// Returns:
//   - The parsed registry or an error if fetching/parsing fails
func (vm *VersionManager) FetchRegistry() (*PackageRegistry, error) {
	// Get registry URL from server identity
	identity, err := vm.config.GetOrCreateServerIdentity()
	if err != nil {
		return nil, fmt.Errorf("failed to get server identity: %w", err)
	}

	registryURL := identity.Registry
	logger.Debug("Fetching package registry from: %s", registryURL)

	// Fetch registry
	resp, err := vm.client.Get(registryURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned status %d", resp.StatusCode)
	}

	// Parse JSON
	var registry PackageRegistry
	if err := json.NewDecoder(resp.Body).Decode(&registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry JSON: %w", err)
	}

	vm.registry = &registry
	logger.Debug("Fetched %d packages from registry", len(registry.Packages))

	return &registry, nil
}

// GetLatestCompatibleVersion returns the latest version compatible with the current binary.
//
// Parameters:
//   - binaryVersion: Current xw binary version (e.g., "v0.0.1")
//
// Returns:
//   - The latest compatible package or an error if none found
func (vm *VersionManager) GetLatestCompatibleVersion(binaryVersion string) (*Package, error) {
	if vm.registry == nil {
		return nil, fmt.Errorf("registry not loaded, call FetchRegistry first")
	}

	compatible, err := vm.GetCompatibleVersions(binaryVersion)
	if err != nil {
		return nil, err
	}

	if len(compatible) == 0 {
		return nil, fmt.Errorf("no compatible versions found for xw %s", binaryVersion)
	}

	// Return the first one (already sorted, newest first)
	return &compatible[0], nil
}

// GetCompatibleVersions returns all versions compatible with the given binary version,
// sorted from newest to oldest.
//
// Parameters:
//   - binaryVersion: Current xw binary version
//
// Returns:
//   - Slice of compatible packages sorted by version (newest first)
func (vm *VersionManager) GetCompatibleVersions(binaryVersion string) ([]Package, error) {
	if vm.registry == nil {
		return nil, fmt.Errorf("registry not loaded")
	}

	var compatible []Package
	for _, pkg := range vm.registry.Packages {
		// Check if current version meets minimum requirement
		isCompat, err := isVersionGreaterOrEqual(binaryVersion, pkg.MinXwVersion)
		if err != nil {
			logger.Warn("Skipping package %s: invalid version comparison: %v", 
				pkg.Version, err)
			continue
		}

		if isCompat {
			compatible = append(compatible, pkg)
		}
	}

	// Sort by version (newest first)
	sort.Slice(compatible, func(i, j int) bool {
		cmp, err := compareVersions(compatible[i].Version, compatible[j].Version)
		if err != nil {
			return false
		}
		return cmp > 0
	})

	return compatible, nil
}

// FindPackage finds a specific package version in the registry.
//
// Parameters:
//   - version: The version to find (e.g., "v0.0.1")
//
// Returns:
//   - The package or an error if not found
func (vm *VersionManager) FindPackage(version string) (*Package, error) {
	if vm.registry == nil {
		return nil, fmt.Errorf("registry not loaded")
	}

	for _, pkg := range vm.registry.Packages {
		if pkg.Version == version {
			return &pkg, nil
		}
	}

	return nil, fmt.Errorf("version %s not found in registry", version)
}

// IsVersionCompatible checks if a package version is compatible with the binary version.
//
// Parameters:
//   - pkg: The package to check
//   - binaryVersion: Current xw binary version
//
// Returns:
//   - true if compatible, false otherwise
func (vm *VersionManager) IsVersionCompatible(pkg *Package, binaryVersion string) (bool, error) {
	return isVersionGreaterOrEqual(binaryVersion, pkg.MinXwVersion)
}

// DownloadPackage downloads and extracts a configuration package.
//
// Parameters:
//   - pkg: The package to download
//
// Returns:
//   - nil on success, error on failure
func (vm *VersionManager) DownloadPackage(pkg *Package) error {
	logger.Info("Downloading configuration %s from %s", pkg.Version, pkg.DownloadURL)

	// Create temp file for download
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("xw-config-%s-*.tar.gz", pkg.Version))
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Download package
	resp, err := vm.client.Get(pkg.DownloadURL)
	if err != nil {
		return fmt.Errorf("failed to download package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Write to temp file and calculate SHA256
	hash := sha256.New()
	writer := io.MultiWriter(tmpFile, hash)

	written, err := io.Copy(writer, resp.Body)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write package: %w", err)
	}
	tmpFile.Close()

	logger.Debug("Downloaded %d bytes", written)

	// Verify SHA256
	actualHash := hex.EncodeToString(hash.Sum(nil))
	if pkg.SHA256 != "" && actualHash != pkg.SHA256 {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", pkg.SHA256, actualHash)
	}
	logger.Debug("SHA256 verified: %s", actualHash)

	// Extract package (normalize version by removing "v" prefix)
	normalizedVersion := strings.TrimPrefix(pkg.Version, "v")
	destDir := filepath.Join(vm.config.Storage.ConfigDir, normalizedVersion)
	if err := vm.extractPackage(tmpPath, destDir); err != nil {
		return fmt.Errorf("failed to extract package: %w", err)
	}

	logger.Info("Configuration %s installed to %s", pkg.Version, destDir)
	return nil
}

// extractPackage extracts a tar.gz package to the destination directory.
func (vm *VersionManager) extractPackage(tarPath, destDir string) error {
	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Use tar command to extract
	// We extract to a temp directory first, then move contents
	tmpExtractDir := destDir + ".tmp"
	defer os.RemoveAll(tmpExtractDir)

	if err := os.MkdirAll(tmpExtractDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp extract directory: %w", err)
	}

	// Extract tar.gz
	cmd := fmt.Sprintf("tar -xzf %s -C %s", tarPath, tmpExtractDir)
	if output, err := execCommand(cmd); err != nil {
		return fmt.Errorf("tar extraction failed: %w\nOutput: %s", err, output)
	}

	// The tar contains a directory with the version name, move its contents
	entries, err := os.ReadDir(tmpExtractDir)
	if err != nil {
		return fmt.Errorf("failed to read extracted directory: %w", err)
	}

	if len(entries) != 1 || !entries[0].IsDir() {
		return fmt.Errorf("unexpected tar structure: expected single directory")
	}

	// Move contents from the versioned directory to destDir
	srcDir := filepath.Join(tmpExtractDir, entries[0].Name())
	srcEntries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	for _, entry := range srcEntries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(destDir, entry.Name())
		
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("failed to move %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// execCommand executes a shell command and returns output.
func execCommand(cmd string) (string, error) {
	output, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	return string(output), err
}

// IsVersionInstalled checks if a version is already installed locally.
func (vm *VersionManager) IsVersionInstalled(version string) bool {
	// Normalize version by removing "v" prefix
	normalizedVersion := strings.TrimPrefix(version, "v")
	versionDir := filepath.Join(vm.config.Storage.ConfigDir, normalizedVersion)
	info, err := os.Stat(versionDir)
	return err == nil && info.IsDir()
}

// ListInstalledVersions scans the local config directory and returns all installed versions.
//
// This method discovers configuration versions by examining the config directory
// (typically ~/.xw/) for subdirectories that look like version numbers.
//
// Returns:
//   - Slice of installed version strings (without "v" prefix)
//   - Error if the config directory cannot be read
func (vm *VersionManager) ListInstalledVersions() ([]string, error) {
	entries, err := os.ReadDir(vm.config.Storage.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read config directory: %w", err)
	}
	
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		
		name := entry.Name()
		// Skip special directories (data, cache, etc.)
		if name == "data" || name == "cache" || strings.HasPrefix(name, ".") {
			continue
		}
		
		// Version directories should match pattern like "0.0.1" or "v0.0.1"
		// Accept any directory that looks version-like
		if isVersionLike(name) {
			versions = append(versions, strings.TrimPrefix(name, "v"))
		}
	}
	
	return versions, nil
}

// isVersionLike checks if a string looks like a version number.
func isVersionLike(s string) bool {
	// Remove "v" prefix if present
	s = strings.TrimPrefix(s, "v")
	
	// Check if it starts with a digit and contains dots
	if len(s) == 0 || (s[0] < '0' || s[0] > '9') {
		return false
	}
	
	// Must contain at least one dot (e.g., "0.0" or "0.0.1")
	return strings.Contains(s, ".")
}

// GetCurrentVersion returns the currently active configuration version.
func (vm *VersionManager) GetCurrentVersion() (string, error) {
	identity, err := vm.config.GetOrCreateServerIdentity()
	if err != nil {
		return "", err
	}
	return identity.ConfigVersion, nil
}

// SwitchVersion switches to a different configuration version.
//
// This updates the config_version in server.conf but does not restart the server.
//
// Parameters:
//   - version: The version to switch to
//
// Returns:
//   - nil on success, error if the version doesn't exist locally
func (vm *VersionManager) SwitchVersion(version string) error {
	// Check if version exists locally
	if !vm.IsVersionInstalled(version) {
		return fmt.Errorf("version %s is not installed locally", version)
	}

	// Load current identity
	identity, err := vm.config.GetOrCreateServerIdentity()
	if err != nil {
		return fmt.Errorf("failed to load server identity: %w", err)
	}

	// Update config version (normalize by removing "v" prefix)
	identity.ConfigVersion = strings.TrimPrefix(version, "v")

	// Save to file
	confPath := filepath.Join(vm.config.Storage.DataDir, ServerConfFileName)
	if err := vm.config.writeServerIdentity(confPath, identity); err != nil {
		return fmt.Errorf("failed to save config version: %w", err)
	}

	logger.Info("Switched to configuration version %s", version)
	return nil
}

