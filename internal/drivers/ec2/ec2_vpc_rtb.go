package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

////////////////////////////////////////////////////////////////////////////////
// Route Table

var (
	ErrRouteTableGetForVPC = fmt.Errorf("failed to fetch route table for VPC")
	ErrNoRouteTable        = fmt.Errorf("found no route tables for the provided VPC ID")
	ErrNilRouteTableID     = fmt.Errorf("received no error in describe route table call, but the route table ID returned was nil")
)

func routeTableGetForVPC(ctx context.Context, client *ec2.Client, vpcID string) (string, error) {
	result, err := client.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcID},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrRouteTableGetForVPC, err)
	}
	if len(result.RouteTables) == 0 {
		return "", ErrNoRouteTable
	}
	rtb := result.RouteTables[0]
	if rtb.RouteTableId == nil {
		return "", ErrNilRouteTableID
	}
	return *rtb.RouteTableId, nil
}

var ErrRouteTableRouteCreate = fmt.Errorf("failed to add route to route table")

// Open to suggestions on names! 'addRouteTableDefaultRouteToInternetGateway'
// seemed a bit much. x'D
func routeTableIGWRouteCreate(ctx context.Context, client *ec2.Client, rtbID, destCIDR, igwID string) error {
	result, err := client.CreateRoute(ctx, &ec2.CreateRouteInput{
		RouteTableId:         &rtbID,
		GatewayId:            &igwID,
		DestinationCidrBlock: &destCIDR,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRouteTableRouteCreate, err)
	}
	if result.Return == nil || !*result.Return {
		return ErrRouteTableRouteCreate
	}
	return nil
}

var ErrRouteTableRouteDelete = fmt.Errorf("failed to delete route table route")

func routeTableRouteDelete(ctx context.Context, client *ec2.Client, rtbID, destCIDR string) error {
	_, err := client.DeleteRoute(ctx, &ec2.DeleteRouteInput{
		RouteTableId:         &rtbID,
		DestinationCidrBlock: &destCIDR,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRouteTableRouteDelete, err)
	}
	return nil
}
