package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCustomLogger_LogExecution(t *testing.T) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testDir := filepath.Join(tempDir, "dirpoller_UTESTS", "logger_exec")
	_ = os.MkdirAll(testDir, 0750)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	logName := filepath.Join(testDir, "test.log")
	logger := NewCustomLogger(logName, 0)

	summary := ExecutionSummary{
		StartTime: time.Now(),
		Processed: []FileProcessInfo{
			{Path: "file1.txt", Size: 100, Hash: "hash1"},
		},
		Errors: []FileProcessInfo{
			{Path: "file2.txt", Size: 200, Hash: "hash2", Error: "some error"},
		},
	}

	if err := logger.LogExecution(summary); err != nil {
		t.Fatalf("LogExecution failed: %v", err)
	}

	// Check if activity log was created
	files, _ := os.ReadDir(testDir)
	found := false
	for _, f := range files {
		if strings.Contains(f.Name(), "test_activity_") {
			found = true
			content, _ := os.ReadFile(filepath.Join(testDir, f.Name()))
			sContent := string(content)
			if !strings.Contains(sContent, "# Status") {
				t.Errorf("log missing # Status section")
			}
			if !strings.Contains(sContent, "file1.txt|100|hash1") {
				t.Errorf("log missing processed file info")
			}
			if !strings.Contains(sContent, "file2.txt in error|200|hash2|some error") {
				t.Errorf("log missing error file info or incorrect format")
			}
		}
	}
	if !found {
		t.Error("activity log file not found")
	}
}

func TestCustomLogger_LogProcess(t *testing.T) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testDir := filepath.Join(tempDir, "dirpoller_UTESTS", "logger_process")
	_ = os.MkdirAll(testDir, 0750)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	logName := filepath.Join(testDir, "test.log")
	logger := NewCustomLogger(logName, 0)

	if err := logger.LogProcess("System started"); err != nil {
		t.Fatalf("LogProcess failed: %v", err)
	}

	// Check if process log was created
	files, _ := os.ReadDir(testDir)
	found := false
	for _, f := range files {
		if strings.Contains(f.Name(), "test_process_") {
			found = true
			content, _ := os.ReadFile(filepath.Join(testDir, f.Name()))
			if !strings.Contains(string(content), "System started") {
				t.Errorf("log missing process message")
			}
		}
	}
	if !found {
		t.Error("process log file not found")
		t.Errorf("log file was not created")
	}
}

func TestCustomLogger_PurgeOldLogs(t *testing.T) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testDir := filepath.Join(tempDir, "dirpoller_UTESTS", "logger_purge")
	_ = os.MkdirAll(testDir, 0750)
	defer os.RemoveAll(testDir)

	logName := filepath.Join(testDir, "test.log")
	logger := NewCustomLogger(logName, 2) // 2 days retention

	// Create an old process log file
	oldProcessLog := filepath.Join(testDir, "test_process_20200101.log")
	if err := os.WriteFile(oldProcessLog, []byte("old process"), 0644); err != nil {
		t.Fatalf("failed to create old process log: %v", err)
	}

	// Create an old activity log file
	oldActivityLog := filepath.Join(testDir, "test_activity_20200101-120000.log")
	if err := os.WriteFile(oldActivityLog, []byte("old activity"), 0644); err != nil {
		t.Fatalf("failed to create old activity log: %v", err)
	}

	// Set their mod times to 10 days ago
	oldTime := time.Now().AddDate(0, 0, -10)
	_ = os.Chtimes(oldProcessLog, oldTime, oldTime)
	_ = os.Chtimes(oldActivityLog, oldTime, oldTime)

	// Create a recent process log
	recentLog := filepath.Join(testDir, "test_process_"+time.Now().Format("20060102")+".log")
	if err := os.WriteFile(recentLog, []byte("recent"), 0644); err != nil {
		t.Fatalf("failed to create recent log: %v", err)
	}

	// Trigger purge via LogProcess
	if err := logger.LogProcess("Triggering purge"); err != nil {
		t.Fatalf("LogProcess failed: %v", err)
	}

	// Verify old logs are gone, recent one stays
	if _, err := os.Stat(oldProcessLog); !os.IsNotExist(err) {
		t.Errorf("old process log was not purged")
	}
	if _, err := os.Stat(oldActivityLog); !os.IsNotExist(err) {
		t.Errorf("old activity log was not purged")
	}
	if _, err := os.Stat(recentLog); err != nil {
		t.Errorf("recent log was purged or missing: %v", err)
	}
}
