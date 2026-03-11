package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS", "config")
	_ = os.MkdirAll(testBaseDir, 0750)

	code := m.Run()
	os.Exit(code)
}

var testBaseDir string

func getTestDir(name string) string {
	dir := filepath.Join(testBaseDir, name)
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0750)
	return dir
}

func TestConfigValidation(t *testing.T) {
	testDir := getTestDir("Validation")

	t.Run("ValidConfig", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{
				Directory: testDir,
				Algorithm: PollInterval,
				Value:     1,
			},
			Integrity: IntegrityConfig{
				Algorithm:            IntegritySize,
				VerificationAttempts: 1,
				VerificationInterval: 1,
			},
			Action: ActionConfig{
				Type: ActionSFTP,
				PostProcess: PostProcessConfig{
					Action: PostActionDelete,
				},
				SFTP: SFTPConfig{
					Host:     "localhost",
					Username: "user",
					Password: "pass",
				},
			},
		}
		if err := validate(cfg); err != nil {
			t.Errorf("expected no error for valid config, got: %v", err)
		}
	})

	t.Run("MissingDirectory", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{
				Directory: "",
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for missing directory, got nil")
		}
	})

	t.Run("InvalidAlgorithm", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{
				Directory: testDir,
				Algorithm: "invalid",
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for invalid algorithm, got nil")
		}
	})

	t.Run("SFTPMissingHost", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{
				Type: ActionSFTP,
				SFTP: SFTPConfig{Username: "user", Password: "p"},
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for missing SFTP host, got nil")
		}
	})

	t.Run("SFTPMissingAuth", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{
				Type: ActionSFTP,
				SFTP: SFTPConfig{Host: "h", Username: "u"},
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for missing SFTP auth, got nil")
		}
	})

	t.Run("ScriptMissingPath", func(t *testing.T) {
		cfg := &Config{
			Poll:   PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{Type: ActionScript},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for missing script path, got nil")
		}
	})

	t.Run("ScriptNotAbsolute", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{
				Type:   ActionScript,
				Script: ScriptConfig{Path: "relative/path.bat"},
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for relative script path, got nil")
		}
	})

	t.Run("UnsupportedAction", func(t *testing.T) {
		cfg := &Config{
			Poll:   PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{Type: "invalid"},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for unsupported action type, got nil")
		}
	})

	t.Run("SFTPValidMFA", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{Directory: testDir, Algorithm: PollInterval},
			Integrity: IntegrityConfig{
				Algorithm: IntegritySize,
			},
			Action: ActionConfig{
				Type: ActionSFTP,
				SFTP: SFTPConfig{
					Host:       "h",
					Username:   "u",
					Password:   "p",
					SSHKeyPath: testDir, // Not a real key but path exists
				},
			},
		}
		if err := validate(cfg); err != nil {
			t.Errorf("expected no error for SFTP with both password and key, got: %v", err)
		}
	})

	t.Run("SFTPPortZeroDefaultsTo22", func(t *testing.T) {
		cfg := &Config{
			Poll:      PollConfig{Directory: testDir, Algorithm: PollInterval},
			Integrity: IntegrityConfig{Algorithm: IntegritySize},
			Action: ActionConfig{
				Type: ActionSFTP,
				SFTP: SFTPConfig{Host: "h", Username: "u", Password: "p", Port: 0},
			},
		}
		if err := validate(cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Action.SFTP.Port != 22 {
			t.Errorf("expected port 22, got %d", cfg.Action.SFTP.Port)
		}
	})

	t.Run("IntegrityAlgorithms", func(t *testing.T) {
		algorithms := []IntegrityAlgorithm{IntegrityHash, IntegrityTimestamp, IntegritySize}
		for _, algo := range algorithms {
			cfg := &Config{
				Poll:      PollConfig{Directory: testDir, Algorithm: PollInterval},
				Integrity: IntegrityConfig{Algorithm: algo},
				Action: ActionConfig{
					Type: ActionSFTP,
					SFTP: SFTPConfig{Host: "h", Username: "u", Password: "p"},
				},
			}
			if err := validate(cfg); err != nil {
				t.Errorf("expected no error for algo %s, got %v", algo, err)
			}
		}
	})

	t.Run("ScriptPathNotFound", func(t *testing.T) {
		cfg := &Config{
			Poll: PollConfig{Directory: testDir, Algorithm: PollInterval},
			Action: ActionConfig{
				Type:   ActionScript,
				Script: ScriptConfig{Path: filepath.Join(testDir, "non_existent.bat")},
			},
		}
		if err := validate(cfg); err == nil {
			t.Error("expected error for non-existent script path, got nil")
		}
	})
}

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{
		Poll: PollConfig{
			Algorithm: PollBatch,
		},
	}
	setDefaults(cfg)

	if cfg.ServiceName != "DirPoller" {
		t.Errorf("expected ServiceName 'DirPoller', got %s", cfg.ServiceName)
	}
	if cfg.Poll.BatchTimeoutSeconds != 600 {
		t.Errorf("expected BatchTimeoutSeconds 600, got %d", cfg.Poll.BatchTimeoutSeconds)
	}
	if cfg.Integrity.VerificationAttempts != 3 {
		t.Errorf("expected VerificationAttempts 3, got %d", cfg.Integrity.VerificationAttempts)
	}

	t.Run("DefaultIntervalAlgorithm", func(t *testing.T) {
		cfg := &Config{}
		setDefaults(cfg)
		if cfg.Poll.Algorithm != PollInterval {
			t.Errorf("expected default algorithm %s, got %s", PollInterval, cfg.Poll.Algorithm)
		}
		if cfg.Integrity.Algorithm != IntegrityTimestamp {
			t.Errorf("expected default integrity %s, got %s", IntegrityTimestamp, cfg.Integrity.Algorithm)
		}
		if cfg.Action.ConcurrentConnections <= 0 {
			t.Errorf("expected default connections > 0, got %d", cfg.Action.ConcurrentConnections)
		}
	})

	t.Run("DefaultBatchTimeout", func(t *testing.T) {
		cfg := &Config{Poll: PollConfig{Algorithm: PollBatch}}
		setDefaults(cfg)
		if cfg.Poll.BatchTimeoutSeconds != 600 {
			t.Errorf("expected 600, got %d", cfg.Poll.BatchTimeoutSeconds)
		}
	})

	t.Run("ExhaustiveDefaults", func(t *testing.T) {
		cfg := &Config{}
		setDefaults(cfg)
		// Check every field that has a default
		if cfg.ServiceName != "DirPoller" {
			t.Error("ServiceName default failed")
		}
		if cfg.Poll.Algorithm != PollInterval {
			t.Error("Poll Algorithm default failed")
		}
		if cfg.Integrity.Algorithm != IntegrityTimestamp {
			t.Error("Integrity Algorithm default failed")
		}
		if cfg.Integrity.VerificationAttempts != 3 {
			t.Error("VerificationAttempts default failed")
		}
		if cfg.Integrity.VerificationInterval != 5 {
			t.Error("VerificationInterval default failed")
		}
		if cfg.Action.ConcurrentConnections <= 0 {
			t.Error("ConcurrentConnections default failed")
		}
	})
}

