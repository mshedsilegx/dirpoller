package service

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

var testBaseDir string

func TestMain(m *testing.M) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS", "service")
	_ = os.MkdirAll(testBaseDir, 0750)

	code := m.Run()
	os.Exit(code)
}

func getTestDir(name string) string {
	dir := filepath.Join(testBaseDir, name)
	_ = os.RemoveAll(dir)
	_ = os.MkdirAll(dir, 0750)
	return dir
}

// TestEngineLifecycle simulates the engine start/stop/pause/continue.
func TestEngineLifecycle(t *testing.T) {
	testDir := getTestDir("EngineLifecycle")
	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory: testDir,
			Algorithm: config.PollInterval,
			Value:     1,
		},
		Integrity: config.IntegrityConfig{
			VerificationAttempts: 1,
			VerificationInterval: 1,
			Algorithm:            config.IntegritySize,
		},
		Action: config.ActionConfig{
			Type:                  config.ActionSFTP,
			ConcurrentConnections: 1,
			PostProcess: config.PostProcessConfig{
				Action: config.PostActionDelete,
			},
			SFTP: config.SFTPConfig{
				Host:     "127.0.0.1",
				Username: "test",
				Password: "pass",
			},
		},
	}

	engine, err := NewEngine(cfg, false)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errChan := make(chan error, 1)

	go func() {
		errChan <- engine.Run(ctx)
	}()

	// Wait for engine to start
	time.Sleep(500 * time.Millisecond)

	// Create a test file
	testFile := filepath.Join(testDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("test data"), 0644)

	// Wait for processing
	time.Sleep(2 * time.Second)

	// Stop engine
	cancel()

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Errorf("engine stopped with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for engine to stop")
	}
}

// TestWindowsServiceExecute simulates the Execute loop of the Windows service.
func TestWindowsServiceExecute(t *testing.T) {
	testDir := getTestDir("ServiceExecute")
	cfgPath := filepath.Join(testDir, "config.json")
	// Simplified config for testing
	_ = os.WriteFile(cfgPath, []byte(`{
		"poll": { "directory": "`+filepath.ToSlash(testDir)+`", "algorithm": "interval", "value": 1 },
		"integrity": { "attempts": 1, "interval": 1, "algorithm": "size" },
		"action": { "type": "sftp", "connections": 1, "post_process": { "action": "delete" }, "sftp": { "host": "127.0.0.1", "username": "test", "password": "pass" } }
	}`), 0644)

	ws := &WindowsService{cfgPath: cfgPath}

	r := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 10)

	go func() {
		// Start service
		ws.Execute(nil, r, changes)
	}()

	// Verify StartPending
	status := <-changes
	if status.State != svc.StartPending {
		t.Errorf("expected StartPending, got %v", status.State)
	}

	// Verify Running
	status = <-changes
	if status.State != svc.Running {
		t.Errorf("expected Running, got %v", status.State)
	}

	// Send Stop request
	r <- svc.ChangeRequest{Cmd: svc.Stop}

	// Verify StopPending
	status = <-changes
	if status.State != svc.StopPending {
		t.Errorf("expected StopPending, got %v", status.State)
	}
}

func TestNewEngineErrors(t *testing.T) {
	t.Run("UnsupportedPoller", func(t *testing.T) {
		cfg := &config.Config{
			Poll: config.PollConfig{Algorithm: "invalid"},
		}
		_, err := NewEngine(cfg, false)
		if err == nil {
			t.Error("expected error for unsupported poller, got nil")
		}
	})

	t.Run("UnsupportedAction", func(t *testing.T) {
		cfg := &config.Config{
			Poll:   config.PollConfig{Algorithm: config.PollInterval},
			Action: config.ActionConfig{Type: "invalid"},
		}
		_, err := NewEngine(cfg, false)
		if err == nil {
			t.Error("expected error for unsupported action, got nil")
		}
	})
}

func TestWindowsServiceControlRequests(t *testing.T) {
	testDir := getTestDir("ServiceControls")
	cfgPath := filepath.Join(testDir, "config.json")
	_ = os.WriteFile(cfgPath, []byte(`{
		"poll": { "directory": "`+filepath.ToSlash(testDir)+`", "algorithm": "interval", "value": 10 },
		"integrity": { "attempts": 1, "interval": 1, "algorithm": "size" },
		"action": { "type": "sftp", "connections": 1, "post_process": { "action": "delete" }, "sftp": { "host": "localhost", "username": "u", "password": "p" } }
	}`), 0644)

	ws := &WindowsService{cfgPath: cfgPath}
	r := make(chan svc.ChangeRequest)
	changes := make(chan svc.Status, 10)

	go ws.Execute(nil, r, changes)

	<-changes // StartPending
	<-changes // Running

	// Test Interrogate
	r <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: svc.Status{State: svc.Running}}
	status := <-changes
	if status.State != svc.Running {
		t.Errorf("expected Running after Interrogate (1), got %v", status.State)
	}
	status = <-changes // The second status from Interrogate
	if status.State != svc.Running {
		t.Errorf("expected Running after Interrogate (2), got %v", status.State)
	}

	// Test Pause
	r <- svc.ChangeRequest{Cmd: svc.Pause}
	status = <-changes
	if status.State != svc.Paused {
		t.Errorf("expected Paused, got %v", status.State)
	}

	// Test Continue
	r <- svc.ChangeRequest{Cmd: svc.Continue}
	status = <-changes
	if status.State != svc.Running {
		t.Errorf("expected Running after Continue, got %v", status.State)
	}

	// Stop
	r <- svc.ChangeRequest{Cmd: svc.Stop}
	<-changes // StopPending
}

func TestEngineLogErrorAndClose(t *testing.T) {
	testDir := getTestDir("EngineLogClose")

	t.Run("LogErrorAndClose", func(t *testing.T) {
		cfg := &config.Config{
			Poll:      config.PollConfig{Directory: testDir, Algorithm: config.PollInterval, Value: 1},
			Integrity: config.IntegrityConfig{Algorithm: config.IntegritySize},
			Action: config.ActionConfig{
				Type: config.ActionSFTP,
				PostProcess: config.PostProcessConfig{
					Action: config.PostActionDelete,
				},
				SFTP: config.SFTPConfig{Host: "h", Username: "u", Password: "p"},
			},
		}
		e, _ := NewEngine(cfg, false)
		e.logError("test error")
		e.Close()
	})
}

type errorPoller struct{}

func (p *errorPoller) Start(ctx context.Context, results chan<- []string) error {
	return fmt.Errorf("forced poller error")
}

func TestEngineRunError(t *testing.T) {
	testDir := getTestDir("EngineRunError")
	cfg := &config.Config{
		Poll:      config.PollConfig{Directory: testDir, Algorithm: config.PollInterval, Value: 1},
		Integrity: config.IntegrityConfig{Algorithm: config.IntegritySize},
		Action: config.ActionConfig{
			Type: config.ActionSFTP,
			PostProcess: config.PostProcessConfig{
				Action: config.PostActionDelete,
			},
			SFTP: config.SFTPConfig{Host: "h", Username: "u", Password: "p"},
		},
	}

	e, _ := NewEngine(cfg, false)
	e.poller = &errorPoller{}

	err := e.Run(context.Background())
	if err == nil || err.Error() != "forced poller error" {
		t.Errorf("expected forced poller error, got %v", err)
	}
}
