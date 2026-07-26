package ec2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/chainguard-dev/clog"
)

type instance struct {
	client          *ec2.Client
	ami             string
	instanceType    types.InstanceType
	rootVolumeSize  int32
	subnetID        string
	securityGroupID string
	keyName         string
	profileName     string
	userData        string
	sshPort         int32
	tags            []types.Tag

	id       string
	publicIP string
}

var _ resource = (*instance)(nil)

func (i *instance) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(i.ami),
		InstanceType: i.instanceType,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		KeyName:      aws.String(i.keyName),
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex:              aws.Int32(0),
			SubnetId:                 aws.String(i.subnetID),
			AssociatePublicIpAddress: aws.Bool(true),
			Groups:                   []string{i.securityGroupID},
		}},
		BlockDeviceMappings: []types.BlockDeviceMapping{{
			DeviceName: aws.String("/dev/sda1"),
			Ebs: &types.EbsBlockDevice{
				VolumeSize:          aws.Int32(i.rootVolumeSize),
				VolumeType:          types.VolumeTypeGp3,
				DeleteOnTermination: aws.Bool(true),
			},
		}},
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeInstance,
			Tags:         i.tags,
		}},
	}

	if i.profileName != "" {
		input.IamInstanceProfile = &types.IamInstanceProfileSpecification{
			Name: aws.String(i.profileName),
		}
	}

	if i.userData != "" {
		input.UserData = aws.String(i.userData)
	}

	// Retry RunInstances to handle IAM eventual consistency.
	// The instance profile may not be immediately available after creation.
	var result *ec2.RunInstancesOutput
	backoff := 2 * time.Second
	for attempt := 1; attempt <= 10; attempt++ {
		var err error
		result, err = i.client.RunInstances(ctx, input)
		if err == nil {
			break
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			code := apiErr.ErrorCode()
			msg := strings.ToLower(apiErr.ErrorMessage())
			isProfileError := strings.Contains(msg, "instance profile") || strings.Contains(msg, "iaminstanceprofile")
			if code == "InvalidParameterValue" && isProfileError {
				log.Debug("instance profile not ready, retrying", "attempt", attempt, "backoff", backoff)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoff):
					backoff = min(backoff*2, 30*time.Second)
					continue
				}
			}
		}
		return nil, fmt.Errorf("launching instance: %w", err)
	}

	if result == nil || len(result.Instances) == 0 || result.Instances[0].InstanceId == nil {
		return nil, fmt.Errorf("no instance returned from launch")
	}

	i.id = *result.Instances[0].InstanceId
	log.Info("launched instance", "id", i.id)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("terminating instance", "id", i.id, "ip", i.publicIP)

		_, err := i.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds:    []string{i.id},
			Force:          aws.Bool(true),
			SkipOsShutdown: aws.Bool(true),
		})
		if err != nil {
			return fmt.Errorf("terminating instance: %w", err)
		}

		// Wait for instance to be fully terminated so ENI is released
		// This allows security group and subnet deletion to succeed
		// Use a large timeout - the parent context controls actual duration
		log.Info("waiting for instance to terminate", "id", i.id)
		waiter := ec2.NewInstanceTerminatedWaiter(i.client)
		if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{i.id},
		}, time.Hour); err != nil {
			log.Warn("error waiting for instance termination, continuing", "id", i.id, "error", err)
		} else {
			log.Info("instance terminated", "id", i.id)
		}
		return nil
	}

	return teardown, nil
}

func (i *instance) wait(ctx context.Context) error {
	log := clog.FromContext(ctx)

	log.Info("waiting for instance to enter running state", "id", i.id)
	runningWaiter := ec2.NewInstanceRunningWaiter(i.client)
	runningOutput, err := runningWaiter.WaitForOutput(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{i.id},
	}, time.Hour)
	if err != nil {
		return fmt.Errorf("waiting for running state: %w", err)
	}

	if len(runningOutput.Reservations) == 0 || len(runningOutput.Reservations[0].Instances) == 0 {
		return fmt.Errorf("instance not found in waiter output")
	}
	inst := runningOutput.Reservations[0].Instances[0]
	if inst.PublicIpAddress == nil {
		return fmt.Errorf("instance has no public IP")
	}
	i.publicIP = *inst.PublicIpAddress

	log.Info("waiting for instance status checks", "id", i.id)
	statusWaiter := ec2.NewInstanceStatusOkWaiter(i.client)
	if err := statusWaiter.Wait(ctx, &ec2.DescribeInstanceStatusInput{
		InstanceIds: []string{i.id},
	}, time.Hour); err != nil {
		return fmt.Errorf("waiting for status checks: %w", err)
	}

	log.Info("instance ready", "id", i.id, "ip", i.publicIP)

	log.Info("waiting for SSH to become available", "ip", i.publicIP)
	if err := waitTCP(ctx, i.publicIP, uint16(i.sshPort)); err != nil {
		return fmt.Errorf("waiting for SSH: %w", err)
	}

	return nil
}

func waitTCP(ctx context.Context, host string, port uint16) error {
	log := clog.FromContext(ctx)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := &net.Dialer{Timeout: 3 * time.Second}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			conn, err := dialer.DialContext(ctx, "tcp", target)
			if err != nil {
				log.Debug("TCP port not ready", "target", target)
				continue
			}
			_ = conn.Close()
			log.Debug("TCP port ready", "target", target)
			return nil
		}
	}
}
