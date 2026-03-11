// Package archive handles post-processing tasks like deletion, moving, or zstd compression.
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
	"github.com/klauspost/compress/zstd"
)

// Archiver manages the cleanup and archiving of successfully processed files.
// It supports permanent deletion, moving to datestamped folders, or consolidation into .tar.zst archives.
type Archiver struct {
	cfg *config.Config
}

// NewArchiver creates a new archiver instance.
func NewArchiver(cfg *config.Config) *Archiver {
	return &Archiver{cfg: cfg}
}

// Process executes the configured post-action (Delete, Move, or Compress) on a batch of files.
func (a *Archiver) Process(ctx context.Context, files []string) error {
	switch a.cfg.Action.PostProcess.Action {
	case config.PostActionDelete:
		return a.deleteFiles(ctx, files)
	case config.PostActionMoveArchive:
		return a.moveToFolder(ctx, files)
	case config.PostActionMoveCompress:
		return a.compressToArchive(ctx, files)
	default:
		return nil
	}
}

func (a *Archiver) deleteFiles(ctx context.Context, files []string) error {
	for _, f := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := os.Remove(f); err != nil {
				return fmt.Errorf("failed to delete file %s: %w", f, err)
			}
		}
	}
	return nil
}

func (a *Archiver) moveToFolder(ctx context.Context, files []string) error {
	datestamp := time.Now().Format("20060102-150405.000000")
	destDir := filepath.Join(a.cfg.Action.PostProcess.ArchivePath, datestamp)

	if err := os.MkdirAll(destDir, 0750); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			dest := filepath.Join(destDir, filepath.Base(f))
			if err := os.Rename(f, dest); err != nil {
				return fmt.Errorf("failed to move file %s to %s: %w", f, dest, err)
			}
		}
	}
	return nil
}

// compressToArchive consolidates multiple files into a single standard .tar.zst archive.
// 1. Creates a tar container.
// 2. Compresses the stream using multi-threaded zstd.
// 3. Deletes the original files upon success.
func (a *Archiver) compressToArchive(ctx context.Context, files []string) error {
	datestamp := time.Now().Format("20060102-150405.000000")
	archiveName := fmt.Sprintf("batch-%s.zst", datestamp)
	archivePath := filepath.Clean(filepath.Join(a.cfg.Action.PostProcess.ArchivePath, archiveName))

	if err := os.MkdirAll(a.cfg.Action.PostProcess.ArchivePath, 0750); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}

	f, err := os.Create(archivePath) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("Warning: failed to close archive file %s: %v\n", archivePath, closeErr)
		}
	}()

	// Use multi-threaded zstd encoder
	enc, err := zstd.NewWriter(f, zstd.WithEncoderConcurrency(0)) // 0 means use all available cores
	if err != nil {
		return fmt.Errorf("failed to create zstd writer: %w", err)
	}
	defer func() {
		if closeErr := enc.Close(); closeErr != nil {
			log.Printf("Warning: failed to close zstd encoder: %v\n", closeErr)
		}
	}()

	// Use tar to consolidate files into one archive
	tw := tar.NewWriter(enc)
	defer func() {
		if closeErr := tw.Close(); closeErr != nil {
			log.Printf("Warning: failed to close tar writer: %v\n", closeErr)
		}
	}()

	for _, file := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := a.addFileToArchive(tw, file); err != nil {
				return err
			}
		}
	}

	// Ensure everything is flushed before deleting originals
	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to close tar writer during flush: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("failed to close zstd writer during flush: %w", err)
	}

	// After successful compression, delete original files
	return a.deleteFiles(ctx, files)
}

func (a *Archiver) addFileToArchive(tw *tar.Writer, path string) error {
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

	_, err = io.Copy(tw, f)
	return err
}
