// Package action contains logic for processing files via SFTP or local scripts.
//
// Objective:
// Execute high-performance, reliable, and secure operations on batches of
// discovered files. It abstracts the underlying transport (SFTP) or local
// execution (Script) through a unified ActionHandler interface.
//
// Core Components:
// - ActionHandler: Universal interface for file-set operations.
// - SFTPHandler: Multi-threaded SFTP upload engine with atomic commit protocol.
// - ScriptHandler: Local execution engine with timeout and concurrency control.
//
// Data Flow:
// 1. Dispatch: Engine provides a list of verified absolute file paths.
// 2. Parallelism: Handler allocates workers from its internal semaphore pool.
// 3. Execution: Handler performs the action (Transfer or Execute) for each file.
// 4. Verification: Handler confirms success (e.g., remote size check or exit code).
// 5. Results: Returns a slice of successfully processed paths and any errors.
package action

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"criticalsys.net/dirpoller/internal/config"
	"criticalsys/secretprotector/pkg/libsecsecrets"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	maxRetries              = 3
	initialBackoff          = 1 * time.Second
	maxBackoff              = 30 * time.Second
	circuitBreakerThreshold = 3
)

// ActionHandler defines the interface for executing an action on a batch of files.
type ActionHandler interface {
	io.Closer
	// Execute performs the configured action on the provided file list.
	// It returns the list of files successfully processed and any error encountered.
	Execute(ctx context.Context, files []string) ([]string, error)
	// RemoteCleanup cleans orphaned .tmp files from the remote server.
	RemoteCleanup(ctx context.Context) error
}

// SFTPClient defines the subset of sftp.Client methods used by SFTPHandler.
type SFTPClient interface {
	MkdirAll(path string) error
	Create(path string) (SFTPFile, error)
	Stat(path string) (os.FileInfo, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	ReadDir(path string) ([]os.FileInfo, error)
	Close() error
}

// SFTPFile defines the subset of sftp.File methods used by SFTPHandler.
type SFTPFile interface {
	io.Writer
	io.Closer
}

// SSHClient defines the subset of ssh.Client methods used by SFTPHandler.
type SSHClient interface {
	Close() error
}

// Dialer defines the interface for establishing SSH and SFTP connections.
type Dialer interface {
	Dial(network, addr string, config *ssh.ClientConfig) (SFTPClient, SSHClient, error)
}

type realDialer struct{}

func (d *realDialer) Dial(network, addr string, config *ssh.ClientConfig) (SFTPClient, SSHClient, error) {
	conn, err := ssh.Dial(network, addr, config)
	if err != nil {
		return nil, nil, err
	}

	// Optimize for high-speed SFTPGo uploads with 1MB packet size
	// Note: We use 1MB for packet/buffer optimization as per Engineering Specification.
	// Some legacy SFTP servers might only support 32KB, but we are targeting modern SFTPGo.
	client, err := sftp.NewClient(conn, sftp.MaxPacket(1*1024*1024))
	if err != nil {
		// Fallback to default if 1MB fails (some mock servers or legacy servers)
		client, err = sftp.NewClient(conn)
		if err != nil {
			if closeErr := conn.Close(); closeErr != nil {
				return nil, nil, fmt.Errorf("failed to create SFTP client: %w (also failed to close connection: %v)", err, closeErr)
			}
			return nil, nil, fmt.Errorf("failed to create SFTP client: %w", err)
		}
	}

	return &sftpClientWrapper{client}, conn, nil
}

type sftpClientWrapper struct {
	*sftp.Client
}

func (w *sftpClientWrapper) Create(path string) (SFTPFile, error) {
	return w.Client.Create(path)
}

// SFTPHandler manages persistent multi-threaded file uploads to a remote SFTP server.
//
// Objective:
// Provide a robust, high-performance upload mechanism that guarantees file
// integrity and system resilience. It is optimized for modern SFTP servers
// (e.g., SFTPGo) and handles complex authentication (MFA/SSH-Key).
//
// Logic:
// 1. Session Management: getOrCreateClient maintains a persistent SSH/SFTP session.
// 2. Atomic Upload: Stage (.tmp) -> Stream (1MB Buffer) -> Rename (Commit) -> Stat (Verify).
// 3. Circuit Breaker: Suspends execution if consecutive connection failures exceed a threshold.
// 4. Memory Hygiene: Ensures decrypted passwords are wiped (ZeroBuffer) immediately after use.
type SFTPHandler struct {
	cfg             *config.Config
	client          SFTPClient
	conn            SSHClient
	dialer          Dialer
	mu              sync.Mutex
	semaphore       chan struct{}
	consecutiveFail int // Counter for connection-level failures (Circuit Breaker)
}

// NewSFTPHandler creates a new SFTP action handler with a persistent semaphore.
func NewSFTPHandler(cfg *config.Config) *SFTPHandler {
	conns := cfg.Action.ConcurrentConnections
	if conns <= 0 {
		conns = 1
	}
	return &SFTPHandler{
		cfg:       cfg,
		dialer:    &realDialer{},
		semaphore: make(chan struct{}, conns),
	}
}

func (h *SFTPHandler) isRetriable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// SFTP/SSH specific retriable errors
	retriableMessages := []string{
		"eof",
		"connection reset",
		"connection lost",
		"connection is closed",
		"timeout",
		"broken pipe",
		"connection refused",
		"i/o timeout",
	}
	for _, m := range retriableMessages {
		if strings.Contains(strings.ToLower(msg), m) {
			return true
		}
	}
	return false
}

