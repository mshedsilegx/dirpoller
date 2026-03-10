//go:build linux

package service

import (
	"log"
)

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
