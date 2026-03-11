package action

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"os"
	"path/filepath"
	"testing"
)

var testBaseDir string

func TestMain(m *testing.M) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS", "action")
	_ = os.MkdirAll(testBaseDir, 0750)

	code := m.Run()

	// _ = os.RemoveAll(testBaseDir) // Removed to avoid race conditions
	os.Exit(code)
}

func getTestDir(name string) string {
	dir := filepath.Join(testBaseDir, name)
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0750)
	return dir
}

func TestScriptAction(t *testing.T) {
	testDir := getTestDir("ScriptAction")
	testFile := filepath.Join(testDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("hello"), 0644)

	// Create a simple batch script that just exits 0
	scriptPath := filepath.Join(testDir, "test_script.bat")
	_ = os.WriteFile(scriptPath, []byte("@echo off\necho Processing %1\nexit /b 0"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 1,
			Script: config.ScriptConfig{
				Path:           scriptPath,
				TimeoutSeconds: 5,
			},
		},
	}

	h := NewScriptHandler(cfg)
	defer func() { _ = h.Close() }()

	ctx := context.Background()
	success, err := h.Execute(ctx, []string{testFile})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(success) != 1 || success[0] != testFile {
		t.Errorf("expected 1 success for %s, got %v", testFile, success)
	}
}

func TestScriptActionTimeout(t *testing.T) {
	testDir := getTestDir("ScriptTimeout")
	testFile := filepath.Join(testDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("hello"), 0644)

	// Create a script that sleeps longer than the timeout
	scriptPath := filepath.Join(testDir, "timeout_script.bat")
	_ = os.WriteFile(scriptPath, []byte("@echo off\npowershell -Command \"Start-Sleep -Seconds 10\"\nexit /b 0"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 1,
			Script: config.ScriptConfig{
				Path:           scriptPath,
				TimeoutSeconds: 1,
			},
		},
	}

	h := NewScriptHandler(cfg)
	defer func() { _ = h.Close() }()

	ctx := context.Background()
	_, err := h.Execute(ctx, []string{testFile})
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestScriptActionFailure(t *testing.T) {
	testDir := getTestDir("ScriptFailure")
	testFile := filepath.Join(testDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("hello"), 0644)

	// Create a script that exits with non-zero code
	scriptPath := filepath.Join(testDir, "fail_script.bat")
	_ = os.WriteFile(scriptPath, []byte("@echo off\necho Failing...\nexit /b 1"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 1,
			Script: config.ScriptConfig{
				Path:           scriptPath,
				TimeoutSeconds: 5,
			},
		},
	}

	h := NewScriptHandler(cfg)
	defer func() { _ = h.Close() }()

	ctx := context.Background()
	_, err := h.Execute(ctx, []string{testFile})
	if err == nil {
		t.Error("expected execution failure error, got nil")
	}
}
