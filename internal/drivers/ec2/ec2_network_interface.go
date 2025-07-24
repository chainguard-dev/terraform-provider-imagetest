package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

var (
	ErrNetIFCreate      = fmt.Errorf("failed to create network interface")
	ErrNetIFCreateIDNil = fmt.Errorf("encountered no error in network interface" +
		" create, but the returned interface ID was nil")
)

func netIFCreate(
	ctx context.Context,
	client *ec2.Client,
	subnetID string,
) (string, error) {
	result, err := client.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId: &subnetID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNetIFCreate, err)
	}
	if result.NetworkInterface == nil || result.NetworkInterface.NetworkInterfaceId == nil {
		return "", ErrNetIFCreateIDNil
	}
	return *result.NetworkInterface.NetworkInterfaceId, nil
}

var ErrNetIFDelete = fmt.Errorf("failed to delete elastic network interface")

func netIFDelete(ctx context.Context, client *ec2.Client, netIFID string) error {
	_, err := client.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: &netIFID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNetIFDelete, err)
	}
	return nil
}
