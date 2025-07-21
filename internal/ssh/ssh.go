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
	return client, nil
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
		panic("impossible")
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

// Exec executes a single command, returning any standard out/err received.
func Exec(client *ssh.Client, cmd string) (string, string, error) {
	// Init an SSH session.
	session, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrSessionInit, err)
	}
	defer session.Close()
	// Wire up standard streams.
	stdout := new(bytes.Buffer)
	session.Stdout = stdout
	stderr := new(bytes.Buffer)
	session.Stderr = stderr
	// Execute the provided command.
	if err = session.Run(cmd); err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("%w: %w", ErrCMDExec, err)
	}
	return stdout.String(), stderr.String(), nil
}

// ExecIn executes all provided commands within the provided 'shell'.
func ExecIn(client *ssh.Client, shell Shell, cmds ...string) (string, string, error) {
	cmd := "/usr/bin/env " + shell
	// Begin a new SSH session.
	session, err := client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrSessionInit, err)
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
	stdout := new(bytes.Buffer)
	session.Stdout = stdout
	stderr := new(bytes.Buffer)
	session.Stderr = stderr
	// Begin the command (we'll pass the input 'cmds' via stdin further down).
	if err = session.Start(cmd); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrCMDExec, err)
	}
	// Pass all provided commands in via stdin.
	for _, cmd := range cmds {
		// "Execute" the command.
		_, err := stdinw.Write([]byte(cmd + "\n"))
		if err != nil {
			return stdout.String(), stderr.String(), fmt.Errorf(
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
		return stdout.String(), stderr.String(), fmt.Errorf(
			"%w: %w",
			ErrStdStreamClose, err,
		)
	}
	// Wait for the command to send an 'exit-status' request.
	if err = session.Wait(); err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("%w: %w", ErrInWait, err)
	}
	return stdout.String(), stderr.String(), nil
}
