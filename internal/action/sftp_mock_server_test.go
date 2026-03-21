// Package action_test provides a high-fidelity mock SFTP server for integration testing.
//
// Objective:
// Provide a realistic, in-memory SFTP server environment that simulates
// SSH handshakes, multiple authentication methods (Password, SSH-Key, MFA),
// and SFTP subsystem requests. It allows for high-fidelity integration
// testing of the SFTPHandler without requiring a physical remote server.
//
// Core Components:
// - MockSFTPServer: Encapsulates the network listener, SSH configuration, and session handling.
// - InMemHandler: Leverages github.com/pkg/sftp's in-memory handlers for file operations.
//
// Data Flow:
// 1. Setup: NewMockServer generates a host key and starts a TCP listener on a random port.
// 2. Auth: SSH clients connect and undergo authentication via PasswordCallback or PublicKeyCallback.
// 3. Subsystem: Upon a successful "sftp" subsystem request, an SFTP RequestServer is started.
// 4. Teardown: Close() shuts down the listener and stops the serving loop.
package action

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// MockSFTPServer encapsulates the test server state
type MockSFTPServer struct {
	Addr     string
	Config   *ssh.ServerConfig
	HostKey  ssh.PublicKey
	listener net.Listener
	stop     chan struct{}
}

// NewMockServer creates and starts a high-fidelity mock SFTP server for testing.
func NewMockServer(t *testing.T, authModes map[string]string) *MockSFTPServer {
	// 1. Generate Host Key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}

	hostKey := signer.PublicKey()

	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			pass, ok := authModes["password"]
			if ok && conn.User() == "testuser" && string(password) == pass {
				return nil, nil
			}
			// MFA check: if user is mfauser, we assume key was already checked
			if conn.User() == "mfauser" && string(password) == "mfapass" {
				return nil, nil
			}
			return nil, fmt.Errorf("password rejected for %q", conn.User())
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() == "mfauser" {
				// To simulate MFA (Key + Password), we return a PartialSuccessError.
				// This tells the SSH client that the key was accepted but more
				// authentication (like password) is required.
				return nil, &ssh.PartialSuccessError{
					Next: ssh.ServerAuthCallbacks{
						PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
							if string(password) == "mfapass" {
								return nil, nil
							}
							return nil, fmt.Errorf("MFA password rejected")
						},
					},
				}
			}
			if conn.User() == "keyuser" {
				return nil, nil
			}
			return nil, fmt.Errorf("public key rejected for %q", conn.User())
		},
	}
	config.AddHostKey(signer)

	// 2. Start Listener
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	server := &MockSFTPServer{
		Addr:     l.Addr().String(),
		Config:   config,
		HostKey:  hostKey,
		listener: l,
		stop:     make(chan struct{}),
	}

	go server.serve()

	return server
}

func (s *MockSFTPServer) serve() {
	for {
		nConn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				continue
			}
		}

		go s.handleConnection(nConn)
	}
}

func (s *MockSFTPServer) handleConnection(nConn net.Conn) {
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, s.Config)
	if err != nil {
		_ = nConn.Close()
		return
	}
	defer func() { _ = sConn.Close() }()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "unknown channel")
			continue
		}

		ch, reqs, err := newChan.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				if req.Type == "subsystem" && len(req.Payload) >= 4 && string(req.Payload[4:]) == "sftp" {
					_ = req.Reply(true, nil)

					// Use in-memory handlers for the SFTP server
					handlers := sftp.InMemHandler()
					server := sftp.NewRequestServer(ch, handlers)
					if err := server.Serve(); err != nil && err != io.EOF {
						_ = server.Close()
					}
					_ = ch.Close() // Ensure channel is closed after SFTP server finishes
					return
				} else {
					_ = req.Reply(false, nil)
				}
			}
		}(reqs)
	}
}

func (s *MockSFTPServer) Close() {
	close(s.stop)
	_ = s.listener.Close()
}
