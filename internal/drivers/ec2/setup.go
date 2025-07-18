package ec2

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

var ErrNoInstanceTypes = fmt.Errorf(
	"found no instance types which satisfy all input requirements",
)

func (self *Driver) Setup(ctx context.Context) error {
	const defaultVPCName = "imagetest-demo"
	const defaultSubnet = "172.25.0.0/24"

	log := clog.FromContext(ctx).With("driver", "ec2")

	// Bootstrap the VPC
	//
	// TODO: In the future this can be extended to allow VPC+Subnet+Security
	// Group configurability from the caller (as inputs)
	_, subnetIDs, sgID, err := initDefaultNetwork(
		ctx,
		self,
		defaultVPCName,
		defaultSubnet,
		[]string{defaultSubnet},
	)
	if err != nil {
		return fmt.Errorf(
			"failed to bootstrap VPC, subnets and security group: %w",
			err,
		)
	}

	// Select the most cost-effective instance type
	instanceType, err := selectInstanceType(ctx, self)
	if err != nil {
		return fmt.Errorf("failed instance selection: %w", err)
	}
	log = log.With("instance_type", instanceType)
	log.Info("selected instance type")

	// Query and select AMI
	ami, err := selectAMI(ctx, self)
	if err != nil {
		return fmt.Errorf("failed AMI selection: %w", err)
	}

	// Launch the instance
	launchResult, err := self.client.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      ami,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		InstanceType: instanceType,
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int32(0),
				SubnetId:                 aws.String(subnetIDs[0]),
				AssociatePublicIpAddress: aws.Bool(true),
				Groups:                   []string{sgID},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to launch instance [%s]: %s", instanceType, err)
	} else if len(launchResult.Instances) != 1 {
		return fmt.Errorf("received no error during instance launch, but no instance was actually created")
	}

	// Capture necessary instance metadata
	self.instanceID = launchResult.Instances[0].InstanceId
	self.instanceAddr = launchResult.Instances[0].PublicIpAddress

	return nil
}

var ErrNoAMI = fmt.Errorf("an AMI is not configured")

func selectAMI(ctx context.Context, d *Driver) (*string, error) {
	if d.AMI != "" {
		return aws.String(d.AMI), nil
	} else if ami, ok := os.LookupEnv("IMAGE_TEST_AMI"); ok {
		return aws.String(ami), nil
	} else {
		return nil, ErrNoAMI
	}
}

func selectInstanceType(ctx context.Context, d *Driver) (types.InstanceType, error) {
	var err error
	log := clog.FromContext(ctx)

	// Init the `DescribeInstanceTypesInput`
	//
	// `DescribeInstanceTypesInput` is the "request body" used to ask the AWS Go
	// SDK v2 for a list of EC2 instance types
	describeInstanceTypesInput := new(ec2.DescribeInstanceTypesInput)

	// Assemble pre filters
	//
	// A number of things (GPU kind, disk capacity) cannot be filtered in-request
	// so we try to pack as much into the request as we can, then filter the rest
	// further down.
	//
	// NOTE: yes, there is a listed filter for storage capacity in the docs - it
	// does not work. \o/ Also there just are no filters for GPUs.
	describeInstanceTypesInput.Filters, err = buildPreFilters(ctx, d)
	if err != nil {
		return "", fmt.Errorf("failed to build instance filters: %w", err)
	}
	log.Debug(
		"filter_assembly_complete",
		"filter_count", len(describeInstanceTypesInput.Filters),
	)

	// Roll all paginated EC2 `InstanceTypeInfo` results up
	var instanceTypeInfos []types.InstanceTypeInfo
	var nextToken *string
	for {
		// If we caught a next-page token on the previous iteration, apply it
		if nextToken != nil {
			describeInstanceTypesInput.NextToken = nextToken
		}

		// Fetch the next page of results
		results, err := d.client.DescribeInstanceTypes(ctx, describeInstanceTypesInput)
		if err != nil {
			return "", err
		} else if len(results.InstanceTypes) == 0 {
			return "", ErrNoInstanceTypes
		} else if len(instanceTypeInfos) == 0 {
			instanceTypeInfos = results.InstanceTypes
		} else {
			instanceTypeInfos = append(instanceTypeInfos, results.InstanceTypes...)
		}

		// If we don't have a `NextToken` (next page of results hint), we're done
		if results.NextToken == nil {
			break
		}

		// Set the next-page token for the next iteration
		nextToken = results.NextToken
	}

	// Apply post-request filters
	instanceTypeInfos = applyPostFilters(ctx, d, instanceTypeInfos)

	// Of the instance types which fulfill our requirements, select the cheapest
	instanceTypes := make([]types.InstanceType, len(instanceTypeInfos))
	for i := range len(instanceTypeInfos) {
		instanceTypes[i] = instanceTypeInfos[i].InstanceType
	}

	// TODO: Select cheapest

	if len(instanceTypes) == 0 {
		return "", ErrNoInstanceTypes
	}

	return instanceTypes[0], nil
}
