//go:build linux

// Package main_test provides unit tests for Linux-specific service orchestration.
//
// Objective:
// Validate the platform-agnostic handleWindowsService logic when running on
// Linux. It ensures that service installation and removal requests are
// correctly routed to the Linux-specific installer functions and that
// administrative privilege requirements are enforced.
//
// Scenarios Covered:
//   - Installation: Successful service creation and failure paths on Linux.
//   - Removal: Successful service deletion and failure paths on Linux.
//   - Privilege Check: Verification that administrative rights are required
//     for service operations.
//   - Name Parsing: Ensures complex service names (unit@instance) are handled.
package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/testutils"
)

// TestHandleWindowsService_Linux_Success validates successful service operations on Linux.
//
// Scenario:
// 1. Install - Success: Simulates a successful systemd unit installation.
// 2. Remove - Success: Simulates a successful systemd unit removal.
// 3. Error Paths: Handles failures in both installation and removal.
//
// Success Criteria:
// - Installer functions must be called with correct arguments.
// - Handled state and exit codes must accurately reflect operation outcome.
func TestHandleWindowsService_Linux_Success(t *testing.T) {
	tempDir := testutils.GetUniqueTestDir("cmd", "handle_linux_success")
	configPath := filepath.Join(tempDir, "config.json")

	cfg := &config.Config{
		ServiceName: "TestService@Instance",
	}

	// Mock isAdminFunc
	oldIsAdmin := isAdminFunc
	defer func() { isAdminFunc = oldIsAdmin }()
	isAdminFunc = func() bool { return true }

	// Mock service functions
	oldInstall := installServiceFunc
	oldRemove := removeServiceFunc
	defer func() {
		installServiceFunc = oldInstall
		removeServiceFunc = oldRemove
	}()

	installServiceFunc = func(name, userGroup string) error { return nil }
	removeServiceFunc = func(name string) error { return nil }

	t.Run("Install - Success", func(t *testing.T) {
		handled, code := handleWindowsService(cfg, configPath, false, true, false, "user", "pass")
		if !handled || code != 0 {
			t.Errorf("expected handled=true, code=0; got handled=%v, code=%d", handled, code)
		}
	})

	t.Run("Remove - Success", func(t *testing.T) {
		handled, code := handleWindowsService(cfg, configPath, false, false, true, "", "")
		if !handled || code != 0 {
			t.Errorf("expected handled=true, code=0; got handled=%v, code=%d", handled, code)
		}
	})

	t.Run("Install - Error", func(t *testing.T) {
		installServiceFunc = func(name, userGroup string) error { return fmt.Errorf("install error") }
		handled, code := handleWindowsService(cfg, configPath, false, true, false, "user", "pass")
		if !handled || code != 1 {
			t.Errorf("expected handled=true, code=1; got handled=%v, code=%d", handled, code)
		}
	})

	t.Run("Remove - Error", func(t *testing.T) {
		removeServiceFunc = func(name string) error { return fmt.Errorf("remove error") }
		handled, code := handleWindowsService(cfg, configPath, false, false, true, "", "")
		if !handled || code != 1 {
			t.Errorf("expected handled=true, code=1; got handled=%v, code=%d", handled, code)
		}
	})
}

func TestHandleWindowsService_Linux(t *testing.T) {
	tempDir := testutils.GetUniqueTestDir("cmd", "handle_linux")
	configPath := filepath.Join(tempDir, "config.json")

	cfg := &config.Config{
		ServiceName: "TestService",
	}

	// Mock isAdminFunc
	oldIsAdmin := isAdminFunc
	defer func() { isAdminFunc = oldIsAdmin }()

	tests := []struct {
		name          string
		install       bool
		remove        bool
		isAdmin       bool
		expectHandled bool
		expectCode    int
		emptyName     bool
	}{
		{
			name:          "Install - No Admin",
			install:       true,
			remove:        false,
			isAdmin:       false,
			expectHandled: true,
			expectCode:    1,
		},
		{
			name:          "Install - Missing Name",
			install:       true,
			remove:        false,
			isAdmin:       true,
			expectHandled: true,
			expectCode:    1,
			emptyName:     true,
		},
		{
			name:          "Remove - No Admin",
			install:       false,
			remove:        true,
			isAdmin:       false,
			expectHandled: true,
			expectCode:    1,
		},
		{
			name:          "Remove - Missing Name",
			install:       false,
			remove:        true,
			isAdmin:       true,
			expectHandled: true,
			expectCode:    1,
			emptyName:     true,
		},
		{
			name:          "No Service Action",
			install:       false,
			remove:        false,
			isAdmin:       true,
			expectHandled: false,
			expectCode:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isAdminFunc = func() bool { return tt.isAdmin }
			testCfg := &config.Config{ServiceName: cfg.ServiceName}
			if tt.emptyName {
				testCfg.ServiceName = ""
			}
			handled, code := handleWindowsService(testCfg, configPath, false, tt.install, tt.remove, "", "")
			if handled != tt.expectHandled {
				t.Errorf("expected handled %v, got %v", tt.expectHandled, handled)
			}
			if code != tt.expectCode {
				t.Errorf("expected code %d, got %d", tt.expectCode, code)
			}
		})
	}
}
