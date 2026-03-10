# DirPoller

Efficient Go-based Windows Directory Poller (Windows Server 2019+) for automated file monitoring, verification, and secure transfer.

## Overview and Objectives
DirPoller is designed for high-performance directory monitoring in enterprise environments. It provides a robust way to:
1.  **Monitor** directories using multiple polling strategies (Interval, Batch, or Real-time Events).
2.  **Verify** file integrity before processing to ensure files are fully committed and not locked.
3.  **Execute** automated actions such as high-performance multi-threaded SFTP uploads or local script execution.
4.  **Archive** processed files using datestamped folders or consolidated high-efficiency `zstd` compression.

The application can run as a standalone CLI or be installed as a native Windows Service with full EventLog integration.

## Architecture and Design Choices
-   **Go-Based**: Leverages Go's efficient concurrency model for parallel integrity checks and multi-threaded SFTP uploads.
-   **Windows Native**: Uses `ReadDirectoryChangesW` for real-time events and `CreateFile` with specific sharing modes for robust lock detection on Windows Server 2019+.
-   **OS Isolation**: The application architecture is strictly decoupled. Platform-agnostic core logic (polling, integrity, actions) interacts with OS-native features through interfaces. Windows-specific implementations (e.g., `ReadDirectoryChangesW`, **Windows EventLog**, and `FILE_SHARE_NONE` locking) are isolated in `*_windows.go` files, while `*_linux.go` files provide a clean path for Linux support.
-   **Worker Pools**: Uses semaphore-controlled worker pools for SFTP transfers to optimize throughput without overwhelming system resources.
-   **Stream Processing**: Archiving logic uses `io.Copy` and streaming `zstd` writers to handle large files with minimal memory footprint.

## Dependencies
-   **fsnotify/fsnotify**: Cross-platform file system notifications (used as a fallback for generic events).
-   **klauspost/compress/zstd**: High-performance multi-threaded zstd compression.
-   **pkg/sftp**: Robust SFTP client implementation.
-   **cespare/xxhash/v2**: Extremely fast non-cryptographic hash algorithm for file integrity.
-   **golang.org/x/sys/windows**: Direct access to Windows APIs for service management and EventLog.

## Command Line Arguments
| Argument | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `-config` | String | (Required) | Full absolute path to the JSON configuration file. |
| `-install` | Boolean | `false` | Install the application as a native Windows service. |
| `-remove` | Boolean | `false` | Stop and remove the Windows service and EventLog source. |
| `-user` | String | `""` | Service account (e.g., `DOMAIN\User`). Defaults to `LocalSystem`. |
| `-pass` | String | `""` | Password for the specified service account. |
| `-debug` | Boolean | `false` | Run in interactive debug mode (console output for service events). |
| `-version` | Boolean | `false` | Print the application version and exit. |

## Windows Service Management
DirPoller provides native integration with the Windows Service Control Manager (SCM). 

### Administrative Requirements
**Note**: You must run PowerShell or Command Prompt as **Administrator** to perform installation or removal.

### Installation
The `-install` command handles service creation and EventLog source registration.

**1. Using LocalSystem (Default):**
```powershell
.\dirpoller.exe -install -config "C:\Program Files\DirPoller\config.json"
```

**2. Using a Service Account:**
```powershell
.\dirpoller.exe -install -config "C:\Program Files\DirPoller\config.json" -user "MYDOMAIN\svc_poller" -pass "SecurePass123"
```

### Removal
The `-remove` command gracefully stops the service (if running), deletes it from the SCM, and unregisters the EventLog source.
```powershell
.\dirpoller.exe -remove -config "C:\Program Files\DirPoller\config.json"
```

### Service Control and Monitoring
Once installed, the service is named `DirPoller` and can be managed using standard Windows commands or the GUI.

#### Standard Commands
- **Start**: `Start-Service DirPoller` or `net start DirPoller`
- **Stop**: `Stop-Service DirPoller` or `net stop DirPoller`
- **Restart**: `Restart-Service DirPoller`
- **Status**: `Get-Service DirPoller` or `sc.exe query DirPoller`

