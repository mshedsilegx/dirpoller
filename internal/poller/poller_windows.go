package poller

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// WindowsPollerUtils provides Windows-native implementations for file locking and directory scanning.
type windowsOSUtils struct{}

func newOSUtils() OSUtils {
	return &windowsOSUtils{}
}

// IsLocked checks if a file is locked by another process using Windows-native CreateFile.
// It attempts to open the file with FILE_SHARE_NONE; a failure with ERROR_SHARING_VIOLATION indicates a lock.
func (w *windowsOSUtils) IsLocked(path string) (bool, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}

	// Try to open the file with FILE_SHARE_NONE to check for locks.
	// If it fails with ERROR_SHARING_VIOLATION, the file is locked.
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		0, // FILE_SHARE_NONE
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)

	if err != nil {
		if err == windows.ERROR_SHARING_VIOLATION {
			return true, nil
		}
		return false, err
	}
	defer func() {
		if closeErr := windows.CloseHandle(handle); closeErr != nil {
			fmt.Printf("Warning: failed to close file handle for %s: %v\n", path, closeErr)
		}
	}()

	return false, nil
}

// HasSubfolders performs a non-recursive scan of the directory to ensure no subfolders exist.
// If a subfolder is found, it returns an error as per the technical specification.
func (w *windowsOSUtils) HasSubfolders(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return true, fmt.Errorf("subfolder detected: %s", entry.Name())
		}
	}

	return false, nil
}

// GetFiles retrieves a list of all files in the directory.
// It returns an error if a subfolder is detected during the scan.
func (w *windowsOSUtils) GetFiles(dir string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("subfolder detected: %s", entry.Name())
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}

	return files, nil
}

func (w *windowsOSUtils) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
