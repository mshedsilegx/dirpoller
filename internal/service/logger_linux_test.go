//go:build linux

// Package service_test provides unit tests for the Linux-specific platform logger.
//
// Objective:
// Validate the integration with the Linux system logger (syslog), ensuring
// that Info and Error messages are correctly dispatched and that the
// connection can be closed cleanly.
//
// Scenarios Covered:
// - Platform Logging: Verification of message dispatch to syslog.
// - Lifecycle: Testing of logger creation and cleanup.
package service

import (
	"testing"
)

// TestLinuxLogger verifies the basic functionality of the Linux syslog logger.
//
// Scenario:
// 1. Create a NewPlatformLogger for Linux.
// 2. Dispatch an Info message.
// 3. Dispatch an Error message.
// 4. Close the logger.
//
// Success Criteria:
// - All operations must complete without error.
func TestLinuxLogger(t *testing.T) {
	logger, err := NewPlatformLogger("TestLogger", false)
	if err != nil {
		t.Fatalf("failed to create platform logger: %v", err)
	}

	if err := logger.Info(100, "test info message"); err != nil {
		t.Errorf("failed to log info: %v", err)
	}

	if err := logger.Error(200, "test error message"); err != nil {
		t.Errorf("failed to log error: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Errorf("failed to close logger: %v", err)
	}
}