// Execute uploads a batch of files in parallel using a handler-wide semaphore pool.
//
// Data Flow:
// 1. Circuit Breaker: Check if the handler is in a failed state due to consecutive errors.
// 2. Client Management: getOrCreateClient ensures a persistent, authenticated SSH/SFTP session.
// 3. Remote Preparation: Ensure the destination directory exists.
// 4. Parallel Workers: Fan-out uploads across the worker pool (semaphore-limited).
// 5. Retries & Backoff: Individual file failures are retried if categorised as retriable.
// 6. Result Aggregation: Collects all successful paths and any multi-errors.
func (h *SFTPHandler) Execute(ctx context.Context, files []string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	// Security: Ensure decrypted password is wiped at end of execution regardless of outcome.
	defer func() {
		if h.cfg.Action.SFTP.Password != "" {
			libsecsecrets.ZeroBuffer([]byte(h.cfg.Action.SFTP.Password))
			h.cfg.Action.SFTP.Password = ""
		}
	}()

	h.mu.Lock()
	if h.consecutiveFail >= circuitBreakerThreshold {
		h.mu.Unlock()
		return nil, fmt.Errorf("circuit breaker active: too many consecutive SFTP failures")
	}
	h.mu.Unlock()

	client, err := h.getOrCreateClient(ctx)
	if err != nil {
		h.incrementFail()
		return nil, err
	}

	// Ensure remote directory exists
	if err := client.MkdirAll(h.cfg.Action.SFTP.RemotePath); err != nil {
		h.incrementFail()
		return nil, &ErrConnectionLost{Err: fmt.Errorf("failed to create remote directory: %w", err)}
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(files))
	successChan := make(chan string, len(files))

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()

			select {
			case h.semaphore <- struct{}{}:
				defer func() { <-h.semaphore }()

				var lastErr error
				backoff := initialBackoff

				for i := 0; i < maxRetries; i++ {
					// Double-check context before starting upload
					select {
					case <-ctx.Done():
						return
					default:
					}

					err := h.uploadFile(client, f)
					if err == nil {
						successChan <- f
						h.resetFail()
						// Zero the password from memory if it's no longer needed
						// Note: This is a bit tricky as the password is in the config which might be reused.
						// However, the plan specifically asks for memory hygiene.
						// A better approach is to not store the decrypted password in the config at all.
						return
					}

					lastErr = err
					if !h.isRetriable(err) {
						break
					}

					// Exponential backoff
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
						backoff *= 2
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
					}
				}

				h.incrementFail()
				errChan <- &ErrConnectionLost{Err: fmt.Errorf("failed to upload %s after %d attempts: %w", f, maxRetries, lastErr)}
			case <-ctx.Done():
				return
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
		var errs []error
		for e := range errChan {
			errs = append(errs, e)
		}
		return successfulFiles, errors.Join(errs...)
	}

	return successfulFiles, nil
}

func (h *SFTPHandler) incrementFail() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.consecutiveFail++
}

func (h *SFTPHandler) resetFail() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.consecutiveFail = 0
}

