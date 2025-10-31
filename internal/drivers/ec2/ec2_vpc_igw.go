package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	ErrInternetGatewayCreate = fmt.Errorf("failed to create internet gateway")
	ErrNilInternetGatewayID  = fmt.Errorf("received no error in internet gateway create, but the internet gateway ID returned was nil")
)

func internetGatewayCreate(ctx context.Context, client *ec2.Client, tags ...types.Tag) (string, error) {
	igwResult, err := client.CreateInternetGateway(ctx, &ec2.CreateInternetGatewayInput{
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeInternetGateway,
			tags...,
		),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInternetGatewayCreate, err)
	}
	if igwResult.InternetGateway == nil || igwResult.InternetGateway.InternetGatewayId == nil {
		return "", ErrNilInternetGatewayID
	}
	return *igwResult.InternetGateway.InternetGatewayId, nil
}

var ErrInternetGatewayAttach = fmt.Errorf("failed to attach internet gateway to VPC")

func internetGatewayAttach(ctx context.Context, client *ec2.Client, vpcID, igwID string) error {
	_, err := client.AttachInternetGateway(ctx, &ec2.AttachInternetGatewayInput{
		VpcId:             &vpcID,
		InternetGatewayId: &igwID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInternetGatewayAttach, err)
	}
	return nil
}

var ErrInternetGatewayDetach = fmt.Errorf("failed to detach internet gateway")

func internetGatewayDetach(ctx context.Context, client *ec2.Client, vpcID, igwID string) error {
	_, err := client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
		InternetGatewayId: &igwID,
		VpcId:             &vpcID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInternetGatewayDetach, err)
	}
	return nil
}

var ErrInternetGatewayDelete = fmt.Errorf("failed to delete internet gateway")

func internetGatewayDelete(ctx context.Context, client *ec2.Client, igwID string) error {
	_, err := client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
		InternetGatewayId: &igwID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInternetGatewayDelete, err)
	}
	return err
}
