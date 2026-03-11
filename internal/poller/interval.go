package poller

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"time"
)

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

func (p *IntervalPoller) poll(results chan<- []string) error {
	if _, err := p.utils.HasSubfolders(p.cfg.Poll.Directory); err != nil {
		return err
	}
	files, err := p.utils.GetFiles(p.cfg.Poll.Directory)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		results <- files
	}
	return nil
}