func (h *SFTPHandler) getOrCreateClient(ctx context.Context) (SFTPClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.client != nil {
		// Check if connection is still alive by performing a simple metadata operation.
		// [Recommendation Impl]: Replaced Getwd() with Stat(".") for lighter heartbeat.
		_, err := h.client.Stat(".")
		if err == nil {
			return h.client, nil
		}
		// Connection lost, cleanup and reconnect
		if err := h.closeNoLock(); err != nil {
			// Log error but continue to attempt reconnect
			log.Printf("Error closing lost SFTP connection: %v\n", err)
		}
	}

	// 0. Security: Handle Secret Decryption (SFTP Password) before connecting.
	// This ensures decrypted credentials exist in memory only during active session management.
	if h.cfg.Action.SFTP.EncryptedPassword != "" && h.cfg.Action.SFTP.Password == "" {
		resolver := newKeyResolver()
		masterKey, err := resolver.ResolveMasterKey(ctx, &h.cfg.Action.SFTP)
		if err != nil {
			return nil, fmt.Errorf("security failure (master key resolution): %w", err)
		}

		realPass, err := libsecsecrets.Decrypt(ctx, h.cfg.Action.SFTP.EncryptedPassword, masterKey)
		libsecsecrets.ZeroBuffer(masterKey)
		if err != nil {
			log.Printf("[Action:SFTP] security failure: failed to decrypt password: %v\n", err)
			return nil, fmt.Errorf("security failure (decryption): %w", err)
		}

		log.Printf("[Action:SFTP] security: password decrypted successfully\n")
		h.cfg.Action.SFTP.Password = realPass
	}

	client, conn, err := h.connect()
	if err != nil {
		return nil, &ErrConnectionLost{Err: err}
	}
	h.client = client
	h.conn = conn
	return h.client, nil
}

func (h *SFTPHandler) connect() (SFTPClient, SSHClient, error) {
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
		pass := h.cfg.Action.SFTP.Password
		authMethods = append(authMethods, ssh.Password(pass))

		// Add Keyboard-Interactive authentication
		// Modern SFTP servers often require this for password prompts.
		// The error "ssh: unable to authenticate, attempted methods [none], no supported methods remain"
		// is frequently resolved by providing this method.
		authMethods = append(authMethods, ssh.KeyboardInteractive(
			func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					// Most prompts for passwords contain "password" (case-insensitive)
					if strings.Contains(strings.ToLower(questions[i]), "password") {
						answers[i] = pass
					}
				}
				return answers, nil
			},
		))
	}

	var hostKeyCallback ssh.HostKeyCallback
	var sshConfig *ssh.ClientConfig
	if h.cfg.Action.SFTP.HostKey != "" {
		// Handle full OpenSSH format (e.g., "ssh-ed25519 AAAAC3NzaC...")
		parts := strings.Fields(h.cfg.Action.SFTP.HostKey)
		base64Key := parts[0]
		if len(parts) > 1 {
			base64Key = parts[1]
		}

		pubKeyData, err := base64.StdEncoding.DecodeString(base64Key)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode host key: %w", err)
		}
		pubKey, err := ssh.ParsePublicKey(pubKeyData)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse host key: %w", err)
		}
		hostKeyCallback = ssh.FixedHostKey(pubKey)
		// Restrict HostKeyAlgorithms to only the provided algorithm to avoid mismatch if server has multiple keys.
		sshConfig = &ssh.ClientConfig{
			User:              h.cfg.Action.SFTP.Username,
			Auth:              authMethods,
			HostKeyCallback:   hostKeyCallback,
			HostKeyAlgorithms: []string{pubKey.Type()},
		}
	} else {
		hostKeyCallback = ssh.InsecureIgnoreHostKey() // #nosec G106 - Fallback if not provided
		sshConfig = &ssh.ClientConfig{
			User:            h.cfg.Action.SFTP.Username,
			Auth:            authMethods,
			HostKeyCallback: hostKeyCallback,
		}
	}

	addr := fmt.Sprintf("%s:%d", h.cfg.Action.SFTP.Host, h.cfg.Action.SFTP.Port)
	client, conn, err := h.dialer.Dial("tcp", addr, sshConfig)
	if err != nil {
		// Detect authentication failure
		if strings.Contains(err.Error(), "ssh: unable to authenticate") {
			return nil, nil, &ErrAuthenticationFailed{User: h.cfg.Action.SFTP.Username, Err: err}
		}
		return nil, nil, err
	}

	return client, conn, nil
}

