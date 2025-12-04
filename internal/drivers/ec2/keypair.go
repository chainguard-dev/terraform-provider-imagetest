package ec2

import (
	"context"
	"fmt"
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

	result, err := k.client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(k.name),
		PublicKeyMaterial: pubKey,
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeKeyPair,
			Tags:         k.tags,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("importing key pair: %w", err)
	}

	log.Info("imported key pair", "id", *result.KeyPairId, "name", k.name)

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
		log.Info("deleting key pair", "name", k.name)
		_, err := k.client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
			KeyName: aws.String(k.name),
		})
		if err != nil {
			return fmt.Errorf("deleting key pair from AWS: %w", err)
		}

		log.Info("removing private key file", "path", k.path)
		if err := os.Remove(k.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing private key file: %w", err)
		}
		return nil
	}

	return teardown, nil
}
