package gce

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

type sshKey struct {
	name    string
	sshUser string

	path    string
	public  ssh.ED25519PublicKey
	private ssh.ED25519PrivateKey
}

var _ resource = (*sshKey)(nil)

func (k *sshKey) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	keys, err := ssh.NewED25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating key pair: %w", err)
	}
	k.public = keys.Public
	k.private = keys.Private

	keyFile, err := os.CreateTemp("", k.name+"-*.pem")
	if err != nil {
		return nil, fmt.Errorf("creating temp key file: %w", err)
	}
	k.path = keyFile.Name()

	pemData, err := keys.Private.MarshalOpenSSH(k.name)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}

	if _, err := keyFile.Write(pemData); err != nil {
		_ = keyFile.Close()
		return nil, fmt.Errorf("writing private key: %w", err)
	}
	if err := keyFile.Chmod(0o600); err != nil {
		_ = keyFile.Close()
		return nil, fmt.Errorf("setting key file permissions: %w", err)
	}
	_ = keyFile.Close()
	log.Info("saved private key", "path", k.path)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("removing private key file", "path", k.path)
		if err := os.Remove(k.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing private key file: %w", err)
		}
		return nil
	}

	return teardown, nil
}

// metadataEntry returns the SSH public key formatted for GCE instance metadata.
// Format: "{ssh_user}:ssh-ed25519 AAAA... imagetest"
func (k *sshKey) metadataEntry() (string, error) {
	pubBytes, err := k.public.MarshalOpenSSH()
	if err != nil {
		return "", fmt.Errorf("marshaling public key: %w", err)
	}

	// GCE metadata format: "user:ssh-ed25519 AAAA... comment"
	// MarshalOpenSSH output is "ssh-ed25519 AAAA...\n" — trim the trailing newline
	// so the comment lands on the same line.
	pubStr := strings.TrimRight(string(pubBytes), "\n")
	return fmt.Sprintf("%s:%s imagetest", k.sshUser, pubStr), nil
}
