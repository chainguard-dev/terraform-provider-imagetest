package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

////////////////////////////////////////////////////////////////////////////////
// Security Group

var (
	ErrSecurityGroupCreate = fmt.Errorf("failed to create security group")
	ErrSecurityGroupIDNil  = fmt.Errorf("encountered no error in security group create, but the returned security group ID was nil")
)

func securityGroupCreate(ctx context.Context, client *ec2.Client, vpcID, sgName string) (string, error) {
	result, err := client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   &sgName,
		Description: &sgName,
		VpcId:       &vpcID,
		TagSpecifications: tagSpecificationWithDefaults(
			types.ResourceTypeSecurityGroup,
			types.Tag{
				Key:   aws.String("Name"),
				Value: &sgName,
			}),
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSecurityGroupCreate, err)
	}
	if result.GroupId == nil {
		return "", ErrSecurityGroupIDNil
	}
	return *result.GroupId, nil
}

var ErrSecurityGroupVPCAttach = fmt.Errorf("failed to attach security group to VPC")

func securityGroupVPCAttach(ctx context.Context, client *ec2.Client, securityGroupID, vpcID string) error {
	_, err := client.AssociateSecurityGroupVpc(ctx, &ec2.AssociateSecurityGroupVpcInput{
		GroupId: &securityGroupID,
		VpcId:   &vpcID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSecurityGroupVPCAttach, err)
	}
	return nil
}

var ErrSecurityGroupVPCDetach = fmt.Errorf("failed to detach security group from VPC")

func securityGroupVPCDetach(ctx context.Context, client *ec2.Client, securityGroupID, vpcID string) error {
	_, err := client.DisassociateSecurityGroupVpc(ctx, &ec2.DisassociateSecurityGroupVpcInput{
		GroupId: &securityGroupID,
		VpcId:   &vpcID,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSecurityGroupVPCDetach, err)
	}
	return nil
}

var ErrSecurityGroupDelete = fmt.Errorf("failed to delete security group")

func securityGroupDelete(ctx context.Context, client *ec2.Client, securityGroupID string) error {
	_, err := client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
		GroupId: &securityGroupID,
	})
	if err != nil {
		clog.FromContext(ctx).Error(
			"failed to delete security group",
			"id", securityGroupID,
		)
		return fmt.Errorf("%w: %w", ErrSecurityGroupDelete, err)
	}
	return nil
}

var ErrSecurityGroupInboundRuleCreate = fmt.Errorf("failed to add security group rule")

func securityGroupInboundRuleCreate(ctx context.Context, client *ec2.Client, rule ec2.AuthorizeSecurityGroupIngressInput) error {
	_, err := client.AuthorizeSecurityGroupIngress(ctx, &rule)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSecurityGroupInboundRuleCreate, err)
	}
	return nil
}
