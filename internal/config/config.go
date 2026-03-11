// Package config handles JSON configuration parsing, validation, and default value management.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// PollAlgorithm defines the supported directory scanning strategies.
type PollAlgorithm string

const (
	// PollInterval triggers a scan every N seconds.
	PollInterval PollAlgorithm = "interval"
	// PollBatch triggers a scan when a file count threshold is reached.
	PollBatch PollAlgorithm = "batch"
	// PollEvent uses OS-native events (ReadDirectoryChangesW) for real-time detection.
	PollEvent PollAlgorithm = "event"
	// PollTrigger waits for a specific trigger file to appear.
	PollTrigger PollAlgorithm = "trigger"
)

// IntegrityAlgorithm defines the methods used to ensure a file is fully written and consistent.
type IntegrityAlgorithm string

const (
	// IntegrityHash verifies file consistency using xxHash-64.
	IntegrityHash IntegrityAlgorithm = "hash"
	// IntegrityTimestamp verifies consistency by checking the Last Modified timestamp.
	IntegrityTimestamp IntegrityAlgorithm = "timestamp"
	// IntegritySize verifies consistency by checking the file size.
	IntegritySize IntegrityAlgorithm = "size"
)

// ActionType defines the primary operation to perform on discovered and verified files.
type ActionType string

const (
	// ActionSFTP performs a multi-threaded upload to a remote server.
	ActionSFTP ActionType = "sftp"
	// ActionScript executes a local script with the file path as an argument.
	ActionScript ActionType = "script"
)

// PostAction defines what happens to the local file after a successful Action.
type PostAction string

const (
	// PostActionDelete removes the file immediately.
	PostActionDelete PostAction = "delete"
	// PostActionMoveArchive moves the file to a datestamped subfolder.
	PostActionMoveArchive PostAction = "move_archive"
	// PostActionMoveCompress adds the file to a consolidated multi-threaded zstd archive.
	PostActionMoveCompress PostAction = "move_compress"
)

// Config represents the root configuration structure for the DirPoller application.
type Config struct {
	ServiceName string          `json:"service_name,omitempty"`
	Poll        PollConfig      `json:"poll"`
	Integrity   IntegrityConfig `json:"integrity"`
	Action      ActionConfig    `json:"action"`
	Logging     []LoggingConfig `json:"logging,omitempty"`
}

// LoggingConfig contains parameters for the custom logging facility.
type LoggingConfig struct {
	LogName      string `json:"log_name"`
	LogRetention int    `json:"log_retention"` // in days
}

// PollConfig contains parameters for the directory scanning engine.
type PollConfig struct {
	Directory           string        `json:"directory"`
	Algorithm           PollAlgorithm `json:"algorithm"`
	Value               interface{}   `json:"value"` // Interval/Batch count (int) or Trigger pattern (string)
	BatchTimeoutSeconds int           `json:"batch_timeout_seconds"`
}

// IntegrityConfig contains parameters for the file verification logic.
type IntegrityConfig struct {
	Algorithm            IntegrityAlgorithm `json:"algorithm"`
	VerificationAttempts int                `json:"attempts"`
	VerificationInterval int                `json:"interval"`
}

// ActionConfig contains parameters for the upload or execution phase.
type ActionConfig struct {
	Type                  ActionType        `json:"type"`
	ConcurrentConnections int               `json:"concurrent_connections"`
	PostProcess           PostProcessConfig `json:"post_process"`
	SFTP                  SFTPConfig        `json:"sftp,omitempty"`
	Script                ScriptConfig      `json:"script,omitempty"`
}

// PostProcessConfig contains parameters for the file lifecycle after action execution.
type PostProcessConfig struct {
	Action      PostAction `json:"action"`
	ArchivePath string     `json:"archive_path,omitempty"`
}

