// Package poller defines the interface and implementations for directory scanning strategies.
package poller

import (
	"context"
	"os"
)

// Poller defines the interface for different polling algorithms.
type Poller interface {
	// Start begins the polling process and sends discovered files to the channel.
	// It should block until the context is cancelled or a fatal error occurs.
	Start(ctx context.Context, results chan<- []string) error
}

// OSUtils defines the interface for operating system specific file operations.
type OSUtils interface {
	// IsLocked returns true if the file is currently locked by another process.
	IsLocked(path string) (bool, error)
	// HasSubfolders returns true if any subdirectories exist within the given path.
	HasSubfolders(path string) (bool, error)
	// GetFiles retrieves a list of all files in the directory.
	GetFiles(dir string) ([]string, error)
	// Stat returns os.FileInfo for the given path.
	Stat(path string) (os.FileInfo, error)
}

// NewOSUtils creates an OSUtils implementation for the current platform.
func NewOSUtils() OSUtils {
	return newOSUtils()
}
