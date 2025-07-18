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
	VPCName         string
	VPCID           string
	SubnetIDs       []string
	SecurityGroupID string
}

var (
	ErrNoVPCSubnets         = fmt.Errorf("received no subnets")
	ErrSecurityGroupRuleAdd = fmt.Errorf("failed to add the default security group rules")
	ErrVPCCreate            = fmt.Errorf("failed VPC creation")
	ErrVPCNil               = fmt.Errorf("VPC creation produced no error, but the VPC is nil")
	ErrVPCSubnetCreate      = fmt.Errorf("failed VPC subnet creation")
	ErrVPCSubnetNil         = fmt.Errorf("VPC subnet creation produced no error, but the VPC subnet is nil")
	ErrSecurityGroupCreate  = fmt.Errorf("failed security group creation")
	ErrSecurityGroupNil     = fmt.Errorf("VPC subnet creation produced no error, but the VPC subnet is nil")
)

func initDefaultNetwork(ctx context.Context, d *Driver, vpcName, networkCIDR string, subnetCIDRs []string) (NetworkStack, error) {
	log := clog.FromContext(ctx)
	log.Debug("constructing default network stack")
	// Init the NetworkStack.
	network := NetworkStack{
		VPCName: vpcName,
	}
	// Create the VPC.
	vpc, err := createVPC(ctx, d, vpcName, networkCIDR)
	if err != nil {
		return network, fmt.Errorf("%w: %w", ErrVPCCreate, err)
	} else if vpc.Vpc == nil || vpc.Vpc.VpcId == nil {
		return network, fmt.Errorf("%w: %w", ErrVPCNil, err)
	}
	network.VPCID = *vpc.Vpc.VpcId
	log.Debug("created VPC", "name", network.VPCName, "id", network.VPCID)
	// Add a destructor for the VPC.
	d.stack.Push(func(ctx context.Context) error {
		_, err := deleteVPC(ctx, d, network.VPCID)
		return err
	})
	// Create the VPC subnet(s).
	if len(subnetCIDRs) == 0 {
		return network, ErrNoVPCSubnets
	}
	for i, subnetCIDR := range subnetCIDRs {
		// Assemble the name of the subnet using the VPC name and current index into
		// 'subnetCIDRs'.
		subnetName := vpcName + "_" + strconv.Itoa(i)
		// Create the subnet.
		subnet, err := createVPCSubnet(ctx, d, *vpc.Vpc.VpcId, subnetName, subnetCIDR)
		if err != nil {
			return network, fmt.Errorf("%w: %w", ErrVPCSubnetCreate, err)
		} else if subnet.Subnet == nil || subnet.Subnet.SubnetId == nil {
			return network, fmt.Errorf("%w: %w", ErrVPCSubnetNil, err)
		}
		// Collect, store the subnet ID.
		subnetID := *subnet.Subnet.SubnetId
		network.SubnetIDs = append(network.SubnetIDs, subnetID)
		log.Debug("created VPC subnet", "name", subnetName, "id", subnetID)
		// Add a destructor for the VPC subnet.
		d.stack.Push(func(ctx context.Context) error {
			_, err := deleteVPCSubnet(ctx, d, subnetID)
			return err
		})
	}
	// Create the security group.
	sg, err := createSecurityGroup(ctx, d, *vpc.Vpc.VpcId, vpcName+"_public_ip_ssh")
	if err != nil {
		return network, fmt.Errorf("%w: %w", ErrSecurityGroupCreate, err)
	} else if sg.GroupId == nil {
		return network, fmt.Errorf("%w: %w", ErrSecurityGroupNil, err)
	}
	network.SecurityGroupID = *sg.GroupId
	log.Debug("created security group", "id", network.SecurityGroupID)
	// Add a destructor for the VPC subnet.
	d.stack.Push(func(ctx context.Context) error {
		_, err := deleteSecurityGroup(ctx, d, network.SecurityGroupID)
		return err
	})
	// Apply default security group rules. By default we'll only open TCP/22 (SSH)
	// to the host we're calling from. Unfortunately, this can't be done in the
	// initial request.
	//
	// Get the public address of this host.
	//
	// TODO: This seems like something we're going to want to create a dumb but
	// self-hosted service to handle.
	pubIP, err := publicAddr()
	if err != nil {
		return network, fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
	}
	log.Debug("identified local station public IP address", "ipv4_addr", pubIP)
	// Add the security group rule.
	const portSSH = 22
	_, err = addInboundSecurityGroupRule(ctx, d, ec2.AuthorizeSecurityGroupIngressInput{
		CidrIp:            aws.String(fmt.Sprintf("%s/32", pubIP)),
		FromPort:          aws.Int32(portSSH),
		ToPort:            aws.Int32(portSSH),
		GroupId:           &network.SecurityGroupID,
		IpProtocol:        aws.String("tcp"),
		TagSpecifications: defaultTagSpecification(types.ResourceTypeSecurityGroupRule),
	})
	if err != nil {
		return network, fmt.Errorf("%w: %w", ErrSecurityGroupRuleAdd, err)
	}
	return network, nil
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

func deleteSecurityGroup(ctx context.Context, d *Driver, securityGroupID string) (*ec2.DeleteSecurityGroupOutput, error) {
	return d.client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: &securityGroupID,
	})
}

func addInboundSecurityGroupRule(ctx context.Context, d *Driver, rule ec2.AuthorizeSecurityGroupIngressInput) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
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

var ErrPublicIPLookup = fmt.Errorf("failed to resolve public IP address")

func publicAddr() (string, error) {
	// TODO: We probably want a Chainguard echo service for this?
	const provider = "https://api.ipify.org"

	res, err := http.Get(provider)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
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
