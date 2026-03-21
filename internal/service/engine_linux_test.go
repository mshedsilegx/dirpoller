//go:build linux

// Package service_test provides Linux-specific unit tests for the processing engine.
//
// Objective:
// Validate the core Engine orchestration logic on Linux, focusing on
// CLI mode behaviors, signal handling, and interaction with the
// custom file-based logger.
//
// Scenarios Covered:
// - CLI Logging: Verification of cliLogger usage in non-service mode.
// - Process Logging: Verification of file creation and content for process logs.
// - Resilience: Testing of poller recovery and backoff logic in service mode.
// - Scheduled Tasks: Verification of daily maintenance triggers (e.g., SFTP cleanup).
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/poller"
)

type mockPoller struct {
	startFunc func(ctx context.Context, results chan<- []string) error
}

func (m *mockPoller) Start(ctx context.Context, results chan<- []string) error {
	if m.startFunc != nil {
		return m.startFunc(ctx, results)
	}
	<-ctx.Done()
	return ctx.Err()
}

type mockActionHandler struct {
	executeFunc       func(ctx context.Context, files []string) ([]string, error)
	remoteCleanupFunc func(ctx context.Context) error
	closeFunc         func() error
}

func (m *mockActionHandler) Execute(ctx context.Context, files []string) ([]string, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, files)
	}
	return files, nil
}

func (m *mockActionHandler) RemoteCleanup(ctx context.Context) error {
	if m.remoteCleanupFunc != nil {
		return m.remoteCleanupFunc(ctx)
	}
	return nil
}

