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
)

type NetworkStack struct {
	VPCName  string
	VPCID    string
	SubnetID string
}

func initDefaultNetwork(ctx context.Context, d *Driver, vpcName, networkCIDR string, subnetCIDRs ...string) (vpcID string, subnetIDs []string, sgID string, err error) {
	// Create the VPC
	var vpc *ec2.CreateVpcOutput
	vpc, err = createVPC(ctx, d, vpcName, networkCIDR)
	if err != nil {
		// TODO: Annotate
		return
	}
	vpcID = *vpc.Vpc.VpcId

	// Create the VPC subnet(s)
	for i, subnetCIDR := range subnetCIDRs {
		snName := vpcName + "_" + strconv.Itoa(i)
		var subnet *ec2.CreateSubnetOutput
		subnet, err = createVPCSubnet(ctx, d, *vpc.Vpc.VpcId, snName, subnetCIDR)
		if err != nil {
			// TODO: Annotate
			return
		}
		subnetIDs = append(subnetIDs, *subnet.Subnet.SubnetId)
	}

	// Create the security group
	var sg *ec2.CreateSecurityGroupOutput
	sg, err = createSecurityGroup(ctx, d, *vpc.Vpc.VpcId, vpcName+"_public_ip_ssh")
	if err != nil {
		// TODO: Annotate
		return
	}
	sgID = *sg.GroupId

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

	// Add the security group rule
	addSecurityGroupRules(ctx, d, sgID, types.SecurityGroupRuleUpdate{
		SecurityGroupRuleId: aws.String("default_ssh"),
		SecurityGroupRule: &types.SecurityGroupRuleRequest{
			CidrIpv4:    aws.String(fmt.Sprintf("%s/32", pubIP)),
			Description: aws.String("SSH from caller"),
			FromPort:    aws.Int32(22),
			ToPort:      aws.Int32(22),
			IpProtocol:  aws.String("tcp"),
		},
	})

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

func addSecurityGroupRules(ctx context.Context, d *Driver, groupID string, rules ...types.SecurityGroupRuleUpdate) error {
	_, err := d.client.ModifySecurityGroupRules(ctx, &ec2.ModifySecurityGroupRulesInput{
		GroupId:            aws.String(groupID),
		SecurityGroupRules: rules,
	})
	return err
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
		return "", fmt.Errorf(
			"%w: received HTTP status code %d",
			ErrPublicAddrLookupFailure, res.StatusCode,
		)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPublicAddrLookupFailure, err)
	}

	return string(data), nil
}
