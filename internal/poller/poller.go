// Package poller defines the interface and implementations for directory scanning strategies.
// It provides multiple algorithms for discovering files, from simple intervals to real-time events.
package poller

import (
	"context"
	"os"
)

// Poller defines the interface for different polling algorithms.
// Each implementation must be non-blocking in its setup and send batches of discovered files
// to the results channel for downstream processing.
type Poller interface {
	// Start begins the polling process and sends discovered files to the channel.
	// It should block until the context is cancelled or a fatal error occurs.
	// Implementations are responsible for enforcing the non-recursive directory constraint.
	Start(ctx context.Context, results chan<- []string) error
}

// OSUtils defines the interface for operating system specific file operations.
// This allows for platform-native implementations (e.g., Windows-specific lock detection)
// while keeping the core poller logic platform-agnostic.
type OSUtils interface {
	// IsLocked returns true if the file is currently locked by another process.
	// On Windows, this typically uses CreateFile with FILE_SHARE_NONE.
	IsLocked(path string) (bool, error)

	// HasSubfolders returns true if any subdirectories exist within the given path.
	// This is used to enforce the non-recursive requirement specified in Section 1.
	HasSubfolders(path string) (bool, error)

	// GetFiles retrieves a list of all files in the directory.
	// It should return an error if a subfolder is detected to ensure non-recursive behavior.
	GetFiles(dir string) ([]string, error)

	// Stat returns os.FileInfo for the given path.
	Stat(path string) (os.FileInfo, error)
}

// NewOSUtils creates an OSUtils implementation for the current platform.
// The actual implementation is determined by build tags (e.g., poller_windows.go).
func NewOSUtils() OSUtils {
	return newOSUtils()
}
