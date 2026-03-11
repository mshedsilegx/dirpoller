# TESTING: DirPoller Unit Testing Suite

This document describes the comprehensive testing strategy and implementation for the DirPoller application, ensuring high code coverage and realistic behavior on Windows environments.

## 1. Test Environment Setup

To ensure tests do not interfere with the source code or production data, all tests use a dedicated temporary directory.

- **Base Directory**: `%TEMP%\dirpoller_UTESTS`
- **Isolation**: Each test case or package uses a unique subdirectory (e.g., `%TEMP%\dirpoller_UTESTS\poller`, `%TEMP%\dirpoller_UTESTS\integrity`).
- **Lifecycle Management**:
    - `TestMain`: Each package contains a `TestMain` that ensures the `%TEMP%\dirpoller_UTESTS` parent directory exists.
    - **Per-Test Cleanup**: Subdirectories are cleaned up *before* each test runs to ensure a deterministic state, but are *not* deleted globally in `TestMain` to allow parallel execution of multiple test packages.

## 2. Component Testing Scenarios

### 2.1 Polling Engine (`internal/poller`)
- **Interval Poller**: Verified that files are detected at the exact configured frequency.
- **Batch Poller**: 
    - **Event-Driven Threshold**: Refactored to use `fsnotify` for immediate discovery. Tests verify that files are dispatched only when the batch count is reached.
    - **Timeout Trigger**: Tests the fallback mechanism that dispatches files if the threshold isn't met within the timeout period.
- **Event Poller**: 
    - Uses real `fsnotify` events to simulate file system activity.
    - **Memory Leak Protection**: Verified TTL-based cleanup of the debouncing map to ensure stability in high-traffic environments.
- **Recursive Constraint**: Specifically tests that subfolder creation within a monitored directory triggers a failure/log as per the design specification.

### 2.2 Integrity Verification (`internal/integrity`)
- **Algorithm Support**: Comprehensive tests for `Size`, `Hash (xxHash-64)`, and `Timestamp` algorithms.
- **Consistency Check**: Simulates partial file writes by modifying files during the verification interval to ensure the verifier waits for the file to be "quiet".
- **Lock Detection**: Validates Windows-native `CreateFile` with `FILE_SHARE_NONE` behavior to prevent processing files held by other processes.

### 2.3 Action Handlers (`internal/action`)
- **SFTP Action**: 
    - Simulates connection failures and recovery logic.
    - **Security**: Validates **Host Key Verification** logic using base64-encoded keys.
    - Validates configuration parameters (Host, Port, User, MFA).
- **Script Action**: 
    - Generates real Windows `.bat` and `.ps1` scripts in `%TEMP%`.
    - Verifies parallel execution via worker pools.
    - Tests **Timeout Enforcement** for long-running scripts.
- **Common Logic**: Verifies **Multi-Error Reporting** using `errors.Join` to ensure all failures in a batch are surfaced.

### 2.4 Windows Service (`internal/service`)
A significant enhancement was made to the service layer to allow full testing without administrator privileges:
- **Interface-Driven Design**: The service logic was refactored to use a `ServiceManager` interface.
- **Simulated Installation**: Tests `InstallService` and `RemoveService` using a mock manager that tracks state without touching the real Windows Service Control Manager (SCM).
- **Control Loop**: Simulates `svc.ChangeRequest` (Stop, Pause, Continue) to verify the Engine's response to service state changes.

### 2.5 Custom Logging (`internal/service`)
- **Execution Logging**: Verified that `CustomLogger` creates datestamped log files with the correct structure (Status, Success, Errors).
- **Log Retention**: Tests simulate old log files and verify they are purged based on the `log_retention` days setting.
- **Empty Log Prevention**: Verified that no log file is created if no files were processed in an execution cycle.

## 3. How to Run Tests

Run the entire suite with coverage reporting:

```powershell
go test ./internal/... -v -cover
```

### Coverage Results (Current)
The suite maintains high coverage across all core packages:
- **Action**: 79.1%
- **Archive**: 75.9%
- **Config**: 75.0%
- **Integrity**: 77.8%
- **Poller**: 79.6%
- **Service**: 71.5%