#### Verifying Logs
DirPoller logs all critical events (startup, errors, processing summaries) to the **Windows Event Log**.
1. Open **Event Viewer** (`eventvwr.msc`).
2. Navigate to `Windows Logs` -> `Application`.
3. Look for the Source: `DirPoller`.

#### Troubleshooting
If the service fails to start:
1. Check the Event Log for specific error messages.
2. Ensure the configuration file path provided during installation is an **absolute path** and is readable by the service account.
3. Run `.\dirpoller.exe -debug -config ...` to see real-time output in the console.

## Configuration JSON File
DirPoller is entirely driven by a structured JSON configuration. For reliability—especially when running as a Windows Service—all file and directory paths **must be absolute**.

The configuration is divided into four functional blocks:

### 1. Polling Strategy (`poll`)
Determines how the application discovers files in the target directory.

| Property | Type | Default | Logic / Purpose |
| :--- | :--- | :--- | :--- |
| `directory` | String | **Required** | The absolute local path to watch. Must exist and be accessible. |
| `algorithm` | String | `interval` | The detection method: `interval`, `batch`, or `event`. |
| `value` | Integer | `0` | **Context-sensitive**: In `interval` mode, this is seconds between scans. In `batch` mode, this is the number of files required to trigger processing. |
| `batch_timeout_seconds` | Integer | `600` | Only used in `batch` mode. Forces processing of any pending files if this many seconds pass, even if the `value` count isn't met. |

**Algorithm Details:**
*   **`interval`**: Performs a full directory scan at fixed time steps. Reliable for all storage types.
*   **`batch`**: Collects files as they arrive but waits until a specific volume is reached before executing actions.
*   **`event`**: Uses Windows `ReadDirectoryChangesW` for real-time, low-overhead detection. Best for high-traffic local disks.

---

### 2. Integrity Verification (`integrity`)
A safety gate that ensures files are fully written and closed by the source process before DirPoller touches them.

| Property | Type | Default | Logic / Purpose |
| :--- | :--- | :--- | :--- |
| `algorithm` | String | `timestamp` | The property to monitor for stability: `hash`, `timestamp`, or `size`. |
| `attempts` | Integer | `3` | Number of consecutive checks where the property must remain identical for the file to be considered "stable". |
| `interval` | Integer | `5` | Seconds to wait between each verification attempt. |

**Verification Flow:**
1.  **Lock Check**: DirPoller first attempts to open the file with exclusive access (`FILE_SHARE_NONE`). If Windows reports a sharing violation, the file is skipped.
2.  **Stability Check**: Once unlocked, the chosen `algorithm` is used. If the property (e.g., file size) changes between any of the `attempts`, the counter resets.

---

### 3. Action Handlers (`action`)
Defines the primary task to perform on verified files.

| Property | Type | Default | Logic / Purpose |
| :--- | :--- | :--- | :--- |
| `type` | String | **Required** | The processing engine to use: `sftp` or `script`. |
| `concurrent_connections` | Integer | CPU x 2 | The size of the worker pool. Limits how many files are processed in parallel across all batches. |

#### SFTP Handler (`action.sftp`)
| Property | Type | Default | Logic / Purpose |
| :--- | :--- | :--- | :--- |
| `host` / `port` | Mix | Req / `22` | Connection details for the remote server. |
| `username` | String | **Required** | SSH/SFTP username. |
| `password` | String | Optional | Used for standard password auth OR as the second factor in MFA. |
| `ssh_key_path` | String | Optional | Absolute path to a private SSH key (OpenSSH format). |
| `ssh_key_passphrase` | String | Optional | The passphrase required to decrypt the private key file (if encrypted). |
| `remote_path` | String | **Required** | The target directory on the SFTP server. |
| `post_action` | String | `delete` | Local lifecycle step after successful upload (see below). |
| `archive_path` | String | Optional | Required if `post_action` is a move or compress operation. |

