package poller

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntervalPoller(t *testing.T) {
	testDir, err := GetTestDir("IntervalPoller")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory: testDir,
			Value:     1, // 1 second interval
		},
	}

	p := NewIntervalPoller(cfg)
	results := make(chan []string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Create a test file
	testFile := filepath.Join(testDir, "test1.txt")
	_ = os.WriteFile(testFile, []byte("hello"), 0644)

	go func() {
		if err := p.Start(ctx, results); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			t.Errorf("Poller failed: %v", err)
		}
	}()

	select {
	case files := <-results:
		if len(files) != 1 || files[0] != testFile {
			t.Errorf("expected %s, got %v", testFile, files)
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for files")
	}
}

func TestBatchPoller(t *testing.T) {
	testDir, err := GetTestDir("BatchPoller")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory:           testDir,
			Value:               2, // Batch size 2
			BatchTimeoutSeconds: 2,
		},
	}

	p := NewBatchPoller(cfg)
	results := make(chan []string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Test threshold trigger
	file1 := filepath.Join(testDir, "batch1.txt")
	file2 := filepath.Join(testDir, "batch2.txt")
	_ = os.WriteFile(file1, []byte("1"), 0644)
	_ = os.WriteFile(file2, []byte("2"), 0644)

	go func() {
		if err := p.Start(ctx, results); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			t.Errorf("Poller failed: %v", err)
		}
	}()

	select {
	case files := <-results:
		if len(files) < 2 {
			t.Errorf("expected at least 2 files, got %d", len(files))
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for batch threshold")
	}

	// 2. Test timeout trigger
	file3 := filepath.Join(testDir, "batch3.txt")
	_ = os.WriteFile(file3, []byte("3"), 0644)

	select {
	case files := <-results:
		found := false
		for _, f := range files {
			if f == file3 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find %s in timeout results", file3)
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for batch timeout trigger")
	}
}

func TestBatchPollerTimeout(t *testing.T) {
	testDir, _ := GetTestDir("BatchPollerTimeout")
	testFile := filepath.Join(testDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("data"), 0644)

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory:           testDir,
			Algorithm:           config.PollBatch,
			Value:               10, // High threshold
			BatchTimeoutSeconds: 1,  // Short timeout
		},
	}

	p := NewBatchPoller(cfg)
	results := make(chan []string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		_ = p.Start(ctx, results)
	}()

	select {
	case files := <-results:
		if len(files) != 1 || files[0] != testFile {
			t.Errorf("expected 1 file in result due to timeout, got %v", files)
		}
	case <-ctx.Done():
		t.Error("timeout waiting for batch poller result")
	}
}

func TestEventPoller(t *testing.T) {
	testDir, err := GetTestDir("EventPoller")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory: testDir,
		},
	}

	p := NewEventPoller(cfg)
	results := make(chan []string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		if err := p.Start(ctx, results); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			t.Errorf("Poller failed: %v", err)
		}
	}()

	// Wait for watcher to start
	time.Sleep(500 * time.Millisecond)

	// Create a file
	testFile := filepath.Join(testDir, "event1.txt")
	_ = os.WriteFile(testFile, []byte("event"), 0644)

	select {
	case files := <-results:
		if len(files) == 0 || files[0] != testFile {
			t.Errorf("expected %s, got %v", testFile, files)
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for event")
	}
}

func TestEventPollerDynamicSubfolder(t *testing.T) {
	testDir, err := GetTestDir("EventPollerDynamic")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory: testDir,
		},
	}

	p := NewEventPoller(cfg)
	results := make(chan []string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- p.Start(ctx, results)
	}()

	// Wait for watcher to start
	time.Sleep(500 * time.Millisecond)

	// Create a subfolder
	subDir := filepath.Join(testDir, "dynamic_sub")
	if err := os.Mkdir(subDir, 0750); err != nil {
		t.Fatalf("failed to create subfolder: %v", err)
	}

	select {
	case err := <-errChan:
		if err == nil || err.Error() == "" {
			t.Error("expected error for dynamic subfolder detection, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for dynamic subfolder error")
	}
}

func TestPollerSubfolderDetection(t *testing.T) {
	testDir, err := GetTestDir("SubfolderDetection")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// Create subfolder
	subDir := filepath.Join(testDir, "sub")
	if err := os.Mkdir(subDir, 0750); err != nil {
		t.Fatalf("failed to create subfolder: %v", err)
	}

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory: testDir,
			Value:     1,
		},
	}

	// Test IntervalPoller
	p := NewIntervalPoller(cfg)
	if err := p.poll(make(chan []string, 1)); err == nil {
		t.Error("expected error for subfolder in IntervalPoller, got nil")
	}

	// Test EventPoller initial check
	ep := NewEventPoller(cfg)
	if err := ep.Start(context.Background(), make(chan []string, 1)); err == nil {
		t.Error("expected error for subfolder in EventPoller Start, got nil")
	}
}
