//go:build windows

// Package service (Windows Logger) provides Windows-specific system logging integration.
//
// Objective:
// Implement the Logger interface for Windows environments, enabling application
// events to be routed to the native Windows EventLog.
//
// Core Functionality:
// - EventLog Integration: Directly interfaces with the Windows Event Logging service.
//
// Data Flow:
// 1. Dispatch: The Engine sends formatted messages and numeric IDs to the logger.
// 2. Execution: The logger calls native Windows APIs to write entries to the "Application" log source.
package service

import (
	"golang.org/x/sys/windows/svc/eventlog"
)

type EventLogger interface {
	Error(id uint32, msg string) error
	Info(id uint32, msg string) error
	Close() error
}

// windowsLogger implements the Logger interface for Windows.
//
// Objective: Provide native Windows EventLog integration for system-level
// reporting and monitoring.
type windowsLogger struct {
	elog EventLogger
}

var eventLogOpen = func(name string) (EventLogger, error) {
	return eventlog.Open(name)
}

func newPlatformLogger(name string, isService bool) (Logger, error) {
	if !isService {
		return nil, nil // Fallback to standard log handled by Engine
	}
	elog, err := eventLogOpen(name)
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
