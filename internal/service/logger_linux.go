//go:build linux

// Package service (Linux Logger) provides Linux-specific system logging integration.
//
// Objective:
// Implement the Logger interface for Linux environments, enabling application
// events to be routed to the system's logging infrastructure.
//
// Core Functionality:
// - Syslog Integration: Formats and dispatches logs to the standard Linux logging facility.
//
// Data Flow:
// 1. Dispatch: The Engine sends formatted messages and numeric IDs to the logger.
// 2. Execution: The logger appends messages to the system log via standard output/syslog.
package service

import (
	"log"
)

// linuxLogger implements the Logger interface for Linux.
//
// Objective: Provide standard log reporting for Linux systems,
// typically integrated with syslog or journald.
type linuxLogger struct{}

func newPlatformLogger(name string, isService bool) (Logger, error) {
	return &linuxLogger{}, nil
}

func (l *linuxLogger) Error(id uint32, msg string) error {
	log.Printf("ERROR [%d]: %s", id, msg)
	return nil
}

func (l *linuxLogger) Info(id uint32, msg string) error {
	log.Printf("INFO [%d]: %s", id, msg)
	return nil
}

func (l *linuxLogger) Close() error {
	return nil
}
