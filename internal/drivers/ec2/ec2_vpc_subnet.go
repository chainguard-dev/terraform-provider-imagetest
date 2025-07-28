package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	ErrSubnetCreate = fmt.Errorf("failed to create subnet")
	ErrNilSubnetID  = fmt.Errorf("received no error in subnet create, but the subnet ID returned was nil")
)

func subnetCreate(ctx context.Context, client *ec2.Client, vpcID, subnetCIDR string, tags ...types.Tag) (string, error) {
	result, err := client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     &vpcID,
		CidrBlock: &subnetCIDR,
		// TODO: Availability zone support would be nice.
		// AvailabilityZone: *string,
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeSubnet,
			tags...,
		),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSubnetCreate, err)
	}
	if result.Subnet == nil || result.Subnet.SubnetId == nil {
		return "", fmt.Errorf("%w: %w", ErrSubnetCreate, ErrNilSubnetID)
	}
	return *result.Subnet.SubnetId, nil
}

var ErrSubnetDelete = fmt.Errorf("failed to delete subnet")

func subnetDelete(ctx context.Context, client *ec2.Client, subnetID string) error {
	_, err := client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
		SubnetId: aws.String(subnetID),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSubnetDelete, err)
	}
	return nil
}
