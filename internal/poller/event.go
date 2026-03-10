package poller

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"github.com/fsnotify/fsnotify"
)

type EventPoller struct {
	cfg   *config.Config
	utils OSUtils
}

func NewEventPoller(cfg *config.Config) *EventPoller {
	return &EventPoller{
		cfg:   cfg,
		utils: NewOSUtils(),
	}
}

func (p *EventPoller) Start(ctx context.Context, results chan<- []string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer func() {
		if closeErr := watcher.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close watcher: %v\n", closeErr)
		}
	}()

	// Track last processed events to deduplicate rapid fire notifications
	processed := make(map[string]time.Time)
	const debounceInterval = 500 * time.Millisecond

	// Perform initial check for existing subfolders
	if _, err := p.utils.HasSubfolders(p.cfg.Poll.Directory); err != nil {
		return err
	}

	// Add the directory to the watcher
	if err := watcher.Add(p.cfg.Poll.Directory); err != nil {
		return fmt.Errorf("failed to add directory to watcher: %w", err)
	}

	// Process existing files first
	files, err := p.utils.GetFiles(p.cfg.Poll.Directory)
	if err == nil && len(files) > 0 {
		results <- files
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// If a new directory is created, we must abort
			if event.Op&fsnotify.Create == fsnotify.Create {
				info, err := filepath.Abs(event.Name)
				if err == nil {
					stat, err := p.utils.Stat(info)
					if err == nil && stat.IsDir() {
						return fmt.Errorf("subfolder detected: %s", event.Name)
					}
				}
			}

			// When a file is written or created, we send it for integrity check
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				last, ok := processed[event.Name]
				if !ok || time.Since(last) > debounceInterval {
					processed[event.Name] = time.Now()
					results <- []string{event.Name}

					// Cleanup old entries from the map periodically
					if len(processed) > 1000 {
						for k, v := range processed {
							if time.Since(v) > debounceInterval*2 {
								delete(processed, k)
							}
						}
					}
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// In a real production app, we'd log this instead of returning
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}
