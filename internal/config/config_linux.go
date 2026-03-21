//go:build linux

// Package config (Linux) provides Linux-specific configuration validation logic.
//
// Objective:
// Enforce Linux-specific security and configuration rules, specifically
// regarding the storage and retrieval of the master encryption key.
//
// Core Functionality:
// - Master Key Validation: Ensures environment-based keys are not used (Windows-only).
// - Default Pathing: Automatically sets the default master key file path to the user's home directory.
//
// Data Flow:
// 1. Validation: Inspects the SFTP configuration for platform-incompatible fields.
// 2. Defaulting: Populates the MasterKeyFile if left empty by the user.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// validatePlatformSFTP enforces Linux-specific SFTP security constraints.
//
// Logic:
// 1. Master Key: Forbids environment-based master keys (Windows-only).
// 2. Defaulting: Sets the default master key file to ${HOME}/.secretprotector.key if not specified.
func validatePlatformSFTP(cfg *Config) error {
	if cfg.Action.SFTP.MasterKeyEnv != "" {
		return fmt.Errorf("master_key_env is not supported on Linux; master key file must be used")
	}
	if cfg.Action.SFTP.MasterKeyFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get user home directory for default master_key_file: %w", err)
		}
		cfg.Action.SFTP.MasterKeyFile = filepath.Join(home, ".secretprotector.key")
	}
	return nil
}
