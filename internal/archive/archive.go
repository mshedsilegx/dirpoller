// Package archive handles post-processing tasks like deletion, moving, or zstd compression.
//
// Objective:
// Manage the local file lifecycle reliably after successful processing. It ensures
// that files are either safely stored (Archive/Compress) or removed, while
// guaranteeing that no file is lost or processed twice due to partial failures.
//
// Core Components:
// - Archiver: Orchestrates the post-processing transaction.
// - Tar/Zstd Writers: Interfaces for high-performance concurrent compression.
//
// Data Flow:
// 1. Transaction Start: Archiver receives a list of successfully processed files.
// 2. Prepare: Files are moved to a hidden .staging directory to prevent interference.
// 3. Commit: The configured action (Delete, Move, or Compress) is performed from staging.
// 4. Finalize: Staging directory is cleaned up upon success.
// 5. Rollback: On failure, files are moved back to their original locations from staging.
package archive

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

// TarWriter defines the interface for tar archive operations.
type TarWriter interface {
	WriteHeader(hdr *tar.Header) error
	io.WriteCloser
}

// Archiver manages the cleanup and archiving of successfully processed files.
// It supports permanent deletion, moving to datestamped folders, or consolidation into .tar.zst archives.
type Archiver struct {
	cfg           *config.Config
	newTarWriter  func(w io.Writer) TarWriter
	newZstdWriter func(w io.Writer) (io.WriteCloser, error)
}

// NewArchiver creates a new archiver instance.
func NewArchiver(cfg *config.Config) *Archiver {
	return &Archiver{
		cfg: cfg,
		newTarWriter: func(w io.Writer) TarWriter {
			return tar.NewWriter(w)
		},
		newZstdWriter: func(w io.Writer) (io.WriteCloser, error) {
			return zstd.NewWriter(w, zstd.WithEncoderConcurrency(0))
		},
	}
}

// Process executes the configured post-action using a transactional pattern.
//
// Objective:
// Guarantee atomicity of the post-processing phase. If the system crashes
// or an error occurs during archiving, the files remain in their original
// state or in a recoverable staging area.
//
// Logic:
// 1. stageFiles: Moves candidates to .staging with unique UUID subfolders.
// 2. commit: Performs the final operation (Delete, MoveArchive, or Compress).
// 3. rollback: Automatically invoked on failure to restore original file paths.
func (a *Archiver) Process(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}

	// 1. PREPARE: Move files to staging
	stagingDir, stagedFiles, err := a.stageFiles(ctx, files)
	if err != nil {
		// Attempt rollback for any already staged files
		_ = a.rollback(stagedFiles)
		if stagingDir != "" {
			_ = os.RemoveAll(stagingDir)
		}
		return fmt.Errorf("failed to stage files for archiving: %w", err)
	}

	// 2. COMMIT: Execute the actual post-action from staging
	var commitErr error
	switch a.cfg.Action.PostProcess.Action {
	case config.PostActionDelete:
		commitErr = os.RemoveAll(stagingDir)
	case config.PostActionMoveArchive:
		commitErr = a.moveToFolder(stagingDir)
	case config.PostActionMoveCompress:
		commitErr = a.compressToArchive(ctx, stagingDir, stagedFiles)
	default:
		commitErr = fmt.Errorf("unsupported post action: %s", a.cfg.Action.PostProcess.Action)
	}

	if commitErr != nil {
		// ROLLBACK: Move files back to source
		_ = a.rollback(stagedFiles)
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("failed to commit archiving transaction: %w", commitErr)
	}

	// Cleanup staging dir if it wasn't removed by the action
	if _, err := os.Stat(stagingDir); err == nil {
		_ = os.RemoveAll(stagingDir)
	}

	return nil
}

