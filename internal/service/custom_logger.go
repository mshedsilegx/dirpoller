package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type FileProcessInfo struct {
	Path  string
	Size  int64
	Hash  string
	Error string
}

type ExecutionSummary struct {
	StartTime time.Time
	Processed []FileProcessInfo
	Errors    []FileProcessInfo
}

type CustomLogger struct {
	mu            sync.Mutex
	logBaseName   string
	retention     int
	lastPurgeDate string // YYYYMMDD
}

func NewCustomLogger(logName string, retention int) *CustomLogger {
	return &CustomLogger{
		logBaseName: logName,
		retention:   retention,
	}
}

// LogProcess logs process-level events to the daily system log.
func (l *CustomLogger) LogProcess(msg string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.checkAndPurge()

	timestamp := time.Now().Format("20060102")
	ext := filepath.Ext(l.logBaseName)
	base := strings.TrimSuffix(l.logBaseName, ext)
	logFileName := fmt.Sprintf("%s_process_%s%s", base, timestamp, ext)

	// #nosec G304 - Log file name is constructed from safe base name and timestamp
	f, err := os.OpenFile(filepath.Clean(logFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open process log file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	now := time.Now().Format("2006-01-02 15:04:05")
	_, err = fmt.Fprintf(f, "%s|%s\n", now, msg)
	return err
}

func (l *CustomLogger) LogExecution(summary ExecutionSummary) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.checkAndPurge()

	if len(summary.Processed) == 0 && len(summary.Errors) == 0 {
		return nil
	}

	timestamp := time.Now().Format("20060102-150405")
	ext := filepath.Ext(l.logBaseName)
	base := strings.TrimSuffix(l.logBaseName, ext)
	logFileName := fmt.Sprintf("%s_activity_%s%s", base, timestamp, ext)

	// #nosec G304 - Log file name is constructed from safe base name and timestamp
	f, err := os.OpenFile(filepath.Clean(logFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create activity log file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// Write Status
	_, _ = fmt.Fprintln(f, "# Status")
	now := time.Now().Format("2006-01-02 15:04:05")
	total := len(summary.Processed) + len(summary.Errors)
	_, _ = fmt.Fprintf(f, "%s|total number of files picked up: %d\n", now, total)
	_, _ = fmt.Fprintf(f, "%s|number of files processed OK: %d\n", now, len(summary.Processed))
	_, _ = fmt.Fprintf(f, "%s|number of files in error: %d\n", now, len(summary.Errors))
	_, _ = fmt.Fprintln(f, "------")

	// Write List of files processed successfully
	_, _ = fmt.Fprintln(f, "# List of files processed successfully")
	for _, info := range summary.Processed {
		_, _ = fmt.Fprintf(f, "%s|%s|%d|%s\n", now, info.Path, info.Size, info.Hash)
	}
	_, _ = fmt.Fprintln(f, "------")

	// Write List of files in error
	fmt.Fprintln(f, "# List of files in error")
	for _, info := range summary.Errors {
		// New order: size|xxhash|error cause
		fmt.Fprintf(f, "%s|%s in error|%d|%s|%s\n", now, info.Path, info.Size, info.Hash, info.Error)
	}

	return nil
}

func (l *CustomLogger) checkAndPurge() {
	if l.retention <= 0 {
		return
	}

	today := time.Now().Format("20060102")
	if l.lastPurgeDate == today {
		return
	}

	l.purgeOldLogs()
	l.lastPurgeDate = today
}

func (l *CustomLogger) purgeOldLogs() {
	dir := filepath.Dir(l.logBaseName)
	if dir == "." {
		dir = ""
	}

	base := filepath.Base(l.logBaseName)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext) + "_"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -l.retention)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Matches either _process_YYYYMMDD or _activity_YYYYMMDD-HHMMSS
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ext) &&
			(strings.Contains(name, "_process_") || strings.Contains(name, "_activity_")) {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}
