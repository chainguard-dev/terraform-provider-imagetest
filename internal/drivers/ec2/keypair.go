package ec2

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

type keyPair struct {
	client *ec2.Client
	name   string
	tags   []types.Tag

	path    string
	private ssh.ED25519PrivateKey
}

var _ resource = (*keyPair)(nil)

func (k *keyPair) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	keys, err := ssh.NewED25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating key pair: %w", err)
	}
	k.private = keys.Private

	pubKey, err := keys.Public.MarshalOpenSSH()
	if err != nil {
		return nil, fmt.Errorf("marshaling public key: %w", err)
	}

	pemData, err := keys.Private.MarshalOpenSSH(k.name)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}

	// The private key file is written before the key pair is imported, so a
	// failure here leaves no key pair behind in AWS. From here on every path
	// out of create, and teardown, must remove it.
	path, err := writeKeyFile(k.name, pemData)
	if err != nil {
		return nil, err
	}
	k.path = path
	log.Info("saved private key", "path", k.path)

	removeKeyFile := func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing private key file: %w", err)
		}
		return nil
	}

	result, err := k.client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(k.name),
		PublicKeyMaterial: pubKey,
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeKeyPair,
			Tags:         k.tags,
		}},
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("importing key pair: %w", err), removeKeyFile())
	}

	log.Info("imported key pair", "id", *result.KeyPairId, "name", k.name)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("deleting key pair", "name", k.name)
		var derr error
		if _, err := k.client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
			KeyName: aws.String(k.name),
		}); err != nil {
			derr = fmt.Errorf("deleting key pair from AWS: %w", err)
		}

		// The local copy of the private key goes regardless: a failed AWS
		// delete is reported, but it is no reason to leave key material on
		// disk.
		log.Info("removing private key file", "path", k.path)
		return errors.Join(derr, removeKeyFile())
	}

	return teardown, nil
}

// writeKeyFile writes the private key to a new temp file and returns its
// path. os.CreateTemp creates the file with mode 0600. On error the file is
// removed.
func writeKeyFile(name string, pemData []byte) (string, error) {
	keyFile, err := os.CreateTemp("", name+"-*.pem")
	if err != nil {
		return "", fmt.Errorf("creating temp key file: %w", err)
	}

	if _, err := keyFile.Write(pemData); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("writing private key: %w", err)
	}
	if err := keyFile.Chmod(0o600); err != nil {
		_ = keyFile.Close()
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("setting key file permissions: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		_ = os.Remove(keyFile.Name())
		return "", fmt.Errorf("writing private key: %w", err)
	}
	return keyFile.Name(), nil
}
