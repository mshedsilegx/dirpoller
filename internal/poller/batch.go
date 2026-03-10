package poller

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"time"
)

type BatchPoller struct {
	cfg   *config.Config
	utils OSUtils
}

func NewBatchPoller(cfg *config.Config) *BatchPoller {
	return &BatchPoller{
		cfg:   cfg,
		utils: NewOSUtils(),
	}
}

func (p *BatchPoller) Start(ctx context.Context, results chan<- []string) error {
	timeoutTicker := time.NewTicker(time.Duration(p.cfg.Poll.BatchTimeoutSeconds) * time.Second)
	defer timeoutTicker.Stop()

	pollTicker := time.NewTicker(1 * time.Second)
	defer pollTicker.Stop()

	for {
		files, err := p.utils.GetFiles(p.cfg.Poll.Directory)
		if err != nil {
			return err
		}

		if len(files) >= p.cfg.Poll.Value {
			results <- files
			timeoutTicker.Reset(time.Duration(p.cfg.Poll.BatchTimeoutSeconds) * time.Second)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeoutTicker.C:
			if len(files) > 0 {
				results <- files
			}
		case <-pollTicker.C:
			// Just continue to next loop iteration to re-scan files
			continue
		}
	}
}
