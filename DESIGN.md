# DESIGN: Directory Poller

This document outlines the technical design and implementation details of the Directory Poller.

## 1. System Overview
DirPoller is a high-performance Go-based utility for Windows Server (2019+) designed to monitor directories and process files based on specific polling and integrity rules. It can run as a CLI or as a Windows Service.

## 2. Architecture & Components

### 2.0 OS Isolation & Portability
To ensure maximum native performance on Windows while maintaining a path for seamless Linux support, the application employs a strict interface-driven isolation strategy:

- **Platform-Specific Implementations**:
  - **Logging**: A generic `Logger` interface is implemented via `windowsLogger` (using the native **Windows EventLog**) and `linuxLogger` (using standard output).
  - **OS Utilities**: The `OSUtils` interface abstracts platform-specific file operations. On Windows, this includes using `CreateFile` with `FILE_SHARE_NONE` for robust lock detection.
- **Build Constraints**: Files are isolated using `//go:build` tags (e.g., `*_windows.go`, `*_linux.go`).
- **Interface Injection**: Core components like the `Engine`, `Poller`, and `Verifier` are entirely platform-agnostic, receiving OS-specific implementations via interfaces at runtime.

### 2.1 Polling Engine (`internal/poller`)
The engine supports three mutually exclusive algorithms:
- **Interval**: Scans the directory every `n` seconds.
- **Batch**: Accumulates files until a count threshold is reached. Includes a fallback timeout (default 10m) to process remaining files if the threshold isn't met.
- **Event**: Real-time monitoring using Windows-native `ReadDirectoryChangesW`. Includes a debounce mechanism (500ms) to handle rapid file system notifications.

**Recursive Constraint**: Recursive scanning is strictly forbidden. If a subfolder is detected:
- **CLI**: Aborts with an error.
- **Service**: Logs an error to the Windows EventLog and skips the current cycle.

### 2.2 Integrity Verification (`internal/integrity`)
Before any action, files must pass an integrity check (configurable `n` attempts every `n` seconds):
- **Lock Check**: Uses Windows-native `CreateFile` with `FILE_SHARE_NONE` to ensure the file is not being written to by another process.
- **Hash-based**: Uses `xxHash-64` to verify content consistency.
- **Timestamp-based**: Monitors `LastWriteTime` for changes.
- **Size-based**: Monitors file size for changes.

### 2.3 Action Handlers (`internal/action`)
- **Internal (SFTP)**: 
  - Multi-threaded upload using a semaphore-controlled worker pool.
  - Supports Password, SSH Key, or Multi-factor (Key + Password) authentication.
  - Performance: Parallel connections default to `Cores x 2`.
- **External (Script)**: 
  - Executes a configured script with the file path as an argument.
  - **Performance**: Now parallelized using a semaphore-controlled worker pool (matching SFTP concurrency).
  - Enforces a maximum execution timeout and uses absolute path validation for security.

### 2.4 Post-Processing (`internal/archive`)
After successful action execution, files undergo one of the following exclusive post-actions:
- **Delete**: Immediate removal of processed files.
- **Move (Archive)**: Moves files to a datestamped folder (`YYYYMMDD-HHMMSS.uuuuuu`).
- **Move (Compress)**: Consolidates the batch into a single `.zst` archive using multi-threaded compression (`klauspost/compress/zstd`).

## 3. Configuration System (`internal/config`)
All parameters are specified in a JSON file. The system implements:
- **Validation**: Ensures mandatory fields (directory, host, paths) are present and valid.
- **Defaults**: Sensible defaults for intervals, attempts, and performance settings.
- **Security**: Ensures script paths are absolute and validated.

## 4. Windows Integration (`internal/service`)
- **Service Lifecycle**: Implements `winsvc` for Start/Stop/Pause/Continue.
- **Logging**: Uses the native Windows EventLog (`DirPoller` source) for error reporting and status updates.

## 5. Security Design
- **Input Sanitization**: All file paths are converted to absolute paths and validated before processing.
- **Resource Management**: Throttled concurrency for both SFTP and Script actions. Streaming I/O and context-aware cancellation are used throughout to prevent memory/socket exhaustion and ensure clean shutdowns.
- **Authentication**: Secure handling of SSH keys and passwords via the `golang.org/x/crypto/ssh` module.
