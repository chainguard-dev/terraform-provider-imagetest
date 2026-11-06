// Package sshtest runs an SSH server for tests. It accepts any client key
// and answers each exec request with a handler, so a test can play the
// host side of a session: a lock holder that is busy, a command that fails.
package sshtest

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	issh "github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"golang.org/x/crypto/ssh"
)

// ExecHandler answers one exec request. It returns the exit status; the
// channel is closed by the server afterwards.
type ExecHandler func(cmd string, ch ssh.Channel) uint32

// Server is a test SSH server on the loopback address.
type Server struct {
	Addr string
	Port int32
	// HostKey is the server's host key, for the client to pin.
	HostKey ssh.PublicKey
	// Signer is the client's key. The server accepts any key, so the
	// client needs one only to complete the handshake.
	Signer ssh.Signer

	t       testing.TB
	l       net.Listener
	userKey issh.ED25519KeyPair
	handle  ExecHandler

	mu   sync.Mutex
	cmds []string
}

// New starts a server that answers exec requests with handle. It is
// stopped when the test ends.
func New(t testing.TB, handle ExecHandler) *Server {
	t.Helper()

	hostPair, err := issh.NewED25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := hostPair.Private.ToSSH()
	if err != nil {
		t.Fatal(err)
	}
	userPair, err := issh.NewED25519KeyPair()
	if err != nil {
		t.Fatal(err)
	}
	userSigner, err := userPair.Private.ToSSH()
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address %T", l.Addr())
	}

	s := &Server{
		Addr:    addr.IP.String(),
		Port:    int32(addr.Port),
		HostKey: hostSigner.PublicKey(),
		Signer:  userSigner,
		t:       t,
		l:       l,
		userKey: userPair,
		handle:  handle,
	}
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return nil, nil },
	}
	config.AddHostKey(hostSigner)
	go s.serve(config)
	return s
}

// ClientFiles writes the client's private key and a known_hosts file
// pinning the server into dir, as a driver configured with paths expects,
// and returns their paths.
func (s *Server) ClientFiles(dir string) (keyPath, knownHosts string) {
	s.t.Helper()
	key, err := s.userKey.Private.MarshalOpenSSH("sshtest")
	if err != nil {
		s.t.Fatal(err)
	}
	keyPath = filepath.Join(dir, "key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		s.t.Fatal(err)
	}
	knownHosts = filepath.Join(dir, "known_hosts")
	line := fmt.Sprintf("[%s]:%d %s", s.Addr, s.Port, ssh.MarshalAuthorizedKey(s.HostKey))
	if err := os.WriteFile(knownHosts, []byte(line), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return keyPath, knownHosts
}

// Commands returns the exec commands received so far.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.cmds...)
}

func (s *Server) serve(config *ssh.ServerConfig) {
	for {
		c, err := s.l.Accept()
		if err != nil {
			return
		}
		go func() {
			conn, chans, reqs, err := ssh.NewServerConn(c, config)
			if err != nil {
				return
			}
			defer conn.Close()
			go ssh.DiscardRequests(reqs)
			for newCh := range chans {
				if newCh.ChannelType() != "session" {
					_ = newCh.Reject(ssh.UnknownChannelType, "unknown channel type")
					continue
				}
				ch, chReqs, err := newCh.Accept()
				if err != nil {
					return
				}
				go s.session(ch, chReqs)
			}
		}()
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		// exec payload: uint32 length, then the command.
		cmd := string(req.Payload[4:])
		s.mu.Lock()
		s.cmds = append(s.cmds, cmd)
		s.mu.Unlock()
		_ = req.Reply(true, nil)
		status := s.handle(cmd, ch)
		var payload [4]byte
		payload[3] = byte(status)
		_, _ = ch.SendRequest("exit-status", false, payload[:])
		return
	}
}
