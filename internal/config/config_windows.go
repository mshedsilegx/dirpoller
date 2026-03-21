//go:build windows

// Package config (Windows) provides Windows-specific configuration validation logic.
//
// Objective:
// Enforce Windows-specific security and configuration rules, specifically
// regarding the storage and retrieval of the master encryption key using
// environment variables.
//
// Core Functionality:
// - Master Key Validation: Ensures file-based keys are not used (Linux-only).
// - Default Naming: Automatically sets the default environment variable name to SECRETPROTECTOR_KEY.
//
// Data Flow:
// 1. Validation: Inspects the SFTP configuration for platform-incompatible fields.
// 2. Defaulting: Populates the MasterKeyEnv if left empty by the user.
package config

import (
	"fmt"
)

// validatePlatformSFTP enforces Windows-specific SFTP security constraints.
//
// Logic:
// 1. Master Key: Forbids file-based master keys (Linux-only).
// 2. Defaulting: Sets the default environment variable name to SECRETPROTECTOR_KEY.
func validatePlatformSFTP(cfg *Config) error {
	if cfg.Action.SFTP.MasterKeyFile != "" {
		return fmt.Errorf("master_key_file is not supported on Windows; environment variable (default SECRETPROTECTOR_KEY) must be used")
	}
	if cfg.Action.SFTP.MasterKeyEnv == "" {
		cfg.Action.SFTP.MasterKeyEnv = "SECRETPROTECTOR_KEY"
	}
	return nil
}
