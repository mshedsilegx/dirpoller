// Package service provides the core engine and Windows Service lifecycle management.
package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"criticalsys.net/dirpoller/internal/action"
	"criticalsys.net/dirpoller/internal/archive"
	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/integrity"
	"criticalsys.net/dirpoller/internal/poller"
)

// EngineLogger defines the interface for engine-level event logging.
type EngineLogger interface {
	Error(id uint32, msg string) error
	Info(id uint32, msg string) error
	Warn(msg string)
	Close() error
}

// Engine orchestrates the polling, verification, and action execution lifecycle.
// It acts as the central controller, routing events between the poller, verifier, action handler, and loggers.
type Engine struct {
	cfg       *config.Config       // Application configuration
	poller    poller.Poller        // The selected polling algorithm (Interval, Batch, Event, or Trigger)
	verifier  *integrity.Verifier  // Logic for ensuring files are fully committed
	handler   action.ActionHandler // The primary action (SFTP or local Script)
	archiver  *archive.Archiver    // Post-processing logic (Delete, Move, or Compress)
	isService bool                 // Indicates if running as a native Windows Service
	logger    EngineLogger         // System event logger (Windows Event Log or Console)
	customLog *CustomLogger        // File-based dual-track logger (if enabled)
}

// NewEngine initializes the core processing engine with configured components.
// It maps configuration directives to specific interface implementations.
func NewEngine(cfg *config.Config, isService bool) (*Engine, error) {
	// 1. Initialize Polling Algorithm
	var p poller.Poller
	switch cfg.Poll.Algorithm {
	case config.PollInterval:
		p = poller.NewIntervalPoller(cfg)
	case config.PollBatch:
		p = poller.NewBatchPoller(cfg)
	case config.PollEvent:
		p = poller.NewEventPoller(cfg)
	case config.PollTrigger:
		p = poller.NewTriggerPoller(cfg)
	default:
		return nil, fmt.Errorf("unsupported poller algorithm: %s", cfg.Poll.Algorithm)
	}

	// 2. Initialize Primary Action Handler
	var handler action.ActionHandler
	switch cfg.Action.Type {
	case config.ActionSFTP:
		handler = action.NewSFTPHandler(cfg)
	case config.ActionScript:
		handler = action.NewScriptHandler(cfg)
	default:
		return nil, fmt.Errorf("unsupported action type: %s", cfg.Action.Type)
	}

	// 3. Initialize System Logger (EventLog for Services, CLI for manual runs)
	var logger EngineLogger
	if isService {
		var err error
		// "DirPoller" is the registered event source name
		platLogger, err := NewPlatformLogger("DirPoller", true)
		if err != nil {
			return nil, fmt.Errorf("failed to open platform logger: %w", err)
		}
		logger = &engineLoggerWrapper{Logger: platLogger}
	} else {
		logger = &cliLogger{}
	}

	// 4. Initialize Optional File-based Logger
	var customLog *CustomLogger
	if len(cfg.Logging) > 0 {
		customLog = NewCustomLogger(cfg.Logging[0].LogName, cfg.Logging[0].LogRetention)
	}

	return &Engine{
		cfg:       cfg,
		poller:    p,
		verifier:  integrity.NewVerifier(cfg),
		handler:   handler,
		archiver:  archive.NewArchiver(cfg),
		isService: isService,
		logger:    logger,
		customLog: customLog,
	}, nil
}

// Run starts the infinite polling loop. It manages the lifecycle of the poller
// and coordinates the transition of file batches to the processing pipeline.
func (e *Engine) Run(ctx context.Context) error {
	e.logProcess("Engine starting...")
	results := make(chan []string)
	errChan := make(chan error, 1)

	// Start the poller in its own goroutine
	go func() {
		if err := e.poller.Start(ctx, results); err != nil {
			errChan <- err
		}
	}()

	for {
		select {
		case <-ctx.Done():
			e.logProcess("Engine stopping (context canceled)...")
			return ctx.Err()
		case err := <-errChan:
			e.logError(fmt.Sprintf("Poller error: %v", err))
			if e.isService {
				// SECTION: Service Resilience
				// As per specs.txt, the service should log errors and continue.
				// We restart the poller to attempt recovery in the next cycle.
				e.logProcess("Restarting poller after error...")
				go func() {
					if err := e.poller.Start(ctx, results); err != nil {
						errChan <- err
					}
				}()
				continue
			}
			return err
		case files := <-results:
			// Pipeline step: Hand off discovered files for verification and execution
			e.processFiles(ctx, files)
		}
	}
}

