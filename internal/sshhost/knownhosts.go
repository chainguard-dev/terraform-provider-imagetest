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
// keys it records for host. Matching follows OpenSSH, which also reads the
// file for the Docker tunnel: the bare host, or [host]:port when port is not
// 22. Hashed entries are not matched. An entry marked @revoked is ignored and
// one marked @cert-authority is an error, since OpenSSH would not accept a
// host key against it. It is an error for the file to hold no key for the
// host, so a file for the wrong host fails before any connection is
// attempted.
func LoadKnownHosts(path, host string, port int32) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseKnownHosts(data, host, port)
}

func parseKnownHosts(data []byte, host string, port int32) ([]ssh.PublicKey, error) {
	want := []string{host}
	if port != 22 {
		want = append(want, fmt.Sprintf("[%s]:%d", host, port))
	}

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
		matches := slices.ContainsFunc(hosts, func(h string) bool { return slices.Contains(want, h) })
		switch {
		case marker == "revoked":
			continue
		case marker == "cert-authority" && matches:
			return nil, fmt.Errorf("known_hosts entry for %s is a certificate authority; pin the host key itself", host)
		case matches:
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("known_hosts has no entry for %s", host)
	}
	return keys, nil
}
