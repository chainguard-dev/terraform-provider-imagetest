package ec2

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/chainguard-dev/clog"
)

type subnet struct {
	client *ec2.Client
	vpcID  string
	region string
	cidr   string // if empty, auto-detect
	tags   []types.Tag

	id string
}

var _ resource = (*subnet)(nil)

func (s *subnet) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	// If explicit CIDR provided, use it directly
	if s.cidr != "" {
		return s.createWithCIDR(ctx, s.cidr)
	}

	// Otherwise, try random CIDRs until one works
	vpcCIDR, numSubnets, err := s.getVPCInfo(ctx)
	if err != nil {
		return nil, err
	}

	// Retry with timeout - parallel tests may conflict on CIDR selection
	const retryTimeout = 30 * time.Second
	retryCtx, cancel := context.WithTimeout(ctx, retryTimeout)
	defer cancel()

	var lastErr error
	for {
		cidr := randomCIDR(vpcCIDR, numSubnets)
		log.Debug("attempting to create subnet", "cidr", cidr)

		teardown, err := s.createWithCIDR(ctx, cidr)
		if err == nil {
			return teardown, nil
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidSubnet.Conflict" {
			log.Debug("subnet CIDR conflict, retrying", "cidr", cidr)
			lastErr = err

			select {
			case <-retryCtx.Done():
				return nil, fmt.Errorf("failed to create subnet after %s: %w", retryTimeout, lastErr)
			default:
				continue
			}
		}

		return nil, err
	}
}

func (s *subnet) createWithCIDR(ctx context.Context, cidr string) (Teardown, error) {
	log := clog.FromContext(ctx)

	result, err := s.client.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(s.vpcID),
		CidrBlock:        aws.String(cidr),
		AvailabilityZone: aws.String(s.region + "a"),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeSubnet,
			Tags:         s.tags,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("creating subnet: %w", err)
	}

	s.id = *result.Subnet.SubnetId
	log.Info("created subnet", "id", s.id, "cidr", cidr)

	_, err = s.client.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
		SubnetId:            aws.String(s.id),
		MapPublicIpOnLaunch: &types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return nil, fmt.Errorf("enabling auto-assign public IP: %w", err)
	}

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("deleting subnet", "id", s.id, "cidr", cidr)
		_, err := s.client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
			SubnetId: aws.String(s.id),
		})
		if err != nil {
			log.Warn("failed to delete subnet", "id", s.id, "error", err)
			return err
		}
		log.Info("subnet deleted", "id", s.id)
		return nil
	}

	return teardown, nil
}

func (s *subnet) getVPCInfo(ctx context.Context) (vpcNet *net.IPNet, numSubnets int, err error) {
	vpcResult, err := s.client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []string{s.vpcID},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("describing VPC: %w", err)
	}
	if len(vpcResult.Vpcs) == 0 {
		return nil, 0, fmt.Errorf("VPC %s not found", s.vpcID)
	}

	vpcCIDR := aws.ToString(vpcResult.Vpcs[0].CidrBlock)
	_, vpcNet, err = net.ParseCIDR(vpcCIDR)
	if err != nil {
		return nil, 0, fmt.Errorf("parsing VPC CIDR %s: %w", vpcCIDR, err)
	}

	ones, _ := vpcNet.Mask.Size()
	if ones > 28 {
		return nil, 0, fmt.Errorf("VPC CIDR %s is smaller than /28", vpcCIDR)
	}

	numSubnets = 1 << (28 - ones)
	return vpcNet, numSubnets, nil
}

func randomCIDR(vpcNet *net.IPNet, numSubnets int) string {
	vpcIP := vpcNet.IP.To4()
	baseInt := binary.BigEndian.Uint32(vpcIP)

	idx := rand.IntN(numSubnets)
	subnetInt := baseInt + uint32(idx*16)

	subnetIP := make(net.IP, 4)
	binary.BigEndian.PutUint32(subnetIP, subnetInt)

	return fmt.Sprintf("%s/28", subnetIP.String())
}
