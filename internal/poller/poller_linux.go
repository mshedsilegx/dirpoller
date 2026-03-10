//go:build linux

package poller

import (
	"fmt"
	"os"
)

type linuxOSUtils struct{}

func newOSUtils() OSUtils {
	return &linuxOSUtils{}
}

func (l *linuxOSUtils) IsLocked(path string) (bool, error) {
	// Linux specific lock check (e.g., flock or checking /proc/locks)
	// For now, return false as stub
	return false, nil
}

func (l *linuxOSUtils) HasSubfolders(dir string) (bool, error) {
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

func (l *linuxOSUtils) GetFiles(dir string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("subfolder detected: %s", entry.Name())
		}
		files = append(files, dir+"/"+entry.Name())
	}

	return files, nil
}

func (l *linuxOSUtils) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}
