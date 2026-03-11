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
type Engine struct {
	cfg       *config.Config
	poller    poller.Poller
	verifier  *integrity.Verifier
	handler   action.ActionHandler
	archiver  *archive.Archiver
	isService bool
	logger    EngineLogger
	customLog *CustomLogger
}

// NewEngine initializes the core processing engine with configured components.
func NewEngine(cfg *config.Config, isService bool) (*Engine, error) {
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

	var handler action.ActionHandler
	switch cfg.Action.Type {
	case config.ActionSFTP:
		handler = action.NewSFTPHandler(cfg)
	case config.ActionScript:
		handler = action.NewScriptHandler(cfg)
	default:
		return nil, fmt.Errorf("unsupported action type: %s", cfg.Action.Type)
	}

	var logger EngineLogger
	if isService {
		var err error
		platLogger, err := NewPlatformLogger("DirPoller", true)
		if err != nil {
			return nil, fmt.Errorf("failed to open platform logger: %w", err)
		}
		logger = &engineLoggerWrapper{Logger: platLogger}
	} else {
		logger = &cliLogger{}
	}

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

func (e *Engine) Run(ctx context.Context) error {
	e.logProcess("Engine starting...")
	results := make(chan []string)
	errChan := make(chan error, 1)

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
				// In service mode, log the error and wait for the next polling cycle
				// by restarting the poller.
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
			e.processFiles(ctx, files)
		}
	}
}

func (e *Engine) processFiles(ctx context.Context, files []string) {
	summary := ExecutionSummary{
		StartTime: time.Now(),
	}
	var verifiedFiles []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Concurrent integrity verification with context awareness
	for _, f := range files {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			ok, err := e.verifier.Verify(ctx, path)
			if err != nil {
				// File-specific error: goes ONLY to activity log (via summary)
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

	if len(verifiedFiles) == 0 && len(summary.Errors) == 0 {
		return
	}

	if len(verifiedFiles) > 0 {
		processedFiles, err := e.handler.Execute(ctx, verifiedFiles)
		if err != nil {
			// System-level error (e.g., SFTP connection failure): goes to Event Log and Process Log
			e.logError(fmt.Sprintf("Action execution failed: %v", err))
		}

		// Track processed and errors from handler
		// Note: handler.Execute returns files that were successfully processed
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

		if len(processedFiles) > 0 {
			if err := e.archiver.Process(ctx, processedFiles); err != nil {
				e.logError(fmt.Sprintf("Post-processing failed: %v", err))
			}
		}
	}

	if e.customLog != nil {
		// LogExecution handles only the activity log file, NO EventLog mirroring
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

func (e *Engine) logWarn(msg string) {
	e.logProcess("WARN: " + msg)
	if e.logger != nil {
		e.logger.Warn(msg)
	} else {
		log.Printf("Warning: %s", msg)
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