#### Script Handler (`action.script`)
| Property | Type | Default | Logic / Purpose |
| :--- | :--- | :--- | :--- |
| `path` | String | **Required** | Absolute path to the script or executable. Supports `.exe`, `.bat`, `.ps1`, etc. |
| `timeout_seconds` | Integer | `60` | Maximum time allowed for the script to run per file before being killed. |

---

### 4. Post-Action Lifecycle
Determines what happens to the local file after the Action Handler confirms a successful operation.

*   **`delete`**: The file is permanently removed from the local `directory`.
*   **`move_archive`**: The file is moved to a timestamped subfolder (`YYYYMMDD-HHMMSS`) under the `archive_path`.
*   **`move_compress`**: The file is added to a high-performance, multi-threaded `zstd` archive in the `archive_path`, then the original is deleted.

---

### Full Configuration Example
```json
{
  "poll": {
    "directory": "C:\\Data\\Incoming",
    "algorithm": "event"
  },
  "integrity": {
    "algorithm": "hash",
    "attempts": 3,
    "interval": 2
  },
  "action": {
    "type": "sftp",
    "concurrent_connections": 4,
    "sftp": {
      "host": "sftp.internal.net",
      "port": 22,
      "username": "svc_poller",
      "ssh_key_path": "C:\\ProgramData\\DirPoller\\keys\\id_ed25519",
      "ssh_key_passphrase": "my-secret-pass",
      "remote_path": "/incoming/raw",
      "post_action": "move_compress",
      "archive_path": "C:\\Data\\Archive"
    }
  }
}
```

## Build
To build a production-ready, security-hardened binary for Windows, use the following template in a PowerShell script. This ensures a statically linked, position-independent executable (PIE) with all debug symbols stripped.

```powershell
# Environment Setup
$env:GOPROXY="https://proxy.golang.org,direct"
$env:CGO_ENABLED="0"

# Production Build Command
$version = Get-Content version.txt
go build -v `
    -buildvcs=false `
    -trimpath `
    -buildmode=pie `
    -ldflags "-s -w -X main.version=$version-$(git rev-parse --short HEAD)" `
    -o ./bin/dirpoller.exe `
    ./cmd/dirpoller
```

### Build Parameters:
-   **GOPROXY**: Uses the official Go proxy with direct fallback for dependency resolution.
-   **CGO_ENABLED=0**: Ensures a fully static binary with no external C library dependencies.
-   **-buildvcs=false**: Suppresses VCS information stamping for reproducible build environments.
-   **-trimpath**: Removes local absolute file paths from the binary (improves security/privacy).
-   **-buildmode=pie**: Generates a Position Independent Executable for enhanced exploit mitigation.
-   **-ldflags "-s -w ..."**: 
    - `-s -w`: Strips the symbol table and DWARF debug information (reduces binary size).
    - `-X main.version`: Injects the specific version and git commit hash into the application.

---

## Examples

### 1. Interactive CLI Mode
Run DirPoller directly in your terminal to monitor a directory and see processing results in real-time.
```powershell
.\dirpoller.exe -config "C:\Configs\prod_config.json"
```

### 2. Troubleshooting with Debug Mode
Use the `-debug` flag to enable verbose logging. This is highly recommended when testing new configurations or SFTP connectivity.
```powershell
.\dirpoller.exe -debug -config ".\test_config.json"
```

### 3. Version Verification
Check the installed version and build hash.
```powershell
.\dirpoller.exe -version
```

### 4. Service Installation (Service Account)
Install as a background service using a dedicated service account.
```powershell
.\dirpoller.exe -install -config "C:\DirPoller\config.json" -user "CORP\svc_poller" -pass "P@ssword123"
```

### 5. Service Removal
Stop and uninstall the service from the system.
```powershell
.\dirpoller.exe -remove -config "C:\DirPoller\config.json"
```
