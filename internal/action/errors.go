// Package action (Errors) defines custom error types for file processing operations.
//
// Objective:
// Provide structured, categorised error reporting for SFTP transfers and
// local script executions. These errors allow the Engine and Logger to
// distinguish between transient network issues, authentication failures,
// and execution errors.
//
// Core Functionality:
// - SFTP Errors: Track connection loss and authentication failures during remote transfers.
// - Script Errors: Capture exit codes and combined output from local process execution.
//
// Data Flow:
// 1. Detection: Handlers (SFTP/Script) encounter a failure during execution.
// 2. Wrapping: Low-level errors are wrapped into these structured types.
// 3. Propagation: Errors are returned to the Engine and eventually processed by the CustomLogger for auditing.
package action

import "fmt"

// ErrConnectionLost is returned when an SFTP/SSH connection is unexpectedly closed.
type ErrConnectionLost struct {
	Err error
}

func (e *ErrConnectionLost) Error() string {
	return fmt.Sprintf("[Action:SFTP] connection lost: %v", e.Err)
}

func (e *ErrConnectionLost) Unwrap() error {
	return e.Err
}

// ErrAuthenticationFailed is returned when SFTP/SSH authentication fails.
type ErrAuthenticationFailed struct {
	User string
	Err  error
}

func (e *ErrAuthenticationFailed) Error() string {
	return fmt.Sprintf("[Action:SFTP] authentication failed for user %s: %v", e.User, e.Err)
}

func (e *ErrAuthenticationFailed) Unwrap() error {
	return e.Err
}

// ErrExecutionFailed is returned when a local script execution fails.
type ErrExecutionFailed struct {
	Path   string
	Output string
	Err    error
}

func (e *ErrExecutionFailed) Error() string {
	return fmt.Sprintf("[Action:Script] execution failed for %s: %v, output: %s", e.Path, e.Err, e.Output)
}

func (e *ErrExecutionFailed) Unwrap() error {
	return e.Err
}