// SFTPConfig contains credentials and path information for SFTP transfers.
type SFTPConfig struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Username         string `json:"username"`
	Password         string `json:"password,omitempty"`           // #nosec G117 - Authentication password
	SSHKeyPath       string `json:"ssh_key_path,omitempty"`       // Path to private key
	SSHKeyPassphrase string `json:"ssh_key_passphrase,omitempty"` // #nosec G117 - Passphrase to decrypt the private key
	HostKey          string `json:"host_key,omitempty"`           // Base64 encoded public host key
	RemotePath       string `json:"remote_path"`
}

// ScriptConfig contains parameters for executing external logic.
type ScriptConfig struct {
	Path           string `json:"path"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// LoadConfig reads, unmarshals, and validates the JSON configuration file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path)) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	setDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "DirPoller"
	}
	if cfg.Poll.Algorithm == "" {
		cfg.Poll.Algorithm = PollInterval
	}
	if (cfg.Poll.Algorithm == PollBatch || cfg.Poll.Algorithm == PollTrigger) && cfg.Poll.BatchTimeoutSeconds == 0 {
		cfg.Poll.BatchTimeoutSeconds = 600 // 10 minutes default
	}

	if cfg.Integrity.Algorithm == "" {
		cfg.Integrity.Algorithm = IntegrityTimestamp
	}
	if cfg.Integrity.VerificationAttempts == 0 {
		cfg.Integrity.VerificationAttempts = 3
	}
	if cfg.Integrity.VerificationInterval == 0 {
		cfg.Integrity.VerificationInterval = 5
	}

	if cfg.Action.ConcurrentConnections == 0 {
		cfg.Action.ConcurrentConnections = runtime.NumCPU() * 2
	}

	if cfg.Action.PostProcess.Action == "" {
		cfg.Action.PostProcess.Action = PostActionDelete
	}
}

func validate(cfg *Config) error {
	if cfg.Poll.Directory == "" {
		return fmt.Errorf("poll directory is required")
	}
	if _, err := os.Stat(cfg.Poll.Directory); err != nil {
		return fmt.Errorf("poll directory does not exist or is inaccessible: %w", err)
	}

	switch cfg.Poll.Algorithm {
	case PollInterval, PollBatch, PollEvent, PollTrigger:
	default:
		return fmt.Errorf("unsupported poll algorithm: %s", cfg.Poll.Algorithm)
	}

	if cfg.Poll.Algorithm == PollTrigger {
		if _, ok := cfg.Poll.Value.(string); !ok {
			return fmt.Errorf("trigger algorithm requires a string value for file pattern")
		}
	}

	switch cfg.Integrity.Algorithm {
	case IntegrityHash, IntegrityTimestamp, IntegritySize:
	default:
		return fmt.Errorf("unsupported integrity algorithm: %s", cfg.Integrity.Algorithm)
	}

	switch cfg.Action.Type {
	case ActionSFTP:
		if cfg.Action.SFTP.Host == "" || cfg.Action.SFTP.Username == "" {
			return fmt.Errorf("SFTP host and username are required")
		}
		// Enforce at least one authentication method: Password, Key, or both (MFA)
		if cfg.Action.SFTP.Password == "" && cfg.Action.SFTP.SSHKeyPath == "" {
			return fmt.Errorf("SFTP authentication requires at least a password or an SSH key")
		}
		if cfg.Action.SFTP.Port == 0 {
			cfg.Action.SFTP.Port = 22
		}
	case ActionScript:
		if cfg.Action.Script.Path == "" {
			return fmt.Errorf("script path is required")
		}
		// Security: Validate script path exists and is absolute
		if !filepath.IsAbs(cfg.Action.Script.Path) {
			return fmt.Errorf("script path must be an absolute path")
		}
		if _, err := os.Stat(cfg.Action.Script.Path); err != nil {
			return fmt.Errorf("script path does not exist: %w", err)
		}
	default:
		return fmt.Errorf("unsupported action type: %s", cfg.Action.Type)
	}

	return nil
}
