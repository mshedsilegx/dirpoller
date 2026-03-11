package action

import (
	"bytes"
	"context"
	"criticalsys.net/dirpoller/internal/config"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Mock objects for SFTP testing
type mockSFTPClient struct {
	mkdirAllErr error
	createErr   error
	getwdErr    error
	closeErr    error
	createdFile *mockSFTPFile
}

func (m *mockSFTPClient) MkdirAll(path string) error { return m.mkdirAllErr }
func (m *mockSFTPClient) Create(path string) (SFTPFile, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.createdFile = &mockSFTPFile{Buffer: new(bytes.Buffer)}
	return m.createdFile, nil
}
func (m *mockSFTPClient) Getwd() (string, error) { return "/", m.getwdErr }
func (m *mockSFTPClient) Close() error           { return m.closeErr }

type mockSFTPFile struct {
	*bytes.Buffer
	closeErr error
}

func (m *mockSFTPFile) Close() error { return m.closeErr }

type mockSSHClient struct {
	closeErr error
}

func (m *mockSSHClient) Close() error { return m.closeErr }

func TestSFTPExecuteSuccess(t *testing.T) {
	testDir := getTestDir("SFTPExecuteSuccess")
	localFile := filepath.Join(testDir, "upload.txt")
	content := []byte("sftp-mock-data")
	_ = os.WriteFile(localFile, content, 0644)

	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 1,
			SFTP: config.SFTPConfig{
				Host:       "localhost",
				Username:   "user",
				RemotePath: "/remote",
			},
		},
	}

	mockClient := &mockSFTPClient{}
	mockConn := &mockSSHClient{}

	h := NewSFTPHandler(cfg)
	h.client = mockClient
	h.conn = mockConn

	success, err := h.Execute(context.Background(), []string{localFile})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(success) != 1 || success[0] != localFile {
		t.Errorf("expected 1 success for %s, got %v", localFile, success)
	}

	if mockClient.createdFile == nil {
		t.Fatal("expected file to be created on mock SFTP server")
	}

	if mockClient.createdFile.String() != string(content) {
		t.Errorf("expected content %s, got %s", string(content), mockClient.createdFile.String())
	}
}

func TestSFTPReconnect(t *testing.T) {
	cfg := &config.Config{
		Action: config.ActionConfig{
			ConcurrentConnections: 1,
			SFTP: config.SFTPConfig{
				Host:       "localhost",
				Username:   "user",
				RemotePath: "/remote",
			},
		},
	}

	// First client fails Getwd (connection lost)
	mockClient1 := &mockSFTPClient{getwdErr: fmt.Errorf("connection lost")}
	mockConn1 := &mockSSHClient{}

	h := NewSFTPHandler(cfg)
	h.client = mockClient1
	h.conn = mockConn1

	// We can't easily mock the connect() call because it's a private method
	// that dials a real SSH server. But we can verify it attempts to close the old one.

	_, err := h.getOrCreateClient()

	// It should fail because connect() fails (no real SSH server)
	if err == nil {
		t.Error("expected error during reconnect attempt, got nil")
	}

	if h.client != nil {
		t.Error("expected old client to be cleared")
	}
}

func TestSFTPConnectInvalidKey(t *testing.T) {
	cfg := &config.Config{
		Action: config.ActionConfig{
			SFTP: config.SFTPConfig{
				SSHKeyPath: "C:/non_existent_key_12345",
			},
		},
	}
	h := NewSFTPHandler(cfg)
	_, _, err := h.connect()
	if err == nil {
		t.Error("expected error for non-existent SSH key, got nil")
	}
}

func TestSFTPConnectInvalidPassphrase(t *testing.T) {
	testDir := getTestDir("SFTPInvalidPassphrase")
	keyFile := filepath.Join(testDir, "id_rsa")
	// Write a fake but valid-looking private key
	_ = os.WriteFile(keyFile, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nbad-data\n-----END OPENSSH PRIVATE KEY-----"), 0600)

	cfg := &config.Config{
		Action: config.ActionConfig{
			SFTP: config.SFTPConfig{
				SSHKeyPath:       keyFile,
				SSHKeyPassphrase: "wrong",
			},
		},
	}
	h := NewSFTPHandler(cfg)
	_, _, err := h.connect()
	if err == nil {
		t.Error("expected error for invalid key/passphrase, got nil")
	}
}
