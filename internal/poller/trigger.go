package poller

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"github.com/fsnotify/fsnotify"
)

type TriggerPoller struct {
	cfg   *config.Config
	utils OSUtils
	mu    sync.Mutex
	files map[string]struct{}
}

func NewTriggerPoller(cfg *config.Config) *TriggerPoller {
	return &TriggerPoller{
		cfg:   cfg,
		utils: NewOSUtils(),
		files: make(map[string]struct{}),
	}
}

func (p *TriggerPoller) Start(ctx context.Context, results chan<- []string) error {
	pattern, ok := p.cfg.Poll.Value.(string)
	if !ok {
		return fmt.Errorf("trigger pattern must be a string")
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	if err := watcher.Add(p.cfg.Poll.Directory); err != nil {
		return fmt.Errorf("failed to add directory to watcher: %w", err)
	}

	// Initial scan
	if _, err := p.utils.HasSubfolders(p.cfg.Poll.Directory); err != nil {
		return err
	}
	initialFiles, err := p.utils.GetFiles(p.cfg.Poll.Directory)
	if err == nil {
		p.mu.Lock()
		for _, f := range initialFiles {
			if p.isTriggerFile(f, pattern) {
				p.flush(results)
				break
			}
			p.files[f] = struct{}{}
		}
		p.mu.Unlock()
	}

	timeoutDuration := time.Duration(p.cfg.Poll.BatchTimeoutSeconds) * time.Second
	timeoutTicker := time.NewTicker(timeoutDuration)
	defer timeoutTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutTicker.C:
			p.mu.Lock()
			if len(p.files) > 0 {
				p.flush(results)
			}
			p.mu.Unlock()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				p.mu.Lock()
				if p.isTriggerFile(event.Name, pattern) {
					p.flush(results)
					timeoutTicker.Reset(timeoutDuration)
				} else {
					// Check if it's a directory before adding to files
					stat, err := p.utils.Stat(event.Name)
					if err == nil && !stat.IsDir() {
						p.files[event.Name] = struct{}{}
					}
				}
				p.mu.Unlock()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}

func (p *TriggerPoller) isTriggerFile(path, pattern string) bool {
	name := filepath.Base(path)
	match, err := filepath.Match(pattern, name)
	if err != nil {
		// If pattern is invalid, fallback to exact match or contains
		return name == pattern || strings.Contains(name, pattern)
	}
	return match
}

func (p *TriggerPoller) flush(results chan<- []string) {
	if len(p.files) == 0 {
		return
	}
	batch := make([]string, 0, len(p.files))
	for f := range p.files {
		batch = append(batch, f)
	}
	results <- batch
	p.files = make(map[string]struct{})
}
