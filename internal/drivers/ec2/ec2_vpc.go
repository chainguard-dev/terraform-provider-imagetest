package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

var (
	ErrVPCCreate = fmt.Errorf("failed VPC creation")
	ErrNilVPCID  = fmt.Errorf("received no error in VPC create, but the VPC ID returned was nil")
)

func vpcCreate(ctx context.Context, client *ec2.Client, vpcName, vpcCIDR string) (string, error) {
	log := clog.FromContext(ctx).With("name", vpcName, "cidr", vpcCIDR)
	log.Debug("creating VPC")
	result, err := client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String(vpcCIDR),
		TagSpecifications: tagSpecificationWithDefaults(types.ResourceTypeVpc, types.Tag{
			Key:   aws.String("Name"),
			Value: &vpcName,
		}),
	})
	if err != nil {
		log.Error("VPC creation failed", "error", err)
		return "", fmt.Errorf("%w: %w", ErrVPCCreate, err)
	}
	if result.Vpc == nil || result.Vpc.VpcId == nil {
		log.Error("VPC creation failed", "error", ErrNilVPCID)
		return "", ErrNilVPCID
	}
	log.Debug("created VPC successfully")
	return *result.Vpc.VpcId, nil
}

var ErrVPCDelete = fmt.Errorf("failed to delete VPC")

func vpcDelete(ctx context.Context, client *ec2.Client, vpcID string) error {
	_, err := client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcID),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVPCDelete, err)
	}
	return err
}

var (
	ErrVPCDefaultSecurityGroupGet          = fmt.Errorf("failed to retrieve the VPC's default security group")
	ErrVPCDefaultSecurityGroupGetIDNil     = fmt.Errorf("encountered no error in VPC default security group retrieval, but the returned security group ID was nil")
	ErrVPCDefaultSecurityGroupGetNoResults = fmt.Errorf("found no security groups for the provided VPC")
)

func vpcDefaultSecurityGroup(ctx context.Context, client *ec2.Client, vpcID string) (string, error) {
	result, err := client.GetSecurityGroupsForVpc(ctx, &ec2.GetSecurityGroupsForVpcInput{
		VpcId: &vpcID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrVPCDefaultSecurityGroupGet, err)
	}
	if len(result.SecurityGroupForVpcs) == 0 {
		return "", ErrVPCDefaultSecurityGroupGetNoResults
	}
	sg := result.SecurityGroupForVpcs[0]
	if sg.GroupId == nil {
		return "", ErrVPCDefaultSecurityGroupGetIDNil
	}
	return *sg.GroupId, nil
}