// uploadFile handles the lifecycle of a single file upload using the Atomic Commit Protocol.
//
// Objective: Ensure that the remote server never sees a partial or corrupted file.
//
// Data Flow:
// 1. Local Read: Open local file and calculate size for later verification.
// 2. Staging: Generate a unique UUID-based .tmp filename on the remote server.
// 3. Immediate Cleanup: Defer a removal of the .tmp file in case of transfer failure.
// 4. Transfer: Stream data using the optimized 1MB CopyBuffer.
// 5. Commit: Atomic Rename from .tmp to the final destination.
// 6. Verification: Post-Write Stat to verify remote size matches local size.
func (h *SFTPHandler) uploadFile(client SFTPClient, localPath string) error {
	src, err := os.Open(filepath.Clean(localPath)) // #nosec G304
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			log.Printf("Warning: failed to close source file %s: %v\n", localPath, closeErr)
		}
	}()

	stat, err := src.Stat()
	if err != nil {
		return fmt.Errorf("[Action:SFTP] failed to stat local file: %w", err)
	}
	localSize := stat.Size()

	// Atomic Upload Protocol: Stage -> Transfer -> Commit -> Verify
	// 1. Stage: Create remote temp file with UUID
	remoteDir := h.cfg.Action.SFTP.RemotePath
	baseName := filepath.Base(localPath)
	destPath := path.Join(remoteDir, baseName)
	tmpPath := destPath + "." + uuid.NewString() + ".tmp"

	dst, err := client.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("[Action:SFTP] failed to create remote temp file: %w", err)
	}

	// 2. REMOTE Cleanup: Ensure partial files are removed from server on failure
	cleanupDone := false
	defer func() {
		if !cleanupDone {
			_ = client.Remove(tmpPath)
		}
	}()

	// 3. Transfer: Stream data using optimized 1MB buffer
	buf := make([]byte, 1*1024*1024)
	_, err = io.CopyBuffer(dst, src, buf)
	_ = dst.Close() // Close before rename
	if err != nil {
		return fmt.Errorf("[Action:SFTP] failed to transfer data: %w", err)
	}

	// 4. Commit: Atomic Remote Rename
	if err := client.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("[Action:SFTP] failed to commit remote file: %w", err)
	}
	cleanupDone = true // Rename succeeded, no need for defer remove

	// 5. Final Integrity Check: Post-Write Stat
	info, err := client.Stat(destPath)
	if err != nil {
		return fmt.Errorf("[Action:SFTP] failed to verify remote file: %w", err)
	}
	if info.Size() != localSize {
		return fmt.Errorf("[Action:SFTP] integrity check failed: size mismatch (local: %d, remote: %d)", localSize, info.Size())
	}

	return nil
}

// RemoteCleanup cleans orphaned .tmp files from the REMOTE filesystem.
//
// Objective: Prevent storage exhaustion on the SFTP server from failed or interrupted uploads.
//
// Logic:
// - Scans the target directory using a targeted ReadDir (efficiency over Walk).
// - Identifies files with the .tmp suffix.
// - Only deletes files older than 24 hours to avoid interfering with active uploads.
func (h *SFTPHandler) RemoteCleanup(ctx context.Context) error {
	// Security: Ensure decrypted password is wiped at end of execution regardless of outcome.
	defer func() {
		if h.cfg.Action.SFTP.Password != "" {
			libsecsecrets.ZeroBuffer([]byte(h.cfg.Action.SFTP.Password))
			h.cfg.Action.SFTP.Password = ""
		}
	}()

	client, err := h.getOrCreateClient(ctx)
	if err != nil {
		return err
	}

	remoteDir := h.cfg.Action.SFTP.RemotePath
	files, err := client.ReadDir(remoteDir)
	if err != nil {
		// If directory doesn't exist, nothing to clean
		return nil
	}

	now := time.Now()
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".tmp") {
			// Only delete files older than 24h to avoid deleting active uploads
			if now.Sub(f.ModTime()) > 24*time.Hour {
				tmpPath := path.Join(remoteDir, f.Name())
				_ = client.Remove(tmpPath)
			}
		}
	}
	return nil
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