func TestLoadConfig(t *testing.T) {
	testDir := getTestDir("config_load")
	defer func() { _ = os.RemoveAll(testDir) }()

	cfgFile := filepath.Join(testDir, "config.json")
	content := `{
		"poll": { "directory": "` + filepath.ToSlash(testDir) + `", "algorithm": "interval", "value": 1 },
		"integrity": { "attempts": 1, "interval": 1, "algorithm": "size" },
		"action": { "type": "sftp", "sftp": { "host": "localhost", "username": "user", "password": "pass", "post_action": "delete" } }
	}`
	_ = os.WriteFile(cfgFile, []byte(content), 0644)

	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Compare using filepath.Clean to handle slash differences on Windows
	if filepath.Clean(cfg.Poll.Directory) != filepath.Clean(testDir) {
		t.Errorf("expected directory %s, got %s", testDir, cfg.Poll.Directory)
	}

	t.Run("MalformedJSON", func(t *testing.T) {
		badFile := filepath.Join(testDir, "bad.json")
		_ = os.WriteFile(badFile, []byte(`{ "poll": { "directory": "C:\" } }`), 0644) // Invalid escape/trailing backslash
		_, err := LoadConfig(badFile)
		if err == nil {
			t.Error("expected error for malformed JSON, got nil")
		}
	})

	t.Run("FileNotFound", func(t *testing.T) {
		_, err := LoadConfig(filepath.Join(testDir, "missing.json"))
		if err == nil {
			t.Error("expected error for missing file, got nil")
		}
	})
}
