// Package service (Logger Common) provides the platform-agnostic logging interface.
//
// Objective:
// Define a universal logging API that allows the core Engine to report
// high-severity and operational events regardless of the underlying OS.
//
// Core Components:
// - Logger Interface: Defines the standard contract for Error, Info, and lifecycle management.
// - NewPlatformLogger: Factory function that returns the OS-specific implementation.
//
// Data Flow:
// 1. Initialization: The Engine requests a platform-native logger during bootstrap.
// 2. Routing: Application events are dispatched through the interface to the native OS log.
package service

// Logger defines the interface for system-level event logging.
//
// Objective: Provide a unified way to log application-level events to
// platform-native system logs (Windows EventLog or Linux Syslog).
type Logger interface {
	// Error logs a high-severity event with a specific numeric ID.
	Error(id uint32, msg string) error
	// Info logs an operational event with a specific numeric ID.
	Info(id uint32, msg string) error
	// Close releases any platform-native resources associated with the logger.
	Close() error
}

// NewPlatformLogger creates a logger appropriate for the current operating system.
//
// Objective: Abstract the selection of the platform-native logging implementation
// based on build constraints.
func NewPlatformLogger(name string, isService bool) (Logger, error) {
	return newPlatformLogger(name, isService)
}