## 4. Implementation Details & Lessons Learned
- **Path Handling**: Always use `filepath.ToSlash` or `filepath.Join` to ensure compatibility with Windows-style paths in JSON configurations.
- **Parallelism**: Global cleanup in `TestMain` was avoided to prevent race conditions when Go runs multiple package tests simultaneously. Each test manages its own subdirectory within the shared `%TEMP%\dirpoller_UTESTS` root.
- **Interface Refactoring**: The `internal/action` and `internal/service` packages were refactored to use interfaces (`SFTPClient`, `ServiceManager`). This allowed for 100% simulated testing of external dependencies like SFTP servers and the Windows Service Control Manager without requiring a real environment.
- **Deadlock Prevention**: Ensure mutexes are not held when calling methods that internally acquire the same lock (e.g., `getOrCreateClient`).

## 5. Test Details

| Category | Test Name | Purpose | Expected Result (PASS) | Failure Criteria (FAIL) |
| :--- | :--- | :--- | :--- | :--- |
| **Polling** | `TestIntervalPoller` | Verify files are detected at fixed intervals. | File detected and sent to results channel within 1s. | Timeout or file not detected. |
| **Polling** | `TestBatchPoller` | Verify batch threshold and timeout triggers. | Files sent when count is reached OR after timeout. | Threshold ignored or timeout doesn't fire. |
| **Polling** | `TestBatchPollerTimeout` | Verify timeout trigger for partial batches. | Files sent after timeout even if threshold not met. | Timeout fails to trigger. |
| **Polling** | `TestEventPoller` | Verify real-time `fsnotify` detection. | File creation triggers immediate detection. | Event missed or watcher fails. |
| **Polling** | `TestEventPollerDynamicSubfolder` | Verify detection of new subfolders during runtime. | Engine returns error when subfolder created. | Subfolder creation ignored. |
| **Polling** | `TestPollerSubfolderDetection` | Ensure recursive folders are blocked. | Error returned when subfolder is present. | Subfolder ignored or allowed. |
| **Integrity** | `TestIntegrityLockCheck` | Verify file lock detection. | `IsLocked` returns true for held files. | Locked file reported as free. |
| **Integrity** | `TestIntegrityHash` | Verify xxHash-64 consistency. | File verified after property is stable. | Hash mismatch or verify fails. |
| **Integrity** | `TestIntegrityChangingFile` | Verify waiting for "quiet" file. | `Verify` returns false while file is changing. | File verified while still growing. |
| **Integrity** | `TestIntegrityTimestamp` | Verify mod-time consistency check. | File verified via timestamp algorithm. | Verification fails for stable file. |
| **Action** | `TestScriptAction` | Verify `.bat` execution and params. | Script runs with exit 0, file marked success. | Exit code != 0 or script not found. |
| **Action** | `TestScriptActionTimeout` | Verify script execution timeout. | Context cancellation kills long-running script. | Script runs indefinitely. |
| **Action** | `TestScriptActionFailure` | Verify handling of script exit codes. | Error returned when script returns non-zero. | Failure treated as success. |
| **Action** | `TestSFTPAction` | Verify SFTP config and dial logic. | Connection error handled gracefully for bad host. | Panic or silent failure. |
| **Archive** | `TestArchiveDelete` | Verify `PostActionDelete`. | File removed from disk after processing. | File remains in source folder. |
| **Archive** | `TestArchiveMove` | Verify `PostActionMoveArchive`. | File moved to datestamped subfolder. | File missing or move failed. |
| **Archive** | `TestArchiveCompress` | Verify `PostActionMoveCompress`. | `.zst` archive created and original deleted. | Archive not found or original remains. |
| **Polling** | `TestTriggerPoller` | Verify trigger file pattern and timeout. | Batch processed on trigger OR timeout. | Trigger ignored or timeout fails. |
| **Logging** | `TestCustomLogger_LogExecution` | Verify JSON-like log structure and content. | Log file created with correct status and lists. | Log missing or incorrect format. |
| **Logging** | `TestCustomLogger_PurgeOldLogs` | Verify log retention/purging logic. | Logs older than N days are deleted. | Old logs remain on disk. |
| **Service** | `TestInstallRemoveService` | Verify SCM interaction logic (mocked). | Service created/deleted in mock manager. | Duplicate allowed or delete fails. |
| **Service** | `TestInstallServiceErrors` | Verify error handling in service installation. | Proper errors for existing services or log failures. | Silent installation failure. |
| **Service** | `TestEngineRunError` | Verify engine exit on poller failure. | Engine returns error if poller fails to start. | Error swallowed. |
