package ec2

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type NetworkDeployment struct {
	// VPC
	VPCName string
	VPCID   string
	VPCCIDR string
	// Subnet
	SubnetName string
	SubnetID   string
	SubnetCIDR string
	// Internet Gateway
	IGWName string
	IGWID   string
	// Route Table
	RTBID string
	// Security Group
	SGID string
	// Elastic IP
	ElasticIP   string
	ElasticIPID string
	// Network Interface
	InterfaceID string
}

func (d *Driver) deployNetwork(ctx context.Context) (NetworkDeployment, error) {
	log := clog.FromContext(ctx)

	net := NetworkDeployment{
		VPCName:    d.runID + "-vpc",
		SubnetName: d.runID + "-subnet",
		IGWName:    d.runID + "-igw",
		VPCCIDR:    "172.25.0.0/24",
		SubnetCIDR: "172.25.0.0/25",
	}

	// Create the VPC.
	var err error
	net.VPCID, err = vpcCreate(ctx, d.client, net.VPCName, net.VPCCIDR)
	if err != nil {
		return net, err
	}
	log.Info("created VPC", "id", net.VPCID)
	// Queue the VPC delete.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("deleting VPC", "id", net.VPCID)
		return vpcDelete(ctx, d.client, net.VPCID)
	})

	// Create the VPC subnet.
	net.SubnetID, err = subnetCreate(ctx, d.client, net.VPCID, net.SubnetName, net.SubnetCIDR)
	if err != nil {
		return net, err
	}
	log.Info("created VPC subnet", "id", net.SubnetID)
	// Queue the VPC subnet delete.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("deleting VPC subnet", "id", net.SubnetID)
		return subnetDelete(ctx, d.client, net.SubnetID)
	})

	// Create the internet gateway.
	net.IGWID, err = internetGatewayCreate(ctx, d.client, net.IGWName)
	if err != nil {
		return net, err
	}
	log.Info("created internet gateway", "id", net.IGWID)
	// Queue the internet gateway destructor.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("deleting internet gateway", "id", net.IGWID)
		return internetGatewayDelete(ctx, d.client, net.IGWID)
	})

	// Attach the internet gateway to the VPC.
	//
	// NOTE: This doesn't need to be torn down manually, the IGW destructor will
	// do it automatically.
	if err := internetGatewayAttach(ctx, d.client, net.VPCID, net.IGWID); err != nil {
		return net, err
	}
	log.Info(
		"internet gateway attached to VPC",
		"internet_gateway_id", net.IGWID,
		"vpc_id", net.VPCID,
	)
	// Queue the internet gateway VPC detach.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("detaching internet gateway", "id", net.IGWID)
		return internetGatewayDetach(ctx, d.client, net.VPCID, net.IGWID)
	})

	// Locate the VPC's main route table (this is created automatically when
	// creating a VPC).
	//
	// Unfortunately, the object returned from the VPC creation contains no
	// information about the route table. But, we need the route table to add a
	// route to it to the internet gateway we created.
	net.RTBID, err = routeTableGetForVPC(ctx, d.client, net.VPCID)
	if err != nil {
		return net, err // No annotation required.
	}
	const defaultRouteCIDR = "0.0.0.0/0"
	err = routeTableIGWRouteCreate(ctx, d.client, net.RTBID, defaultRouteCIDR, net.IGWID)
	if err != nil {
		return net, err // No annotation required.
	}
	log.Info("created default route to internet gateway", "rtb_id", net.RTBID)
	// Queue the route table route delete.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("deleting route table route", "rtb_id", net.RTBID)
		return routeTableRouteDelete(ctx, d.client, net.RTBID, defaultRouteCIDR)
	})

	// Get the public address of this host.
	//
	// TODO: This seems like something we're going to want to create a dumb but
	// self-hosted service to handle.
	pubIP, err := publicAddr()
	if err != nil {
		return net, fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
	}
	log.Info("identified local station public IP address", "ipv4_addr", pubIP)
	// Create a single-address CIDR notation representation of the public address.
	pubIPCIDR, err := singleIPCIDR(pubIP)
	if err != nil {
		return net, err
	}
	log.Info("generated single-IP CIDR", "cidr", pubIPCIDR)
	// Get the VPC's default security group.
	net.SGID, err = vpcDefaultSecurityGroupGet(ctx, d.client, net.VPCID)
	if err != nil {
		return net, err
	}
	log.Info("retrieved default VPC security group", "id", net.SGID)
	// Apply default security group rules.
	//
	// By default we'll only open TCP/22 (SSH)/ to the host we're calling from.
	// Unfortunately, this can't be done in the initial request.
	err = securityGroupInboundRuleCreate(ctx, d.client, ec2.AuthorizeSecurityGroupIngressInput{
		CidrIp:            aws.String(pubIPCIDR),
		FromPort:          aws.Int32(portSSH),
		ToPort:            aws.Int32(portSSH),
		GroupId:           &net.SGID,
		IpProtocol:        aws.String("tcp"),
		TagSpecifications: tagSpecificationWithDefaults(types.ResourceTypeSecurityGroupRule),
	})
	if err != nil {
		return net, err
	}
	log.Info(
		"created inbound security group SSH rule",
		"from", pubIPCIDR,
		"port", portSSH,
		"proto", "tcp",
		"security_group_id", net.SGID,
	)

	// Allocate an elastic IP address (public IP) for this EC2 instance.
	net.ElasticIPID, net.ElasticIP, err = elasticIPCreate(ctx, d.client)
	if err != nil {
		return net, err
	}
	log.Info("created elastic IP", "id", net.ElasticIPID)
	// Queue elastic IP delete.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info("deleting elastic IP", "id", net.ElasticIPID)
		return elasticIPDelete(ctx, d.client, net.ElasticIPID)
	})

	// Create an elastic network interface for the instance.
	net.InterfaceID, err = netIFCreate(ctx, d.client, net.SubnetID)
	if err != nil {
		return net, err
	}
	log.Info("created EC2 instance network interface", "id", net.InterfaceID)
	// Queue the elastic network interface destructor.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info(
			"deleting EC2 network interface",
			"id", net.InterfaceID,
		)
		return netIFDelete(ctx, d.client, net.InterfaceID)
	})

	// Associate the elastic IP to the network interface.
	attachID, err := elasticIPAttach(ctx, d.client, net.ElasticIPID, net.InterfaceID)
	if err != nil {
		return net, err
	}
	log.Info("attaching elastic IP to network interface", "attach_id", attachID)
	// Queue the elastic IP detach.
	d.stack.Push(func(ctx context.Context) error {
		clog.FromContext(ctx).Info(
			"detaching elastic IP from network interface",
			"attach_id", attachID,
		)
		return elasticIPDetach(ctx, d.client, attachID)
	})

	/* 3.. hours.. later.. */
	return net, nil
}
