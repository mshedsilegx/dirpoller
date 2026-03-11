package poller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"github.com/fsnotify/fsnotify"
)

// BatchPoller collects files as they arrive but waits until a specific volume (file count)
// is reached before executing actions. It uses file system notifications for low-latency detection
// and a timeout fallback to ensure files are not stranded.
type BatchPoller struct {
	cfg   *config.Config
	utils OSUtils
	mu    sync.Mutex
	files map[string]struct{}
}

func NewBatchPoller(cfg *config.Config) *BatchPoller {
	return &BatchPoller{
		cfg:   cfg,
		utils: NewOSUtils(),
		files: make(map[string]struct{}),
	}
}

func (p *BatchPoller) Start(ctx context.Context, results chan<- []string) error {
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
			p.files[f] = struct{}{}
		}
		p.checkThreshold(results)
		p.mu.Unlock()
	}

	timeoutTicker := time.NewTicker(time.Duration(p.cfg.Poll.BatchTimeoutSeconds) * time.Second)
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
				// Check if it's a directory before adding to files
				stat, err := p.utils.Stat(event.Name)
				if err == nil && stat.IsDir() {
					p.mu.Unlock()
					return fmt.Errorf("subfolder detected: %s", event.Name)
				}
				p.files[event.Name] = struct{}{}
				if p.checkThreshold(results) {
					timeoutTicker.Reset(time.Duration(p.cfg.Poll.BatchTimeoutSeconds) * time.Second)
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

func (p *BatchPoller) checkThreshold(results chan<- []string) bool {
	threshold, ok := p.cfg.Poll.Value.(int)
	if !ok {
		// Default to 1 if not an int
		threshold = 1
	}
	if len(p.files) >= threshold {
		p.flush(results)
		return true
	}
	return false
}

// flush sends all currently collected files as a single batch and clears the internal map.
func (p *BatchPoller) flush(results chan<- []string) {
	batch := make([]string, 0, len(p.files))
	for f := range p.files {
		batch = append(batch, f)
	}
	results <- batch
	p.files = make(map[string]struct{})
}
