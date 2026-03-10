// Package service provides the core engine and platform-agnostic service logic.
package service

// Logger defines the interface for system-level event logging.
type Logger interface {
	Error(id uint32, msg string) error
	Info(id uint32, msg string) error
	Close() error
}

// NewPlatformLogger creates a logger appropriate for the current operating system.
// The implementation is provided in platform-specific files (e.g., logger_windows.go).
func NewPlatformLogger(name string, isService bool) (Logger, error) {
	return newPlatformLogger(name, isService)
}
