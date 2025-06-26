package ec2

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type NetworkStack struct {
	VPCName  string
	VPCID    string
	SubnetID string
}

var (
	ErrNoSubnets                   = fmt.Errorf("received no subnets")
	ErrSecurityGroupRuleAddFailure = fmt.Errorf("failed to add the default security group rules")
)

func initDefaultNetwork(ctx context.Context, d *Driver, vpcName, networkCIDR string, subnetCIDRs []string) (vpcID string, subnetIDs []string, sgID string, err error) {
	log := clog.FromContext(ctx)
	log.Debug("initializing default network stack")

	// Create the VPC
	var vpc *ec2.CreateVpcOutput
	vpc, err = createVPC(ctx, d, vpcName, networkCIDR)
	if err != nil {
		// TODO: Annotate
		return
	}
	vpcID = *vpc.Vpc.VpcId
	log.Debug("created VPC", "vpc_id", vpcID)
	// Queue the VPC for destruction
	d.stack.Push(NewGenericResource(vpcName, func(ctx context.Context) error {
		_, err := deleteVPC(ctx, d, vpcID)
		return err
	}))

	// Create the VPC subnet(s)
	if len(subnetCIDRs) == 0 {
		return "", nil, "", ErrNoSubnets
	}
	for i, subnetCIDR := range subnetCIDRs {
		snName := vpcName + "_" + strconv.Itoa(i)
		var subnet *ec2.CreateSubnetOutput
		subnet, err = createVPCSubnet(ctx, d, *vpc.Vpc.VpcId, snName, subnetCIDR)
		if err != nil {
			// TODO: Annotate
			return
		}
		subnetID := *subnet.Subnet.SubnetId
		log.Debug(
			"created VPC subnet",
			fmt.Sprintf("%s_subnet_%d_id", vpcName, i), subnetID,
		)
		subnetIDs = append(subnetIDs, subnetID)
		// Queue the subnet for destruction
		d.stack.Push(NewGenericResource(subnetID, func(ctx context.Context) error {
			_, err := deleteVPCSubnet(ctx, d, subnetID)
			return err
		}))
	}

	// Create the security group
	var sg *ec2.CreateSecurityGroupOutput
	sg, err = createSecurityGroup(ctx, d, *vpc.Vpc.VpcId, vpcName+"_public_ip_ssh")
	if err != nil {
		// TODO: Annotate
		return
	}
	sgID = *sg.GroupId
	log.Debug("created security group", "security_group_id", sgID)

	// Apply default security group rules (this can't be done in the initial
	// request - dumb right?)
	//
	// By default we'll only open TCP/22 (SSH) to the host we're calling from

	// Get the public address of this host
	//
	// TODO: This seems like something we're going to want to create some dumb
	// but self-hosted service to accomplish
	pubIP, err := publicAddr()
	if err != nil {
		return "", nil, "", err
	}
	log.Debug("identified local station public IP address", "ipv4_addr", pubIP)

	// Add the security group rule
	const portSSH = 22
	_, err = addInboundSecurityGroupRule(ctx, d, sgID, ec2.AuthorizeSecurityGroupIngressInput{
		CidrIp:            aws.String(fmt.Sprintf("%s/32", pubIP)),
		FromPort:          aws.Int32(portSSH),
		ToPort:            aws.Int32(portSSH),
		GroupId:           &sgID,
		IpProtocol:        aws.String("tcp"),
		TagSpecifications: defaultTagSpecification(types.ResourceTypeSecurityGroupRule),
	})
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: %w", ErrSecurityGroupRuleAddFailure, err)
	}

	return
}

func createVPC(ctx context.Context, d *Driver, name, cidr string) (*ec2.CreateVpcOutput, error) {
	return d.client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String(cidr),
		TagSpecifications: defaultTagSpecification(types.ResourceTypeVpc, types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		}),
	})
}

func deleteVPC(ctx context.Context, d *Driver, id string) (*ec2.DeleteVpcOutput, error) {
	return d.client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(id),
	})
}

func createVPCSubnet(ctx context.Context, d *Driver, vpcID, name, cidr string) (*ec2.CreateSubnetOutput, error) {
	return d.client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:     aws.String(vpcID),
		CidrBlock: aws.String(cidr),
		TagSpecifications: defaultTagSpecification(types.ResourceTypeSubnet, types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		}),
	})
}

func deleteVPCSubnet(ctx context.Context, d *Driver, subnetID string) (*ec2.DeleteSubnetOutput, error) {
	return d.client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
		SubnetId: aws.String(subnetID),
	})
}

func createSecurityGroup(ctx context.Context, d *Driver, vpcID, name string) (*ec2.CreateSecurityGroupOutput, error) {
	return d.client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(name),
		Description: aws.String(name),
		VpcId:       aws.String(vpcID),
		TagSpecifications: defaultTagSpecification(types.ResourceTypeSecurityGroup, types.Tag{
			Key:   aws.String("Name"),
			Value: aws.String(name),
		}),
	})
}

func addInboundSecurityGroupRule(ctx context.Context, d *Driver, groupID string, rule ec2.AuthorizeSecurityGroupIngressInput) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return d.client.AuthorizeSecurityGroupIngress(ctx, &rule)
}

func defaultTagSpecification(rt types.ResourceType, withTags ...types.Tag) []types.TagSpecification {
	return []types.TagSpecification{
		{
			ResourceType: rt,
			Tags:         append(defaultTags(), withTags...),
		},
	}
}

func defaultTags() []types.Tag {
	return []types.Tag{
		{
			Key:   aws.String("team"),
			Value: aws.String("Containers"),
		},
		{
			Key:   aws.String("project"),
			Value: aws.String("terraform-provider-imagetest/ec2-driver"),
		},
	}
}

var ErrPublicAddrLookupFailure = fmt.Errorf("failed to resolve public IP address")

func publicAddr() (string, error) {
	const provider = "https://api.ipify.org"

	res, err := http.Get(provider)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPublicAddrLookupFailure, err)
	} else if res.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("received HTTP status code %d", res.StatusCode)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return string(data), nil
}
