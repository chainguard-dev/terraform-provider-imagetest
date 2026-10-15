package sshhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"golang.org/x/crypto/ssh"
)

// LoadKnownHosts reads an OpenSSH known_hosts file and returns the public
// keys it records for host, matched as the bare host or as [host]:port.
// Hashed entries are not matched. An entry marked @revoked is ignored. It is
// an error for the file to hold no key for the host, so a file for the wrong
// host fails before any connection is attempted.
func LoadKnownHosts(path, host string, port int32) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseKnownHosts(data, host, port)
}

func parseKnownHosts(data []byte, host string, port int32) ([]ssh.PublicKey, error) {
	want := []string{host, fmt.Sprintf("[%s]:%d", host, port)}

	var keys []ssh.PublicKey
	rest := data
	for {
		marker, hosts, key, _, next, err := ssh.ParseKnownHosts(rest)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing known_hosts: %w", err)
		}
		rest = next
		if marker == "revoked" {
			continue
		}
		if slices.ContainsFunc(hosts, func(h string) bool { return slices.Contains(want, h) }) {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("known_hosts has no entry for %s", host)
	}
	return keys, nil
}
