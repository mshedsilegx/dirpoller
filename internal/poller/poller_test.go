package poller

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

var testBaseDir string

func TestMain(m *testing.M) {
	// Setup: Create %TEMP%\dirpoller_UTESTS
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS")

	// Ensure the parent directory exists
	_ = os.MkdirAll(testBaseDir, 0750)

	// Run tests
	code := m.Run()

	// Cleanup: Remove %TEMP%\dirpoller_UTESTS
	// We don't remove the whole base dir in TestMain because multiple packages
	// might be running tests in parallel using the same base dir.
	// Instead, each test cleans up its own subdirectory.

	os.Exit(code)
}

// GetTestDir returns a unique subdirectory within the test base directory.
func GetTestDir(name string) (string, error) {
	dir := filepath.Join(testBaseDir, name)
	// Remove if exists to ensure clean state
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	return dir, nil
}

func TestOSUtils(t *testing.T) {
	testDir, _ := GetTestDir("OSUtils")
	utils := NewOSUtils()

	t.Run("GetFilesError", func(t *testing.T) {
		_, err := utils.GetFiles(filepath.Join(testDir, "non_existent"))
		if err == nil {
			t.Error("expected error for non-existent directory, got nil")
		}
	})

	t.Run("HasSubfoldersError", func(t *testing.T) {
		_, err := utils.HasSubfolders(filepath.Join(testDir, "non_existent"))
		if err == nil {
			t.Error("expected error for non-existent directory, got nil")
		}
	})

	t.Run("Stat", func(t *testing.T) {
		_, err := utils.Stat(testDir)
		if err != nil {
			t.Errorf("unexpected error for Stat: %v", err)
		}
	})

	t.Run("IsLockedSimulation", func(t *testing.T) {
		file := filepath.Join(testDir, "locked_sim.txt")
		_ = os.WriteFile(file, []byte("data"), 0644)

		// Open with FILE_SHARE_NONE to simulate a lock
		pathPtr, _ := windows.UTF16PtrFromString(file)
		h, err := windows.CreateFile(
			pathPtr,
			windows.GENERIC_READ,
			0, // FILE_SHARE_NONE
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err != nil {
			t.Fatalf("failed to lock file: %v", err)
		}
		defer func() { _ = windows.CloseHandle(h) }()

		locked, err := utils.IsLocked(file)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !locked {
			t.Error("expected file to be reported as locked")
		}
	})
}
