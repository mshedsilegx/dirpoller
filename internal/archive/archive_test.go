package archive

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"os"
	"path/filepath"
	"testing"
)

var testBaseDir string

func TestMain(m *testing.M) {
	tempDir := os.Getenv("TEMP")
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	testBaseDir = filepath.Join(tempDir, "dirpoller_UTESTS", "archive")
	_ = os.MkdirAll(testBaseDir, 0750)

	code := m.Run()
	_ = os.RemoveAll(testBaseDir)
	os.Exit(code)
}

func getTestDir(name string) string {
	dir := filepath.Join(testBaseDir, name)
	_ = os.MkdirAll(dir, 0750)
	return dir
}

func TestArchiveDelete(t *testing.T) {
	testDir := getTestDir("Delete")
	testFile := filepath.Join(testDir, "to_delete.txt")
	_ = os.WriteFile(testFile, []byte("delete me"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			PostProcess: config.PostProcessConfig{
				Action: config.PostActionDelete,
			},
		},
	}

	a := NewArchiver(cfg)
	err := a.Process(context.Background(), []string{testFile})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestArchiveMove(t *testing.T) {
	testDir := getTestDir("Move")
	archiveDir := filepath.Join(testDir, "archive")
	testFile := filepath.Join(testDir, "to_move.txt")
	_ = os.WriteFile(testFile, []byte("move me"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			PostProcess: config.PostProcessConfig{
				Action:      config.PostActionMoveArchive,
				ArchivePath: archiveDir,
			},
		},
	}

	a := NewArchiver(cfg)
	err := a.Process(context.Background(), []string{testFile})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Verify file was moved to a subfolder
	entries, _ := os.ReadDir(archiveDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 subfolder in archive dir, got %d", len(entries))
	}

	subDir := filepath.Join(archiveDir, entries[0].Name())
	movedFile := filepath.Join(subDir, "to_move.txt")
	if _, err := os.Stat(movedFile); err != nil {
		t.Errorf("expected moved file at %s, got error: %v", movedFile, err)
	}
}

func TestArchiveCompress(t *testing.T) {
	testDir := getTestDir("Compress")
	archiveDir := filepath.Join(testDir, "archive")
	testFile := filepath.Join(testDir, "to_compress.txt")
	_ = os.WriteFile(testFile, []byte("compress me"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			PostProcess: config.PostProcessConfig{
				Action:      config.PostActionMoveCompress,
				ArchivePath: archiveDir,
			},
		},
	}

	a := NewArchiver(cfg)
	err := a.Process(context.Background(), []string{testFile})
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Verify .zst file exists
	entries, _ := os.ReadDir(archiveDir)
	found := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".zst" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected .zst archive to be created")
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected original file to be deleted after compression")
	}
}

func TestArchiveErrorPaths(t *testing.T) {
	testDir := getTestDir("ErrorPaths")
	a := NewArchiver(&config.Config{
		Action: config.ActionConfig{
			PostProcess: config.PostProcessConfig{
				Action: config.PostActionDelete,
			},
		},
	})

	t.Run("DeleteNonExistent", func(t *testing.T) {
		err := a.Process(context.Background(), []string{filepath.Join(testDir, "missing.txt")})
		if err == nil {
			t.Error("expected error deleting non-existent file, got nil")
		}
	})

	t.Run("MoveToInvalidPath", func(t *testing.T) {
		a2 := NewArchiver(&config.Config{
			Action: config.ActionConfig{
				PostProcess: config.PostProcessConfig{
					Action:      config.PostActionMoveArchive,
					ArchivePath: "||invalid||", // Invalid path characters
				},
			},
		})
		err := a2.Process(context.Background(), []string{filepath.Join(testDir, "test.txt")})
		if err == nil {
			t.Error("expected error moving to invalid path, got nil")
		}
	})
}
