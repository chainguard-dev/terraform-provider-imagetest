package ec2

import (
	"context"
	"errors"
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
	instanceProfileName, ami, keyPairName, netIFID, userData string,
	tags ...types.Tag,
) (string, error) {
	runInstancesInput := &ec2.RunInstancesInput{
		ImageId:      &ami,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		InstanceType: instanceType,
		KeyName:      &keyPairName,
		UserData:     aws.String(userData),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{
			{
				NetworkInterfaceId: &netIFID,
				DeviceIndex:        aws.Int32(0),
			},
		},
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				// By default, you get whatever the default instance root volume size
				// is. For a lot of these, it's ~5-8GB which in testing I exhausted
				// just by installing some elaborate nVIDIA driver stacks.
				//
				// Here we set the root volume (/dev/sda1) to a capacity of 50GB and
				// signal that it should be deleted when the instance is terminated.
				DeviceName: aws.String("/dev/sda1"),
				Ebs: &types.EbsBlockDevice{
					VolumeSize:          aws.Int32(50),
					VolumeType:          types.VolumeTypeGp3,
					DeleteOnTermination: aws.Bool(true),
					Encrypted:           aws.Bool(false),
				},
			},
		},
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeInstance,
			tags...,
		),
	}

	// Only set IAM instance profile if one was provided
	if instanceProfileName != "" {
		runInstancesInput.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Name: &instanceProfileName,
		}
	}

	launchResult, err := client.RunInstances(ctx, runInstancesInput)
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
			return fmt.Errorf(
				"deadlined while awaiting instance state transition(wanted %s)",
				desiredState,
			)
		case <-time.After(1 * time.Second):
			currentState, err := instanceState(ctx, client, instanceID)
			if err != nil {
				return err
			}
			if currentState == desiredState {
				log.Info("instance state transition complete", "new_state", currentState)
				return nil
			} else {
				log.Debug("still waiting for instance state transition", "state", currentState)
			}
		}
	}
}

var (
	ErrInstanceStatus    = fmt.Errorf("failed to fetch instance status")
	ErrInstanceStatusNil = fmt.Errorf("describe instance status call produced " +
		"no errors, but returned no statuses")
)

func instanceStatus(ctx context.Context, client *ec2.Client, instanceID string) (types.SummaryStatus, error) {
	result, err := client.DescribeInstanceStatus(ctx, &ec2.DescribeInstanceStatusInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInstanceStatus, err)
	}

	if len(result.InstanceStatuses) == 0 ||
		result.InstanceStatuses[0].InstanceStatus == nil {
		return "", ErrInstanceStatusNil
	}

	return result.InstanceStatuses[0].InstanceStatus.Status, nil
}

func awaitInstanceStatus(ctx context.Context, client *ec2.Client, instanceID string, status types.SummaryStatus) error {
	for {
		select {
		case <-ctx.Done():
			return context.DeadlineExceeded
		case <-time.After(1 * time.Second):
			currentStatus, err := instanceStatus(ctx, client, instanceID)
			if err != nil {
				if errors.Is(err, ErrInstanceStatusNil) {
					continue
				}
				return err
			}

			if currentStatus == status {
				return nil
			}
		}
	}
}
