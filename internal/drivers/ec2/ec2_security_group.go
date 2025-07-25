package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

var ErrSecurityGroupInboundRuleCreate = fmt.Errorf("failed to add security group rule")

func sgInboundRuleCreate(ctx context.Context, client *ec2.Client, from string, port int32, sgID string) error {
	_, err := client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		CidrIp:            aws.String(from),
		FromPort:          aws.Int32(port),
		ToPort:            aws.Int32(port),
		GroupId:           &sgID,
		IpProtocol:        aws.String("tcp"),
		TagSpecifications: tagSpecificationWithDefaults(types.ResourceTypeSecurityGroupRule),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSecurityGroupInboundRuleCreate, err)
	}
	return nil
}
