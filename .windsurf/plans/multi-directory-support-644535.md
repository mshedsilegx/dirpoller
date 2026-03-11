# Plan: Support Multiple Directory Polling

This plan outlines the changes required to allow DirPoller to monitor multiple directories simultaneously, each with its own polling, integrity, and action configuration.

## Proposed Changes

### 1. Configuration Model Update (`internal/config/config.go`)
- Update the `Config` struct to hold a slice of `TaskConfig` instead of a single set of properties.
- Rename the current `Config` fields into a new `TaskConfig` struct.
- Update `LoadConfig`, `setDefaults`, and `validate` to handle the slice of tasks.

### 2. Engine Orchestration Update (`internal/service/engine.go`)
- Modify `Engine` to manage a collection of task-specific components.
- Introduce a `TaskRunner` struct to encapsulate the logic for a single directory (poller, verifier, handler, archiver).
- Update `NewEngine` to initialize a `TaskRunner` for each task in the configuration.
- Update `Run` to start all `TaskRunners` concurrently using a `sync.WaitGroup` or similar pattern.

### 3. CLI and Service Integration (`cmd/dirpoller/main.go`, `internal/service/service_windows.go`)
- Ensure the `main` function and Windows service wrapper correctly initialize the multi-task engine.
- Update error handling to ensure one failing task doesn't necessarily stop others (depending on requirements).

## Verification Plan

### Automated Testing
- Create a test configuration with multiple directories.
- Verify that `LoadConfig` correctly parses and validates multiple tasks.
- Mock or use temporary directories to verify concurrent polling and processing.

### Manual Verification
- Run the CLI with a configuration containing two different directories.
- Drop files into both directories and verify they are processed independently according to their respective rules.
- Verify Windows Service lifecycle (Start/Stop) with multiple active tasks.
