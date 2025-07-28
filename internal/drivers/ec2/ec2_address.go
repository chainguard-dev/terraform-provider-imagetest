package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	ErrElasticIPCreate = fmt.Errorf("failed to create public IP address")
	ErrElasticIPIDNil  = fmt.Errorf("encountered no error in elastic IP " +
		"address creation, but the returned allocation ID was nil")
	ErrElasticIPNil = fmt.Errorf("encountered no error in elastic IP " +
		"address creation, but the returned public IP was nil")
)

// elasticIPCreate creates an Elastic IP address, which is a public IPv4 address
// that can be allocated to a number of resource types.
//
// The returned strings are the allocation ID and the public IP address. The
// allocation ID is the "handle" to the elastic IP for use in assignment of that
// IP address to a resource.
func elasticIPCreate(
	ctx context.Context,
	client *ec2.Client,
	tags ...types.Tag,
) (string, string, error) {
	result, err := client.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		TagSpecifications: tagSpecificationWithDefaults(types.ResourceTypeElasticIp, tags...),
	})
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrElasticIPCreate, err)
	}
	if result.AllocationId == nil {
		return "", "", ErrElasticIPIDNil
	}
	if result.PublicIp == nil {
		return "", "", ErrElasticIPNil
	}
	return *result.AllocationId, *result.PublicIp, nil
}

var (
	ErrElasticIPAttach = fmt.Errorf("failed to attach the provided " +
		"elastic IP address to the specified elastic network interface")
	ErrElasticIPAttachIDNil = fmt.Errorf("encountered no error in elastic IP " +
		"address association to elastic network interface, but the returned " +
		"association ID was nil")
)

func elasticIPAttach(ctx context.Context, client *ec2.Client, eipID, interfaceID string) (string, error) {
	result, err := client.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId:       &eipID,
		AllowReassociation: aws.Bool(false),
		NetworkInterfaceId: &interfaceID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrElasticIPAttach, err)
	}
	return *result.AssociationId, nil
}

var ErrElasticIPDetach = fmt.Errorf("failed to detach elastic IP address" +
	" from the provided instance")

func elasticIPDetach(ctx context.Context, client *ec2.Client, attachID string) error {
	_, err := client.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
		AssociationId: &attachID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrElasticIPDetach, err)
	}
	return nil
}

var ErrElasticIPDelete = fmt.Errorf("failed to delete elastic IP address")

func elasticIPDelete(ctx context.Context, client *ec2.Client, eipID string) error {
	_, err := client.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
		AllocationId: &eipID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrElasticIPDelete, err)
	}
	return nil
}