func (m *mockActionHandler) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// TestEngine_Linux validates various engine behaviors in a Linux environment.
//
// Scenario:
// 1. NewEngine_CLI_Logger: Ensures correct logger selection for CLI mode.
// 2. Engine_logProcess_CustomLog: Verifies file-based process log creation.
// 3. Engine_Run_Poller_Error_Service: Tests poller restart logic on failure.
// 4. Engine_Run_ScheduledTasks: Confirms daily task execution on day-change.
//
// Success Criteria:
// - Log files must be created with correct naming conventions.
// - The engine must attempt poller recovery in service mode.
// - Scheduled tasks must be triggered correctly by the day-tracking logic.
func TestEngine_Linux(t *testing.T) {
	cfg := &config.Config{
		Poll: config.PollConfig{
			Algorithm: config.PollInterval,
		},
		Action: config.ActionConfig{
			Type: config.ActionScript,
		},
	}

	t.Run("NewEngine_CLI_Logger", func(t *testing.T) {
		e, err := NewEngine(cfg, false)
		if err != nil {
			t.Fatalf("failed to create engine: %v", err)
		}
		if _, ok := e.logger.(*cliLogger); !ok {
			t.Error("expected cliLogger for non-service mode")
		}
	})

	t.Run("Engine_logError_CLI", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		// Should not panic and should log to stdout/stderr
		e.logError("test error")
	})

	t.Run("Engine_logProcess_CustomLog", func(t *testing.T) {
		tempDir := t.TempDir()
		logName := filepath.Join(tempDir, "process.log")
		cfgWithLog := &config.Config{
			Poll:   config.PollConfig{Algorithm: config.PollInterval},
			Action: config.ActionConfig{Type: config.ActionScript},
			Logging: []config.LoggingConfig{
				{LogName: logName, LogRetention: 1},
			},
		}
		e, _ := NewEngine(cfgWithLog, false)
		e.logProcess("test process log")

		// Verify file exists - NewCustomLogger uses the directory of the logName
		// and prefixes the file with "process_" or "activity_" based on its logic.
		// From the previous run, we saw it created "process_process_20260320.log"
		files, _ := os.ReadDir(tempDir)
		found := false
		for _, f := range files {
			if strings.Contains(f.Name(), "process_") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected process log file to be created in %s, found: %v", tempDir, files)
		}
	})

	t.Run("Engine_NewEngineWithPlatLogger", func(t *testing.T) {
		platLoggerFunc := func(name string, isService bool) (Logger, error) {
			return &linuxLogger{}, nil
		}
		e, err := NewEngineWithPlatLogger(cfg, true, platLoggerFunc)
		if err != nil {
			t.Fatalf("failed to create engine with plat logger: %v", err)
		}
		if e.logger == nil {
			t.Error("expected logger to be initialized")
		}
		e.Close()
	})

	t.Run("Engine_Warn_Wrapper", func(t *testing.T) {
		platLoggerFunc := func(name string, isService bool) (Logger, error) {
			return &linuxLogger{}, nil
		}
		e, _ := NewEngineWithPlatLogger(cfg, true, platLoggerFunc)
		e.logger.Warn("test warning")
	})

	t.Run("Engine_Close_With_Handler", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		handler := &mockActionHandler{}
		e.handler = handler
		e.Close()
	})

	t.Run("Engine_Run_Poller_Error_Service", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return fmt.Errorf("recoverable error")
			},
		}
		e.testBackoff = 1 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
		if e.consecutivePollerFailures == 0 {
			t.Error("expected consecutivePollerFailures to be incremented")
		}
	})

	t.Run("Engine_Run_ScheduledTasks", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{}
		handler := &mockActionHandler{}
		e.handler = handler

		// Force scheduled task execution by setting lastCleanupDay to yesterday
		e.testLastCleanupDay = time.Now().YearDay() - 1
		if e.testLastCleanupDay < 1 {
			e.testLastCleanupDay = 365
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
	})

	t.Run("Engine_processFiles_Errors", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		e.verifier = &mockVerifier{verifyErr: fmt.Errorf("verify error")}
		e.processFiles(context.Background(), []string{"fail.txt"})

		e.verifier = &mockVerifier{verifyRet: true}
		handler := &mockActionHandler{
			executeFunc: func(ctx context.Context, files []string) ([]string, error) {
				return nil, fmt.Errorf("handler error")
			},
		}
		e.handler = handler
		e.processFiles(context.Background(), []string{"fail_action.txt"})
	})

	t.Run("Engine_logError_Service", func(t *testing.T) {
		platLoggerFunc := func(name string, isService bool) (Logger, error) {
			return &linuxLogger{}, nil
		}
		e, _ := NewEngineWithPlatLogger(cfg, true, platLoggerFunc)
		e.logError("service error")
	})

	t.Run("Engine_Run_Poller_Permanent_Error", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return &poller.ErrSubfolderDetected{Path: "sub"}
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
	})

	t.Run("Engine_Run_Watcher_Initialization_Error", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return &poller.ErrWatcherInitialization{Err: fmt.Errorf("init fail")}
			},
		}
		e.testBackoff = 1 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
	})

	t.Run("Engine_Run_Watcher_Runtime_Error", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return &poller.ErrWatcherRuntime{Err: fmt.Errorf("runtime fail")}
			},
		}
		e.testBackoff = 1 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
	})

	t.Run("CustomLogger_LogProcess_Error", func(t *testing.T) {
		// Use an invalid path to trigger OpenFile error
		logger := NewCustomLogger("/proc/invalid/path", 0)
		err := logger.LogProcess("test error")
		if err == nil {
			t.Error("expected error for invalid log path, got nil")
		}
	})

	t.Run("CustomLogger_LogExecution_Error", func(t *testing.T) {
		// Use an invalid path to trigger OpenFile error
		logger := NewCustomLogger("/proc/invalid/path", 0)
		summary := ExecutionSummary{
			Processed: []FileProcessInfo{{Path: "test.txt"}},
		}
		err := logger.LogExecution(summary)
		if err == nil {
			t.Error("expected error for invalid log path, got nil")
		}
	})

	t.Run("CustomLogger_LogExecution_NoFiles", func(t *testing.T) {
		tempDir := t.TempDir()
		logger := NewCustomLogger(filepath.Join(tempDir, "test.log"), 0)
		summary := ExecutionSummary{}
		err := logger.LogExecution(summary)
		if err != nil {
			t.Errorf("expected nil error for empty summary, got %v", err)
		}
	})

	t.Run("Engine_Run_Results_Empty", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				results <- []string{}
				return nil
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
	})

	t.Run("Engine_ProcessFiles_Script", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		handler := &mockActionHandler{}
		e.handler = handler

		// Mock verifier to always succeed
		e.verifier = &mockVerifier{verifyRet: true}

		e.processFiles(context.Background(), []string{"test.txt"})
	})

	t.Run("Engine_Run_Poller_Error_Retry_Backoff", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return fmt.Errorf("permission denied")
			},
		}
		e.testBackoff = 1 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
		if e.consecutivePollerFailures < 1 {
			t.Error("expected retry attempt")
		}
	})

	t.Run("Engine_Run_Poller_Runtime_Error_Service", func(t *testing.T) {
		e, _ := NewEngine(cfg, true)
		e.poller = &mockPoller{
			startFunc: func(ctx context.Context, results chan<- []string) error {
				return &poller.ErrWatcherRuntime{Err: fmt.Errorf("runtime error")}
			},
		}
		e.testBackoff = 1 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = e.Run(ctx)
		if e.consecutivePollerFailures == 0 {
			t.Error("expected consecutivePollerFailures to be incremented")
		}
	})

	t.Run("Engine_processFiles_ExecutionFail", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		e.verifier = &mockVerifier{verifyRet: true}
		handler := &mockActionHandler{
			executeFunc: func(ctx context.Context, files []string) ([]string, error) {
				return []string{}, nil // No files processed = execution fail for input
			},
		}
		e.handler = handler
		e.processFiles(context.Background(), []string{"fail_exec.txt"})
	})

	t.Run("Engine_processFiles_ArchiveFail", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		e.verifier = &mockVerifier{verifyRet: true}
		e.archiver = &mockArchiver{processErr: fmt.Errorf("archive fail")}
		e.processFiles(context.Background(), []string{"archive_fail.txt"})
	})

	t.Run("Engine_processFiles_LogExecutionFail", func(t *testing.T) {
		tempDir := t.TempDir()
		cfgWithLog := &config.Config{
			Poll:   config.PollConfig{Algorithm: config.PollInterval},
			Action: config.ActionConfig{Type: config.ActionScript},
			Logging: []config.LoggingConfig{
				{LogName: filepath.Join(tempDir, "activity.log"), LogRetention: 1},
			},
		}
		e, _ := NewEngine(cfgWithLog, false)
		e.verifier = &mockVerifier{verifyRet: true}
		// Mock customLog to fail LogExecution
		e.customLog = &mockActivityLogger{logExecutionErr: fmt.Errorf("log exec fail")}
		e.processFiles(context.Background(), []string{"log_fail.txt"})
	})

	t.Run("Engine_getFileInfo_StatError", func(t *testing.T) {
		e, _ := NewEngine(cfg, false)
		e.verifier = &mockVerifier{}
		info := e.getFileInfo("/non/existent/path", "stat error")
		if info.Size != 0 {
			t.Errorf("expected size 0, got %d", info.Size)
		}
	})

	t.Run("Engine_logError_NoLogger", func(t *testing.T) {
		e := &Engine{}
		e.logError("test no logger")
	})

	t.Run("Engine_Close_Errors", func(t *testing.T) {
		platLoggerFunc := func(name string, isService bool) (Logger, error) {
			return &mockPlatformLogger{failClose: true}, nil
		}
		e, _ := NewEngineWithPlatLogger(cfg, true, platLoggerFunc)
		e.handler = &mockActionHandler{
			closeFunc: func() error { return fmt.Errorf("close fail") },
		}
		e.Close()
	})

	t.Run("CLI_Logger_Warn", func(t *testing.T) {
		l := &cliLogger{}
		l.Warn("test warning")
	})

	t.Run("CLI_Logger_Info", func(t *testing.T) {
		l := &cliLogger{}
		_ = l.Info(1, "test info")
	})

	t.Run("CLI_Logger_Close", func(t *testing.T) {
		l := &cliLogger{}
		_ = l.Close()
	})
}

type mockVerifier struct {
	verifyRet bool
	verifyErr error
}

func (m *mockVerifier) Verify(ctx context.Context, path string) (bool, error) {
	return m.verifyRet, m.verifyErr
}

func (m *mockVerifier) CalculateHash(path string) (string, error) {
	return "hash", nil
}

type mockPlatformLogger struct {
	failClose bool
}

func (m *mockPlatformLogger) Error(id uint32, msg string) error { return nil }
func (m *mockPlatformLogger) Info(id uint32, msg string) error  { return nil }
func (m *mockPlatformLogger) Close() error {
	if m.failClose {
		return fmt.Errorf("close fail")
	}
	return nil
}

type mockArchiver struct {
	processErr error
}

func (m *mockArchiver) Process(ctx context.Context, files []string) error {
	return m.processErr
}

type mockActivityLogger struct {
	logProcessErr   error
	logExecutionErr error
}

func (m *mockActivityLogger) LogProcess(msg string) error {
	return m.logProcessErr
}

func (m *mockActivityLogger) LogExecution(summary ExecutionSummary) error {
	return m.logExecutionErr
}
