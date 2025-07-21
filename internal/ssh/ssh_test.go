package ssh

import (
	"context"
	"log"
	"log/slog"
	"testing"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh/internal/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// The target for our SSH client test functions.
	//
	// The SSH server construction only allows an IP address, which when not
	// supplied defaults to '0.0.0.0'. This is why 'mockListenHost' is not passed
	// as a parameter into 'mock.NewServer'.
	mockListenHost = "127.0.0.1"
	// The port for our SSH client test functions.
	//
	// Ports <1024 are privileged, so we use '2222'.
	mockListenPort uint16 = 2222
)

func TestSSH(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	slog.SetLogLoggerLevel(slog.LevelDebug)
	mock.SetLogger(slog.Default())
	// Generate a "user" keypair.
	userKeys, err := NewED25519KeyPair()
	require.NoError(t, err)
	// Convert the ed25519 private key to an ssh.Signer.
	//
	// The SSH client connection will sign messages with this key.
	userSigner, err := userKeys.Private.ToSSH()
	require.NoError(t, err)
	// Convert the ed25519 public key to an ssh.PublicKey
	//
	// The server will authenticate connections with this key.
	userPubKey, err := userKeys.Public.ToSSH()
	require.NoError(t, err)
	// Generate a "server" keypair
	serverKeys, err := NewED25519KeyPair()
	require.NoError(t, err)
	// Convert the server's ed25519 private key to an ssh.Signer.
	//
	// The server will sign responses to clients with this key.
	serverSigner, err := serverKeys.Private.ToSSH()
	require.NoError(t, err)
	// Convert the server's ed25519 public key to an ssh.PublicKey.
	//
	// The client will authenticate the server's host key using this.
	serverPubKey, err := serverKeys.Public.ToSSH()
	require.NoError(t, err)
	// Construct the mock SSH server on '0.0.0.0:[mockListenPort]'
	server, err := mock.NewServer(
		t,
		mockListenPort,
		serverSigner,
		mock.PublicKeyCallback(t, userPubKey),
	)
	require.NoError(t, err)
	// Begin serving SSH server connections
	reqs, msgs, err := server.ListenAndServe(t, t.Context())
	require.NoError(t, err)
	// Connect to the server with our "user" keypair
	client, err := Connect(
		mockListenHost,
		mockListenPort,
		"hellope",
		userSigner,
		serverPubKey,
	)
	require.NoError(t, err)
	// Execute two 'echo' commands in the 'Bash' shell.
	//
	// Our mock server will produce no stdout from these commands, so we discard
	// those returned values.
	const cmd1 = "echo 'Hello, world!'"
	const cmd2 = "echo 'Goodbyte, world!'"
	_, _, err = ExecIn(
		client,
		ShellBash,
		cmd1,
		cmd2,
	)
	require.NoError(t, err)
	// Expect a request via the reqs channel stipulating the 'Bash' shell
	req := <-reqs
	require.Equal(t, req.Type, "exec")
	require.Equal(t, string(req.Payload), "/usr/bin/env bash")
	// Expect the 'Bash' commands we sent above in the order we sent them in
	msg := <-msgs
	require.Equal(t, cmd1, msg)
	msg = <-msgs
	require.Equal(t, cmd2, msg)
	// Gracefully shutdown our mock SSH server with a 2-second deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(ctx))
}

func TestJoinHostPort(t *testing.T) {
	// invalid ip4 address
	s, err := joinHostPort("192.168.255.", 33)
	assert.Error(t, err)
	assert.Equal(t, "", s)
	// invalid ipv6 address
	s, err = joinHostPort("2001:db8:3333:4444:5555:6666:7777", 33)
	assert.Error(t, err)
	assert.Equal(t, "", s)
	// valid ipv4 address
	s, err = joinHostPort("192.168.255.50", 33)
	assert.NoError(t, err)
	assert.Equal(t, "192.168.255.50:33", s)
	// valid ipv6 address
	s, err = joinHostPort("2001:db8:3333:4444:5555:6666:7777:8888", 33)
	assert.NoError(t, err)
	assert.Equal(t, "[2001:db8:3333:4444:5555:6666:7777:8888]:33", s)
	// valid hostname
	s, err = joinHostPort("localhost", 33)
	assert.NoError(t, err)
	assert.Equal(t, "127.0.0.1:33", s)
}
