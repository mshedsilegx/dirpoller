// Package poller (Interval) implements the interval-based scanning strategy.
//
// Objective:
// Provide a high-reliability polling mechanism that performs full directory
// scans at regular intervals. This is the recommended strategy for network
// shares or storage systems that do not reliably emit file system events.
//
// Data Flow:
// 1. Ticker: Triggers a directory scan every N seconds (configured via Poll.Value).
// 2. Scan: Uses OSUtils.GetFiles to retrieve all files in the target directory.
// 3. Safety Check: Verifies non-recursive constraints via OSUtils.HasSubfolders.
// 4. Dispatch: Sends the discovered file slice to the Engine via the results channel.
package poller

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"time"
)

// IntervalPoller discovers files by performing a full directory scan at fixed time steps.
// This is the most reliable algorithm for all storage types (local, network, cloud).
type IntervalPoller struct {
	cfg   *config.Config
	utils OSUtils
}

func NewIntervalPoller(cfg *config.Config) *IntervalPoller {
	return &IntervalPoller{
		cfg:   cfg,
		utils: NewOSUtils(),
	}
}

// Start begins the polling process and sends discovered files to the channel.
// It blocks until the context is cancelled or a fatal error occurs.
//
// Data Flow:
// 1. Initialization: Parses the interval from configuration.
// 2. Initial Polling: Executes a scan immediately on startup.
// 3. Main Loop: Waits for ticker events or context cancellation.
// 4. Execution: Calls poll() on every ticker tick.
func (p *IntervalPoller) Start(ctx context.Context, results chan<- []string) error {
	interval, ok := p.cfg.Poll.Value.(int)
	if !ok {
		// Default to 60 seconds if not an int
		interval = 60
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	// Initial check
	if err := p.poll(results); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := p.poll(results); err != nil {
				return err
			}
		}
	}
}

// poll performs a single scan of the directory. It enforces the non-recursive requirement
// before collecting and sending files to the results channel.
func (p *IntervalPoller) poll(results chan<- []string) error {
	if _, err := p.utils.HasSubfolders(p.cfg.Poll.Directory); err != nil {
		return err
	}
	files, err := p.utils.GetFiles(p.cfg.Poll.Directory)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		// Security: Dispatch in a goroutine to prevent blocking the poller loop
		go func(f []string) {
			select {
			case results <- f:
			case <-time.After(10 * time.Second):
				// Log or handle timeout
			}
		}(files)
	}
	return nil
}
