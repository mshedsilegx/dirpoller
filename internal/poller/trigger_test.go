package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"criticalsys.net/dirpoller/internal/config"
)

func TestTriggerPoller(t *testing.T) {
	testDir, err := os.MkdirTemp("", "TriggerPollerTest")
	if err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	cfg := &config.Config{
		Poll: config.PollConfig{
			Directory:           testDir,
			Algorithm:           config.PollTrigger,
			Value:               "trigger.txt",
			BatchTimeoutSeconds: 2,
		},
	}

	p := NewTriggerPoller(cfg)
	results := make(chan []string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Test trigger file
	file1 := filepath.Join(testDir, "data1.txt")
	_ = os.WriteFile(file1, []byte("data"), 0644)

	go func() {
		if err := p.Start(ctx, results); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			t.Errorf("Poller failed: %v", err)
		}
	}()

	// Create trigger file
	time.Sleep(500 * time.Millisecond)
	triggerFile := filepath.Join(testDir, "trigger.txt")
	_ = os.WriteFile(triggerFile, []byte("go"), 0644)

	select {
	case files := <-results:
		found := false
		for _, f := range files {
			if filepath.Base(f) == "data1.txt" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find data1.txt in results, got %v", files)
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for trigger")
	}

	// 2. Test timeout trigger
	file2 := filepath.Join(testDir, "data2.txt")
	_ = os.WriteFile(file2, []byte("more data"), 0644)

	select {
	case files := <-results:
		found := false
		for _, f := range files {
			if filepath.Base(f) == "data2.txt" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find data2.txt in timeout results")
		}
	case <-ctx.Done():
		t.Errorf("timeout waiting for batch timeout")
	}
}
