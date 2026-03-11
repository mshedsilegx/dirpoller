// Package service provides the core engine and Windows Service lifecycle management.
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileProcessInfo contains metadata and results for a single file processed during a cycle.
type FileProcessInfo struct {
	Path  string // Full path to the local file
	Size  int64  // Size in bytes
	Hash  string // xxHash-64 hex string
	Error string // Error message if the file failed processing
}

// ExecutionSummary aggregates the results of a single polling execution cycle.
type ExecutionSummary struct {
	StartTime time.Time         // When the cycle began
	Processed []FileProcessInfo // Files successfully transferred/processed
	Errors    []FileProcessInfo // Files that failed integrity or action steps
}

// CustomLogger implements the dual-track logging system specified in Section 6.
// It manages daily system process logs and per-execution activity logs.
type CustomLogger struct {
	mu            sync.Mutex
	logBaseName   string // The base name from config (e.g., C:\Logs\poller.log)
	retention     int    // Number of days to keep logs (0 = disabled)
	lastPurgeDate string // Tracked as YYYYMMDD to ensure purge runs only once per day
}

// NewCustomLogger initializes a new dual-track logger.
func NewCustomLogger(logName string, retention int) *CustomLogger {
	return &CustomLogger{
		logBaseName: logName,
		retention:   retention,
	}
}

// LogProcess logs operational events (start, stop, system errors) to a daily process log.
// On Windows, these events are also mirrored to the Windows Application Event Log via the Engine.
// Format: date stamp|message
// Naming: base_process_YYYYMMDD.log
func (l *CustomLogger) LogProcess(msg string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ensure retention runs at most once per calendar day
	l.checkAndPurge()

	timestamp := time.Now().Format("20060102")
	ext := filepath.Ext(l.logBaseName)
	base := strings.TrimSuffix(l.logBaseName, ext)
	logFileName := fmt.Sprintf("%s_process_%s%s", base, timestamp, ext)

	// Open in append mode, restricted permissions (0600)
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

// LogExecution generates a detailed report for a single polling cycle.
// It creates a unique file per run containing status counts and file-level details.
// Naming: base_activity_YYYYMMDD-HHMMSS.log
func (l *CustomLogger) LogExecution(summary ExecutionSummary) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.checkAndPurge()

	// Don't generate empty logs if nothing was found or errored
	if len(summary.Processed) == 0 && len(summary.Errors) == 0 {
		return nil
	}

	timestamp := time.Now().Format("20060102-150405")
	ext := filepath.Ext(l.logBaseName)
	base := strings.TrimSuffix(l.logBaseName, ext)
	logFileName := fmt.Sprintf("%s_activity_%s%s", base, timestamp, ext)

	// Create new file for this execution cycle
	// #nosec G304 - Log file name is constructed from safe base name and timestamp
	f, err := os.OpenFile(filepath.Clean(logFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create activity log file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	now := time.Now().Format("2006-01-02 15:04:05")

	// Section 1: Summary Counts
	_, _ = fmt.Fprintln(f, "# Status")
	total := len(summary.Processed) + len(summary.Errors)
	_, _ = fmt.Fprintf(f, "%s|total number of files picked up: %d\n", now, total)
	_, _ = fmt.Fprintf(f, "%s|number of files processed OK: %d\n", now, len(summary.Processed))
	_, _ = fmt.Fprintf(f, "%s|number of files in error: %d\n", now, len(summary.Errors))
	_, _ = fmt.Fprintln(f, "------")

	// Section 2: Successful Files
	// Format: date stamp|path|size|xxhash
	_, _ = fmt.Fprintln(f, "# List of files processed successfully")
	for _, info := range summary.Processed {
		_, _ = fmt.Fprintf(f, "%s|%s|%d|%s\n", now, info.Path, info.Size, info.Hash)
	}
	_, _ = fmt.Fprintln(f, "------")

	// Section 3: Error Files
	// Format: date stamp|path in error|size|xxhash|error cause
	_, _ = fmt.Fprintln(f, "# List of files in error")
	for _, info := range summary.Errors {
		_, _ = fmt.Fprintf(f, "%s|%s in error|%d|%s|%s\n", now, info.Path, info.Size, info.Hash, info.Error)
	}

	return nil
}

// checkAndPurge determines if the log retention logic should execute.
// It limits purging to once per calendar day to minimize filesystem impact.
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

// purgeOldLogs scans the log directory for files matching the base name prefix
// and deletes those whose modification time is older than the retention period.
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
		// Matches either _process_YYYYMMDD or _activity_YYYYMMDD-HHMMSS patterns
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ext) &&
			(strings.Contains(name, "_process_") || strings.Contains(name, "_activity_")) {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			// Delete if modification time is before the cutoff
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}
	}
}
