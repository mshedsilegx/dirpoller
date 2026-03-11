package action

import (
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestSFTPAction(t *testing.T) {
	// Note: Proper SFTP mocking requires a lot of boilerplate (host keys, etc.)
	// We'll focus on testing the SFTPHandler's configuration and logic flow.

	testDir := getTestDir("SFTPAction")
	localFile := filepath.Join(testDir, "upload.txt")
	_ = os.WriteFile(localFile, []byte("sftp-data"), 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 2,
			SFTP: config.SFTPConfig{
				Host:       "127.0.0.1",
				Port:       22,
				Username:   "test",
				Password:   "pass",
				RemotePath: "/remote",
			},
		},
	}

	h := NewSFTPHandler(cfg)
	// We don't call Close() yet because we want to test Execute logic.

	// Since we can't easily spin up a full SFTP server without a host key in this environment,
	// we'll verify the Execute fails gracefully with connection error.
	ctx := context.Background()
	_, err := h.Execute(ctx, []string{localFile})
	if err == nil {
		t.Error("expected connection error for SFTP Execute, got nil")
	}
}
