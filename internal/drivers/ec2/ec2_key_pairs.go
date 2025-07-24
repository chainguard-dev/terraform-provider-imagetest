package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	ErrKeypairImport = fmt.Errorf("failed to import keypair")
	ErrNilKeyPairID  = fmt.Errorf("encountered no error in keypair import, but the returned keypair ID was nil")
)

func keypairImport(
	ctx context.Context,
	client *ec2.Client,
	keyPairName string, pubKey []byte,
) (kpID string, err error) {
	result, err := client.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           &keyPairName,
		PublicKeyMaterial: pubKey,
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeKeyPair,
		),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrKeypairImport, err)
	}
	if result.KeyPairId == nil {
		return "", ErrNilKeyPairID
	}
	return *result.KeyPairId, nil
}

var ErrKeypairDelete = fmt.Errorf("failed to delete keypair")

func keypairDelete(
	ctx context.Context,
	client *ec2.Client,
	keyPairID string,
) error {
	_, err := client.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyPairId: &keyPairID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrKeypairDelete, err)
	}
	return nil
}
