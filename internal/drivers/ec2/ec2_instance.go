package ec2

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
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
	tags ...types.Tag,
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
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeInstance,
			tags...,
		),
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
		InstanceIds:    []string{instanceID},
		SkipOsShutdown: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInstanceDelete, err)
	}
	return nil
}

var (
	ErrInstanceState               = fmt.Errorf("failed to fetch instance state")
	ErrInstanceStateNoReservations = fmt.Errorf("describe instances call " +
		"produced no errors, but returned no reservations")
	ErrInstanceStateNoInstances = fmt.Errorf("describe instances call produced " +
		"no errors, but returned no instances")
	ErrInstanceStateStateNil = fmt.Errorf("describe instances call produced no " +
		"errors, but the returned instance state was nil")
)

func instanceState(ctx context.Context, client *ec2.Client, instanceID string) (types.InstanceStateName, error) {
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
		return "", ErrInstanceStateStateNil
	}
	return instance.State.Name, nil
}

func awaitInstanceState(
	ctx context.Context,
	client *ec2.Client,
	instanceID string,
	desiredState types.InstanceStateName,
) error {
	log := clog.FromContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("deadlined waiting for EC2 instance termination")
		case <-time.After(5 * time.Second):
			currentState, err := instanceState(ctx, client, instanceID)
			if err != nil {
				return err
			}
			if currentState == desiredState {
				log.Info("instance termination complete")
				return nil
			} else {
				log.Debug("instance still terminating, waiting longer", "state", currentState)
			}
		}
	}
}
