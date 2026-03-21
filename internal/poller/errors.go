// Package poller (Errors) defines custom error types for directory monitoring.
//
// Objective:
// Provide structured error reporting for the discovery and monitoring phases.
// It ensures that violations of the non-recursive constraint or failures in
// the OS-native watcher are clearly identifiable for higher-level recovery.
//
// Core Functionality:
// - Discovery Errors: Handle violations of directory structure constraints.
// - Watcher Errors: Handle lifecycle and runtime failures of the fsnotify engine.
package poller

import "fmt"

// ErrSubfolderDetected is returned when a subfolder is found in the poll directory.
type ErrSubfolderDetected struct {
	Path string
}

func (e *ErrSubfolderDetected) Error() string {
	return fmt.Sprintf("[Poller:Discovery] subfolder detected: %s", e.Path)
}

// ErrWatcherInitialization is returned when the file system watcher fails to start.
type ErrWatcherInitialization struct {
	Err error
}

func (e *ErrWatcherInitialization) Error() string {
	return fmt.Sprintf("[Poller:Watcher] initialization failed: %v", e.Err)
}

func (e *ErrWatcherInitialization) Unwrap() error {
	return e.Err
}

// ErrWatcherRuntime is returned when the watcher encounters an error during operation.
type ErrWatcherRuntime struct {
	Err error
}

func (e *ErrWatcherRuntime) Error() string {
	return fmt.Sprintf("[Poller:Watcher] runtime error: %v", e.Err)
}

func (e *ErrWatcherRuntime) Unwrap() error {
	return e.Err
}
