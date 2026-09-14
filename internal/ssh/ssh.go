package ssh

// ssh.go implements a facade over 'x/crypto/ssh', simplifying  SSH connection
// construction and SSH command execution/sequencing.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

const sshDefaultTimeout = 3 * time.Second

// keepaliveInterval is how often Connect sends an SSH-level keepalive request on
// an established connection. A long-lived session can legitimately go idle for
// minutes (e.g. a setup command blocked on `cloud-init status --wait`);
// intermediate network infrastructure (NAT gateways, proxies, idle-connection
// reapers) tears down connections that carry no bytes for a while, severing the
// session mid-command. Periodic keepalives keep real traffic flowing so those
// idle-reap timers never fire. Kept well under common reap windows (~60s) so a
// single missed tick is safe.
//
// This is the same mechanism as OpenSSH's ServerAliveInterval: both send
// keepalive@openssh.com global requests on the SSH connection at a fixed
// interval. We implement it here because golang.org/x/crypto/ssh has no
// built-in keepalive knob in ssh.ClientConfig. Unlike OpenSSH we do not pair it
// with a ServerAliveCountMax, so this only keeps bytes flowing, it is not a
// dead-peer detector, which is all the idle-reap problem needs.
const keepaliveInterval = 15 * time.Second

var (
	ErrSSHFailedDial   = fmt.Errorf("failed to establish TCP/22 connection")
	ErrFailedHostParse = fmt.Errorf("failed to parse hostname")
	ErrHostKeyInvalid  = fmt.Errorf("target's host key is invalid")
)

// Connect establishes an SSH (tcp/22) connection to 'host' on TCP port 'port'.
//
// 'host' can be any of: hostname, ipv4 address or ipv6 address. If 'host' is
// an empty string, ipv4 loopback is used.
//
// If 'port' is 0, a default value of '22' is used.
//
// 'keypair' is used for public key authentication when connecting to 'host'.
//
// Any values provided to 'hostKeys' will be used to compare against the host
// key offered by 'host' when a connection is attempted. If no 'hostKeys' value
// is provided, all host keys will be accepted.
func Connect(host string, port uint16, user string, keypair ssh.Signer, hostKeys ...ssh.PublicKey) (*ssh.Client, error) {
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 22
	}
	// Init the SSH config.
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(keypair),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			// If 'hostKeys' was not provided to 'Connect', simply return nil.
			//
			// This behavior is the same as 'ssh.InsecureIgnoreHostKey'.
			if len(hostKeys) == 0 {
				return nil
			}
			// If 'hostKeys' was provided to 'Connect', validate the SSH connection's
			// host key matches one of 'hostKeys'.
			for _, hostKey := range hostKeys {
				if bytes.Equal(hostKey.Marshal(), key.Marshal()) {
					return nil
				}
			}
			return ErrHostKeyInvalid
		},
		Timeout: sshDefaultTimeout,
	}
	// Parse the host + port combination to a ssh.Dial-compatible 'addr' (host+
	// port string).
	target, err := joinHostPort(host, port)
	if err != nil {
		return nil, err
	}
	// Dial the SSH connection.
	client, err := ssh.Dial("tcp", target, config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSSHFailedDial, err)
	}
	// Keep the connection alive during idle stretches so intermediate network
	// infrastructure doesn't reap a mostly-idle long-lived session.
	go keepAlive(client, keepaliveInterval)
	return client, nil
}

