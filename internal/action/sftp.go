// Package action contains logic for processing files via SFTP or local scripts.
package action

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"criticalsys.net/dirpoller/internal/config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// ActionHandler defines the interface for executing an action on a batch of files.
type ActionHandler interface {
	io.Closer
	// Execute performs the configured action on the provided file list.
	// It returns the list of files successfully processed and any error encountered.
	Execute(ctx context.Context, files []string) ([]string, error)
}

// SFTPHandler manages persistent multi-threaded file uploads to a remote SFTP server.
type SFTPHandler struct {
	cfg       *config.Config
	client    *sftp.Client
	conn      *ssh.Client
	mu        sync.Mutex
	semaphore chan struct{}
}

// NewSFTPHandler creates a new SFTP action handler with a persistent semaphore.
func NewSFTPHandler(cfg *config.Config) *SFTPHandler {
	return &SFTPHandler{
		cfg:       cfg,
		semaphore: make(chan struct{}, cfg.Action.ConcurrentConnections),
	}
}

// Execute uploads a batch of files in parallel using a handler-wide semaphore pool.
func (h *SFTPHandler) Execute(ctx context.Context, files []string) ([]string, error) {
	client, err := h.getOrCreateClient()
	if err != nil {
		return nil, err
	}

	// Ensure remote directory exists
	if err := client.MkdirAll(h.cfg.Action.SFTP.RemotePath); err != nil {
		return nil, fmt.Errorf("failed to create remote directory: %w", err)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(files))
	successChan := make(chan string, len(files))

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				return
			case h.semaphore <- struct{}{}:
				defer func() { <-h.semaphore }()
				if err := h.uploadFile(client, f); err != nil {
					errChan <- fmt.Errorf("failed to upload %s: %w", f, err)
				} else {
					successChan <- f
				}
			}
		}(file)
	}

	wg.Wait()
	close(errChan)
	close(successChan)

	var successfulFiles []string
	for f := range successChan {
		successfulFiles = append(successfulFiles, f)
	}

	if len(errChan) > 0 {
		return successfulFiles, <-errChan // Return successful ones and the first error encountered
	}

	return successfulFiles, nil
}

func (h *SFTPHandler) getOrCreateClient() (*sftp.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.client != nil {
		// Check if connection is still alive by performing a simple operation
		_, err := h.client.Getwd()
		if err == nil {
			return h.client, nil
		}
		// Connection lost, cleanup and reconnect
		if err := h.closeNoLock(); err != nil {
			// Log error but continue to attempt reconnect
			fmt.Printf("Error closing lost SFTP connection: %v\n", err)
		}
	}

	client, conn, err := h.connect()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SFTP: %w", err)
	}
	h.client = client
	h.conn = conn
	return h.client, nil
}

func (h *SFTPHandler) connect() (*sftp.Client, *ssh.Client, error) {
	var authMethods []ssh.AuthMethod

	// Support SSH Key
	if h.cfg.Action.SFTP.SSHKeyPath != "" {
		key, err := os.ReadFile(filepath.Clean(h.cfg.Action.SFTP.SSHKeyPath))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read SSH key: %w", err)
		}

		var signer ssh.Signer
		if h.cfg.Action.SFTP.SSHKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(h.cfg.Action.SFTP.SSHKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse SSH key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	// Support Password (either as primary auth or as MFA alongside Key)
	if h.cfg.Action.SFTP.Password != "" {
		authMethods = append(authMethods, ssh.Password(h.cfg.Action.SFTP.Password))
	}

	sshConfig := &ssh.ClientConfig{
		User:            h.cfg.Action.SFTP.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // #nosec G106 - In production, use proper host key verification
	}

	addr := fmt.Sprintf("%s:%d", h.cfg.Action.SFTP.Host, h.cfg.Action.SFTP.Port)
	conn, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial SSH: %w", err)
	}

	client, err := sftp.NewClient(conn)
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("failed to create SFTP client: %w (also failed to close connection: %v)", err, closeErr)
		}
		return nil, nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}

	return client, conn, nil
}

func (h *SFTPHandler) uploadFile(client *sftp.Client, localPath string) error {
	src, err := os.Open(filepath.Clean(localPath)) // #nosec G304
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close source file %s: %v\n", localPath, closeErr)
		}
	}()

	remotePath := filepath.ToSlash(filepath.Join(h.cfg.Action.SFTP.RemotePath, filepath.Base(localPath)))
	dst, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dst.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close remote file %s: %v\n", remotePath, closeErr)
		}
	}()

	_, err = io.Copy(dst, src)
	return err
}

// Close gracefully shuts down the SFTP client and underlying SSH connection.
func (h *SFTPHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closeNoLock()
}

func (h *SFTPHandler) closeNoLock() error {
	var err error
	if h.client != nil {
		err = h.client.Close()
		h.client = nil
	}
	if h.conn != nil {
		if connErr := h.conn.Close(); connErr != nil && err == nil {
			err = connErr
		}
		h.conn = nil
	}
	return err
}
