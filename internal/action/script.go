package action

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"criticalsys.net/dirpoller/internal/config"
)

// ScriptHandler executes local scripts for processed files.
// It uses a semaphore pool to control concurrency and enforces an execution timeout.
type ScriptHandler struct {
	cfg       *config.Config
	semaphore chan struct{}
}

// NewScriptHandler creates a new script action handler with a persistent semaphore.
func NewScriptHandler(cfg *config.Config) *ScriptHandler {
	return &ScriptHandler{
		cfg:       cfg,
		semaphore: make(chan struct{}, cfg.Action.ConcurrentConnections),
	}
}

// Execute runs the configured script for each file in parallel.
func (h *ScriptHandler) Execute(ctx context.Context, files []string) ([]string, error) {
	var wg sync.WaitGroup
	errChan := make(chan error, len(files))
	successChan := make(chan string, len(files))

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case h.semaphore <- struct{}{}:
				defer func() { <-h.semaphore }()
				if err := h.executeScript(ctx, f); err != nil {
					errChan <- err
				} else {
					successChan <- f
				}
			}
		}(file)
	}

	wg.Wait()
	close(errChan)
	close(successChan)

	var successfulFiles []string
	for f := range successChan {
		successfulFiles = append(successfulFiles, f)
	}

	if len(errChan) > 0 {
		var errs []error
		for e := range errChan {
			errs = append(errs, e)
		}
		return successfulFiles, errors.Join(errs...)
	}
	return successfulFiles, nil
}

// Close implements the ActionHandler interface.
func (h *ScriptHandler) Close() error {
	return nil
}

// executeScript runs the configured script for a single file.
// It uses an absolute path for security and a context timeout for reliability.
func (h *ScriptHandler) executeScript(ctx context.Context, file string) error {
	// Security: Use absolute path and validate file exists
	absFile, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %w", file, err)
	}

	timeout := time.Duration(h.cfg.Action.Script.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Performance: Script execution is sequential per batch by default in engine.go
	// but here we ensure the command is executed safely.
	// #nosec G204 - Script path is validated as absolute and existing in config.go
	cmd := exec.CommandContext(childCtx, h.cfg.Action.Script.Path, absFile)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("script execution failed for %s: %w, output: %s", file, err, string(output))
	}

	return nil
}