// keepAlive periodically sends a keepalive@openssh.com global request on client
// every interval (the same mechanism as OpenSSH's ServerAliveInterval) so a
// long-lived, idle SSH connection keeps carrying bytes. It returns the moment
// the connection closes (client.Wait unblocks on close/teardown), so it never
// outlives the connection it guards rather than lingering until the next tick.
func keepAlive(client *ssh.Client, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	closed := make(chan struct{})
	go func() {
		client.Wait() //nolint:errcheck // return value is the close reason; we only need the signal
		close(closed)
	}()
	for {
		select {
		case <-closed:
			return
		case <-t.C:
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

// joinHostPort parses and validates 'host' is a valid IPv4 or IPv6 address,
// then joins it with the port in the address-family-specific format.
//
// If 'host' is a hostname, the hostname will be resolved, then hostToPort will
// recurse using the first of the resolved addresses.
func joinHostPort(host string, port uint16) (string, error) {
	// Set up a context for deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Parse and resolve the provided host, join the result with the port as
	// appropriate.
	if addr := net.ParseIP(host); addr == nil {
		// Is it a hostname?
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return "", fmt.Errorf("%w: %s", ErrFailedHostParse, host)
		}
		// Select the first address we resolved and parse that.
		return joinHostPort(addrs[0], port)
	} else if ipv4 := addr.To4(); ipv4 != nil {
		// 'host' is ipv4
		return fmt.Sprintf("%s:%d", ipv4.String(), port), nil
	} else if ipv6 := addr.To16(); ipv6 != nil {
		// 'host' is ipv6
		return fmt.Sprintf("[%s]:%d", ipv6.String(), port), nil
	} else {
		return "", ErrFailedHostParse
	}
}

var (
	ErrSessionInit     = fmt.Errorf("failed to begin SSH session")
	ErrCMDExec         = fmt.Errorf("failed to execute SSH command")
	ErrInWait          = fmt.Errorf("SSH command did not exit cleanly")
	ErrStdinWrite      = fmt.Errorf("failed to write command to stdin")
	ErrStdinShortWrite = fmt.Errorf("short write to stdin")
	ErrStdStreamClose  = fmt.Errorf("encountered error closing standard stream")
)

// Exec executes a single command, directing standard out+error to the provided
// 'io.Writer's.
func Exec(client *ssh.Client, cmd string, stdout, stderr io.Writer) error {
	// Init an SSH session.
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionInit, err)
	}
	defer session.Close()
	// Wire up standard streams.
	session.Stdout = stdout
	session.Stderr = stderr
	// Execute the provided command.
	if err = session.Run(cmd); err != nil {
		return fmt.Errorf("%w: %w", ErrCMDExec, err)
	}
	return nil
}

// ExecIn executes all provided commands within the provided 'shell'.
func ExecIn(client *ssh.Client, shell Shell, stdout, stderr io.Writer, cmds ...string) error {
	cmd := "/usr/bin/env " + shell
	// Begin a new SSH session.
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSessionInit, err)
	}
	defer session.Close()
	// Wire up standard streams.
	//
	// We use 'io.Pipe' here to ensure the 'session' reads match 1:1 with our
	// stdin writes (sequenced commands).
	stdinr, stdinw := io.Pipe()
	defer stdinr.Close()
	defer stdinw.Close()
	session.Stdin = stdinr
	session.Stdout = stdout
	session.Stderr = stderr
	// Begin the command (we'll pass the input 'cmds' via stdin further down).
	if err = session.Start(cmd); err != nil {
		return fmt.Errorf("%w: %w", ErrCMDExec, err)
	}
	// Pass all provided commands in via stdin.
	for _, cmd := range cmds {
		// "Execute" the command.
		_, err := stdinw.Write([]byte(cmd + "\n"))
		if err != nil {
			return fmt.Errorf(
				"%w: %w",
				ErrStdinWrite, err,
			)
		}
	}
	// Manually close the PipeWriter.
	//
	// This will signal an EOF to the 'PipeReader' and is safe to call multiple
	// times.
	if err = stdinw.Close(); err != nil {
		return fmt.Errorf(
			"%w: %w",
			ErrStdStreamClose, err,
		)
	}
	// Wait for the command to send an 'exit-status' request.
	if err = session.Wait(); err != nil {
		return fmt.Errorf("%w: %w", ErrInWait, err)
	}
	return nil
}
