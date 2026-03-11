// Package integrity provides logic for verifying that files are fully written and consistent.
package integrity

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"criticalsys.net/dirpoller/internal/poller"
	"github.com/cespare/xxhash/v2"
)

// Verifier orchestrates multiple attempts to ensure file integrity.
// A file is considered "stable" only if its property (size, timestamp, or hash)
// remains unchanged across N consecutive attempts at specific intervals.
type Verifier struct {
	cfg   *config.Config
	utils poller.OSUtils
}

// NewVerifier creates a new integrity verifier instance.
func NewVerifier(cfg *config.Config) *Verifier {
	return &Verifier{
		cfg:   cfg,
		utils: poller.NewOSUtils(),
	}
}

// Verify checks file consistency across multiple attempts.
// 1. First checks for Windows-native file locks.
// 2. Performs the stability check using the configured algorithm.
func (v *Verifier) Verify(ctx context.Context, path string) (bool, error) {
	for i := 0; i < v.cfg.Integrity.VerificationAttempts; i++ {
		// 1. Check Windows lock first
		locked, err := v.utils.IsLocked(path)
		if err != nil {
			return false, fmt.Errorf("failed to check lock for %s: %w", path, err)
		}
		if locked {
			return false, nil // File is locked, retry later
		}

		// 2. Perform integrity check based on algorithm
		currentValue, err := v.getIntegrityValue(path)
		if err != nil {
			return false, err
		}

		// Wait for the configured interval
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(v.cfg.Integrity.VerificationInterval) * time.Second):
		}

		// Re-verify after interval
		newValue, err := v.getIntegrityValue(path)
		if err != nil {
			return false, err
		}

		if currentValue != newValue {
			return false, nil // File is still being modified
		}
	}

	return true, nil
}

func (v *Verifier) getIntegrityValue(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	switch v.cfg.Integrity.Algorithm {
	case config.IntegritySize:
		return fmt.Sprintf("%d", info.Size()), nil
	case config.IntegrityTimestamp:
		return info.ModTime().String(), nil
	case config.IntegrityHash:
		return v.calculateHash(path)
	default:
		return "", fmt.Errorf("unsupported integrity algorithm: %s", v.cfg.Integrity.Algorithm)
	}
}

// CalculateHash calculates the xxHash-64 of a file.
// This is used for both the stability check algorithm and for logging in the activity report.
func (v *Verifier) CalculateHash(path string) (string, error) {
	return v.calculateHash(path)
}

func (v *Verifier) calculateHash(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("Warning: failed to close file %s: %v\n", path, closeErr)
		}
	}()

	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum64()), nil
}
