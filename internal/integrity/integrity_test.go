package integrity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"criticalsys.net/dirpoller/internal/config"
)

var testBaseDir string

func TestMain(m *testing.M) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS", "integrity")
	_ = os.MkdirAll(testBaseDir, 0750)

	code := m.Run()

	// os.RemoveAll(testBaseDir) // Removed to avoid race conditions
	os.Exit(code)
}

func getTestDir(name string) string {
	dir := filepath.Join(testBaseDir, name)
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0750)
	return dir
}

func TestIntegrityLockCheck(t *testing.T) {
	testDir := getTestDir("LockCheck")
	testFile := filepath.Join(testDir, "locked.txt")
	err := os.WriteFile(testFile, []byte("data"), 0644)
	if err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			VerificationAttempts: 2,
			VerificationInterval: 1,
			Algorithm:            config.IntegritySize,
		},
	}
	v := NewVerifier(cfg)

	verified, err := v.Verify(context.Background(), testFile)
	if err != nil {
		t.Errorf("Verify failed: %v", err)
	}
	if !verified {
		t.Error("expected file to be verified when not locked")
	}
}

func TestIntegrityHash(t *testing.T) {
	testDir := getTestDir("HashCheck")
	testFile := filepath.Join(testDir, "hash.txt")
	_ = os.WriteFile(testFile, []byte("initial"), 0644)

	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			VerificationAttempts: 2,
			VerificationInterval: 1,
			Algorithm:            config.IntegrityHash,
		},
	}
	v := NewVerifier(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	verified, err := v.Verify(ctx, testFile)
	if err != nil {
		t.Errorf("Verify failed: %v", err)
	}
	if !verified {
		t.Error("expected file to be verified")
	}
}

func TestIntegrityChangingFile(t *testing.T) {
	testDir := getTestDir("ChangingFile")
	testFile := filepath.Join(testDir, "changing.txt")
	_ = os.WriteFile(testFile, []byte("v1"), 0644)

	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			VerificationAttempts: 1,
			VerificationInterval: 1,
			Algorithm:            config.IntegritySize,
		},
	}
	v := NewVerifier(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start a goroutine to change the file size during the interval
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = os.WriteFile(testFile, []byte("v1-changed-size"), 0644)
	}()

	verified, err := v.Verify(ctx, testFile)
	if err != nil {
		t.Errorf("Verify failed: %v", err)
	}
	if verified {
		t.Error("expected file to NOT be verified because size changed")
	}
}

func TestIntegrityTimestamp(t *testing.T) {
	testDir := getTestDir("TimestampCheck")
	testFile := filepath.Join(testDir, "timestamp.txt")
	_ = os.WriteFile(testFile, []byte("timestamp data"), 0644)

	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			VerificationAttempts: 2,
			VerificationInterval: 1,
			Algorithm:            config.IntegrityTimestamp,
		},
	}
	v := NewVerifier(cfg)

	verified, err := v.Verify(context.Background(), testFile)
	if err != nil {
		t.Errorf("Verify failed: %v", err)
	}
	if !verified {
		t.Error("expected file to be verified")
	}
}

func TestVerifierUnsupportedAlgorithm(t *testing.T) {
	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			Algorithm: "invalid",
		},
	}
	v := NewVerifier(cfg)
	_, err := v.getIntegrityValue("any")
	if err == nil {
		t.Error("expected error for unsupported integrity algorithm, got nil")
	}
}

func TestVerifierCalculateHashError(t *testing.T) {
	testDir := getTestDir("HashError")
	cfg := &config.Config{
		Integrity: config.IntegrityConfig{
			Algorithm: config.IntegrityHash,
		},
	}
	v := NewVerifier(cfg)

	// Passing a directory to calculateHash should fail
	_, err := v.calculateHash(testDir)
	if err == nil {
		t.Error("expected error calculating hash of a directory, got nil")
	}
}