// processFiles coordinates the concurrent verification and sequential execution of a file batch.
func (e *Engine) processFiles(ctx context.Context, files []string) {
	summary := ExecutionSummary{
		StartTime: time.Now(),
	}
	var verifiedFiles []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	// STEP 1: CONCURRENT INTEGRITY VERIFICATION
	// Each file is checked across N attempts to ensure it's not being modified.
	for _, f := range files {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			ok, err := e.verifier.Verify(ctx, path)
			if err != nil {
				// Individual file errors go to the Activity Log, not the System Event Log
				mu.Lock()
				summary.Errors = append(summary.Errors, FileProcessInfo{
					Path:  path,
					Error: err.Error(),
				})
				mu.Unlock()
				return
			}
			if ok {
				mu.Lock()
				verifiedFiles = append(verifiedFiles, path)
				mu.Unlock()
			}
		}(f)
	}

	wg.Wait()

	// Exit early if nothing to do
	if len(verifiedFiles) == 0 && len(summary.Errors) == 0 {
		return
	}

	// STEP 2: ACTION EXECUTION (SFTP OR SCRIPT)
	if len(verifiedFiles) > 0 {
		processedFiles, err := e.handler.Execute(ctx, verifiedFiles)
		if err != nil {
			// System-level action failures (e.g., SFTP down) are logged to the System Event Log
			e.logError(fmt.Sprintf("Action execution failed: %v", err))
		}

		// Update activity summary with results from the handler
		successMap := make(map[string]bool)
		for _, f := range processedFiles {
			successMap[f] = true
			info := e.getFileInfo(f, "")
			summary.Processed = append(summary.Processed, info)
		}

		for _, f := range verifiedFiles {
			if !successMap[f] {
				info := e.getFileInfo(f, "action execution failed")
				summary.Errors = append(summary.Errors, info)
			}
		}

		// STEP 3: POST-PROCESSING (ARCHIVE/DELETE)
		// Only performed on files successfully handled in Step 2.
		if len(processedFiles) > 0 {
			if err := e.archiver.Process(ctx, processedFiles); err != nil {
				e.logError(fmt.Sprintf("Post-processing failed: %v", err))
			}
		}
	}

	// STEP 4: ACTIVITY LOGGING
	// Writes the per-execution report to the file system (if configured).
	if e.customLog != nil {
		if err := e.customLog.LogExecution(summary); err != nil {
			e.logError(fmt.Sprintf("Failed to write activity log: %v", err))
		}
	}
}

func (e *Engine) getFileInfo(path string, errMsg string) FileProcessInfo {
	info := FileProcessInfo{
		Path:  path,
		Error: errMsg,
	}
	stat, err := os.Stat(path)
	if err == nil {
		info.Size = stat.Size()
	}

	// Calculate hash for logging
	hash, err := e.verifier.CalculateHash(path)
	if err == nil {
		info.Hash = hash
	}

	return info
}

func (e *Engine) logError(msg string) {
	e.logProcess("ERROR: " + msg)
	if e.logger != nil {
		if err := e.logger.Error(1, msg); err != nil {
			log.Printf("Failed to log error: %v (message: %s)", err, msg)
		}
	} else {
		log.Println(msg)
	}
}

func (e *Engine) logProcess(msg string) {
	if e.customLog != nil {
		if err := e.customLog.LogProcess(msg); err != nil {
			log.Printf("Failed to write process log: %v (message: %s)", err, msg)
		}
	}
}

type engineLoggerWrapper struct {
	Logger
}

func (w *engineLoggerWrapper) Warn(msg string) {
	_ = w.Info(2, "Warning: "+msg)
}

type cliLogger struct{}

func (l *cliLogger) Error(id uint32, msg string) error {
	log.Println("ERROR:", msg)
	return nil
}

func (l *cliLogger) Info(id uint32, msg string) error {
	log.Println("INFO:", msg)
	return nil
}

func (l *cliLogger) Warn(msg string) {
	log.Println("WARN:", msg)
}

func (l *cliLogger) Close() error {
	return nil
}

func (e *Engine) Close() {
	if e.logger != nil {
		if err := e.logger.Close(); err != nil {
			log.Printf("Warning: failed to close system logger: %v", err)
		}
	}
	if e.handler != nil {
		if err := e.handler.Close(); err != nil {
			e.logError(fmt.Sprintf("Warning: failed to close action handler: %v", err))
		}
	}
}
