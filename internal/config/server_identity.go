package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ServerConfFileName is the name of the server configuration file
	ServerConfFileName = "server.conf"
	
	// ServerNameLength is the length of the server name
	ServerNameLength = 6
)

// ServerIdentity represents the server's unique identity
type ServerIdentity struct {
	Name     string `json:"name"`
	Registry string `json:"registry"`
}

// GenerateServerName generates a random 6-character server name
// consisting of uppercase, lowercase letters and numbers
func GenerateServerName() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, ServerNameLength)
	rand.Read(b)
	
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	
	return string(b)
}

// GetOrCreateServerIdentity gets the server identity from server.conf
// or creates a new one if it doesn't exist
func (c *Config) GetOrCreateServerIdentity() (*ServerIdentity, error) {
	confPath := filepath.Join(c.Storage.DataDir, ServerConfFileName)
	
	// Check if server.conf exists
	if _, err := os.Stat(confPath); err == nil {
		// File exists, read it
		identity, err := c.readServerIdentity(confPath)
		if err != nil {
			return nil, err
		}
		
		// Check if registry is missing and add it
		needsUpdate := false
		if identity.Registry == "" {
			identity.Registry = DefaultRegistry
			needsUpdate = true
		}
		
		// Update file if needed
		if needsUpdate {
			if err := c.writeServerIdentity(confPath, identity); err != nil {
				return nil, fmt.Errorf("failed to update server identity: %w", err)
			}
		}
		
		return identity, nil
	}
	
	// File doesn't exist, create new identity
	identity := &ServerIdentity{
		Name:     GenerateServerName(),
		Registry: DefaultRegistry,
	}
	
	// Write to file
	if err := c.writeServerIdentity(confPath, identity); err != nil {
		return nil, fmt.Errorf("failed to write server identity: %w", err)
	}
	
	return identity, nil
}

// readServerIdentity reads server identity from file
func (c *Config) readServerIdentity(path string) (*ServerIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read server.conf: %w", err)
	}
	
	// Parse simple key=value format
	lines := strings.Split(string(data), "\n")
	identity := &ServerIdentity{}
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		
		if key == "name" {
			identity.Name = value
		} else if key == "registry" {
			identity.Registry = value
		}
	}
	
	if identity.Name == "" {
		return nil, fmt.Errorf("server.conf does not contain 'name' field")
	}
	
	return identity, nil
}

// writeServerIdentity writes server identity to file
func (c *Config) writeServerIdentity(path string, identity *ServerIdentity) error {
	content := fmt.Sprintf("# XW Server Configuration\n# Do not modify this file unless you know what you are doing\n\n# Server instance unique identifier\nname=%s\n\n# Configuration package registry URL\nregistry=%s\n", identity.Name, identity.Registry)
	
	return os.WriteFile(path, []byte(content), 0644)
}

// LoadServerConfig loads server configuration from server.conf
func (c *Config) LoadServerConfig() error {
	identity, err := c.GetOrCreateServerIdentity()
	if err != nil {
		return err
	}
	
	c.Server.Name = identity.Name
	c.Server.Registry = identity.Registry
	return nil
}

// SaveServerConfig saves current server configuration to server.conf
func (c *Config) SaveServerConfig() error {
	confPath := filepath.Join(c.Storage.DataDir, ServerConfFileName)
	identity := &ServerIdentity{
		Name:     c.Server.Name,
		Registry: c.Server.Registry,
	}
	return c.writeServerIdentity(confPath, identity)
}

