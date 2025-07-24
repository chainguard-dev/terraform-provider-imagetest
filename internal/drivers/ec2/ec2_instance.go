package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var (
	ErrInstanceCreate            = fmt.Errorf("failed to create EC2 instance")
	ErrInstanceCreateNoInstances = fmt.Errorf("encountered no error during " +
		"instance launch, but no instance was actually created")
	ErrInstanceCreateIDNil = fmt.Errorf("encountered no error during instance " +
		"launch, but the returned instance ID was nil")
)

func instanceCreateWithNetIF(
	ctx context.Context,
	client *ec2.Client,
	instanceType types.InstanceType,
	ami, keyPairName, netIFID string,
) (string, error) {
	launchResult, err := client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      &ami,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		InstanceType: instanceType,
		KeyName:      &keyPairName,
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{
			{
				NetworkInterfaceId: &netIFID,
				DeviceIndex:        aws.Int32(0),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInstanceCreate, err)
	}
	if len(launchResult.Instances) < 1 {
		return "", ErrInstanceCreateNoInstances
	}
	instance := &launchResult.Instances[0]
	if instance.InstanceId == nil {
		return "", ErrInstanceCreateIDNil
	}
	return *instance.InstanceId, nil
}

var ErrInstanceDelete = fmt.Errorf("failed to delete EC2 instance")

func instanceDelete(ctx context.Context, client *ec2.Client, instanceID string) error {
	_, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInstanceDelete, err)
	}
	return nil
}

var (
	ErrInstanceState               = fmt.Errorf("failed to fetch instance state")
	ErrInstanceStateNoReservations = fmt.Errorf("TODO")
	ErrInstanceStateNoInstances    = fmt.Errorf("TODO")
	ErrInstanceStateNoState        = fmt.Errorf("TODO")
)

func instanceState(
	ctx context.Context,
	client *ec2.Client,
	instanceID string,
) (types.InstanceStateName, error) {
	result, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInstanceState, err)
	}
	if len(result.Reservations) == 0 {
		return "", ErrInstanceStateNoReservations
	}
	reservation := result.Reservations[0]
	if len(reservation.Instances) == 0 {
		return "", ErrInstanceStateNoInstances
	}
	instance := reservation.Instances[0]
	if instance.State == nil {
		return "", ErrInstanceStateNoState
	}
	return instance.State.Name, nil
}
