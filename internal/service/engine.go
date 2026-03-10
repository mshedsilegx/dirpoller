// Package service provides the core engine and Windows Service lifecycle management.
package service

import (
	"context"
	"fmt"
	"log"
	"sync"

	"criticalsys.net/dirpoller/internal/action"
	"criticalsys.net/dirpoller/internal/archive"
	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/integrity"
	"criticalsys.net/dirpoller/internal/poller"
)

// Engine orchestrates the polling, verification, and action execution lifecycle.
type Engine struct {
	cfg       *config.Config
	poller    poller.Poller
	verifier  *integrity.Verifier
	handler   action.ActionHandler
	archiver  *archive.Archiver
	isService bool
	logger    Logger
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

	var logger Logger
	if isService {
		var err error
		logger, err = NewPlatformLogger("DirPoller", true)
		if err != nil {
			return nil, fmt.Errorf("failed to open platform logger: %w", err)
		}
	}

	return &Engine{
		cfg:       cfg,
		poller:    p,
		verifier:  integrity.NewVerifier(cfg),
		handler:   handler,
		archiver:  archive.NewArchiver(cfg),
		isService: isService,
		logger:    logger,
	}, nil
}

func (e *Engine) Run(ctx context.Context) error {
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
			return ctx.Err()
		case err := <-errChan:
			if e.isService && e.logger != nil {
				if logErr := e.logger.Error(1, fmt.Sprintf("Poller error: %v", err)); logErr != nil {
					log.Printf("Failed to log error to system logger: %v (original error: %v)", logErr, err)
				}
				// In service mode, we might want to restart the poller or wait
				// For now, we return the error to the service manager
				return err
			}
			return err
		case files := <-results:
			e.processFiles(ctx, files)
		}
	}
}

func (e *Engine) processFiles(ctx context.Context, files []string) {
	var verifiedFiles []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	// Concurrent integrity verification with context awareness
	for _, f := range files {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			default:
				ok, err := e.verifier.Verify(ctx, path)
				if err != nil {
					e.logError(fmt.Sprintf("Integrity check failed for %s: %v", path, err))
					return
				}
				if ok {
					mu.Lock()
					verifiedFiles = append(verifiedFiles, path)
					mu.Unlock()
				}
			}
		}(f)
	}

	wg.Wait()

	if len(verifiedFiles) == 0 {
		return
	}

	processedFiles, err := e.handler.Execute(ctx, verifiedFiles)
	if err != nil {
		e.logError(fmt.Sprintf("Action execution failed (some files may have succeeded): %v", err))
	}

	if len(processedFiles) > 0 {
		if err := e.archiver.Process(ctx, processedFiles); err != nil {
			e.logError(fmt.Sprintf("Post-processing failed: %v", err))
		}
	}
}

func (e *Engine) logError(msg string) {
	if e.isService && e.logger != nil {
		if err := e.logger.Error(1, msg); err != nil {
			log.Printf("Failed to log error to system logger: %v (message: %s)", err, msg)
		}
	} else {
		log.Println(msg)
	}
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
