//go:build windows

package service

import (
	"golang.org/x/sys/windows/svc/eventlog"
)

type windowsLogger struct {
	elog *eventlog.Log
}

func newPlatformLogger(name string, isService bool) (Logger, error) {
	if !isService {
		return nil, nil // Fallback to standard log handled by Engine
	}
	elog, err := eventlog.Open(name)
	if err != nil {
		return nil, err
	}
	return &windowsLogger{elog: elog}, nil
}

func (l *windowsLogger) Error(id uint32, msg string) error {
	if l.elog == nil {
		return nil
	}
	return l.elog.Error(id, msg)
}

func (l *windowsLogger) Info(id uint32, msg string) error {
	if l.elog == nil {
		return nil
	}
	return l.elog.Info(id, msg)
}

func (l *windowsLogger) Close() error {
	if l.elog == nil {
		return nil
	}
	return l.elog.Close()
}