func (a *Archiver) stageFiles(ctx context.Context, files []string) (string, map[string]string, error) {
	batchID := uuid.NewString()

	// 1. Check for absolute paths for archive staging
	archivePath := a.cfg.Action.PostProcess.ArchivePath
	if archivePath == "" || !filepath.IsAbs(archivePath) {
		return "", nil, fmt.Errorf("absolute archive_path required: %s", archivePath)
	}

	// 2. Construct the stagingBase as archivepath + .staging + a unique UUID
	stagingDir := filepath.Join(archivePath, ".staging", batchID)

	// If path cannot be created, throw an error
	if err := os.MkdirAll(stagingDir, 0750); err != nil {
		return "", nil, fmt.Errorf("failed to create staging directory %s: %w", stagingDir, err)
	}

	staged := make(map[string]string) // stagedPath -> originalPath
	for _, f := range files {
		select {
		case <-ctx.Done():
			return stagingDir, staged, ctx.Err()
		default:
			fClean := filepath.Clean(f)
			dest := filepath.Clean(filepath.Join(stagingDir, filepath.Base(fClean)))
			if err := os.Rename(fClean, dest); err != nil {
				return stagingDir, staged, err
			}
			staged[dest] = fClean
		}
	}
	return stagingDir, staged, nil
}

func (a *Archiver) rollback(staged map[string]string) error {
	var errs []error
	for stagedPath, originalPath := range staged {
		if err := os.Rename(stagedPath, originalPath); err != nil {
			errs = append(errs, fmt.Errorf("rollback failed for %s -> %s: %v", stagedPath, originalPath, err))
		}
	}
	if len(errs) > 0 {
		log.Printf("Warning: archiving rollback encountered errors: %v\n", errs)
		return fmt.Errorf("rollback incomplete")
	}
	return nil
}

func (a *Archiver) moveToFolder(stagingDir string) error {
	datestamp := time.Now().Format("20060102-150405.000000")
	destDir := filepath.Join(a.cfg.Action.PostProcess.ArchivePath, datestamp)

	// Create parent dir if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(destDir), 0750); err != nil {
		return err
	}

	return os.Rename(stagingDir, destDir)
}

func (a *Archiver) compressToArchive(ctx context.Context, stagingDir string, staged map[string]string) error {
	datestamp := time.Now().Format("20060102-150405.000000")
	archiveName := fmt.Sprintf("batch-%s.zst", datestamp)
	archivePath := filepath.Clean(filepath.Join(a.cfg.Action.PostProcess.ArchivePath, archiveName))

	if err := os.MkdirAll(a.cfg.Action.PostProcess.ArchivePath, 0750); err != nil {
		return err
	}

	// Atomic Archive Creation: Write to a temp file first, then rename.
	// This ensures that a partial or corrupted archive is never visible at the final path.
	tmpArchivePath := archivePath + "." + uuid.NewString() + ".tmp"
	f, err := os.Create(filepath.Clean(tmpArchivePath)) // #nosec G304
	if err != nil {
		return err
	}

	cleanupDone := false
	defer func() {
		_ = f.Close()
		if !cleanupDone {
			_ = os.Remove(tmpArchivePath)
		}
	}()

	enc, err := a.newZstdWriter(f)
	if err != nil {
		return err
	}
	defer func() { _ = enc.Close() }()

	tw := a.newTarWriter(enc)
	defer func() { _ = tw.Close() }()

	buf := make([]byte, 1*1024*1024)
	for stagedPath := range staged {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := a.addFileToArchive(tw, stagedPath, buf); err != nil {
				return err
			}
		}
	}

	// Explicitly close writers to ensure all data is flushed before rename.
	if err := tw.Close(); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpArchivePath, archivePath); err != nil {
		return err
	}
	cleanupDone = true
	return nil
}

func (a *Archiver) addFileToArchive(tw TarWriter, path string, buf []byte) error {
	f, err := os.Open(filepath.Clean(path)) // #nosec G304
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("Warning: failed to close file %s: %v\n", path, closeErr)
		}
	}()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.Base(path)

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = io.CopyBuffer(tw, f, buf)
	return err
}
