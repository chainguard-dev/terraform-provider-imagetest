package ec2

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/clog"
)

func (d *Driver) deployNetwork(ctx context.Context) (NetworkDeployment, error) {
	log := clog.FromContext(ctx)

	net := NetworkDeployment{
		VPCName:       d.runID + "-vpc",
		SubnetName:    d.runID + "-subnet",
		IGWName:       d.runID + "-igw",
		InterfaceName: d.runID + "-if",
		VPCCIDR:       "172.25.0.0/24",
		SubnetCIDR:    "172.25.0.0/25",
		ElasticIPName: d.runID + "-eip",
	}

	// Create the VPC.
	var err error
	net.VPCID, err = vpcCreate(
		ctx,
		d.ec2Client,
		net.VPCName, net.VPCCIDR,
		tagName(net.VPCName),
	)
	if err != nil {
		return net, err
	}
	log.Info("VPC creation is successful", "id", net.VPCID)
	// Queue the VPC delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting VPC", "id", net.VPCID)
		return vpcDelete(ctx, d.ec2Client, net.VPCID)
	})

	// Create the VPC subnet.
	net.SubnetID, err = subnetCreate(
		ctx,
		d.ec2Client,
		net.VPCID, net.SubnetCIDR,
		tagName(net.SubnetName),
	)
	if err != nil {
		return net, err
	}
	log.Info("VPC subnet creation is successful", "id", net.SubnetID)
	// Queue the VPC subnet delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting VPC subnet", "id", net.SubnetID)
		return subnetDelete(ctx, d.ec2Client, net.SubnetID)
	})

	// Create the internet gateway.
	net.IGWID, err = internetGatewayCreate(
		ctx,
		d.ec2Client,
		tagName(net.IGWName),
	)
	if err != nil {
		return net, err
	}
	log.Info("internet gateway creation is successful", "id", net.IGWID)
	// Queue the internet gateway destructor.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting internet gateway", "id", net.IGWID)
		return internetGatewayDelete(ctx, d.ec2Client, net.IGWID)
	})

	// Attach the internet gateway to the VPC.
	//
	// NOTE: This doesn't need to be torn down manually, the IGW destructor will
	// do it automatically.
	if err := internetGatewayAttach(ctx, d.ec2Client, net.VPCID, net.IGWID); err != nil {
		return net, err
	}
	log.Info(
		"internet gateway attachment to VPC subnet is successful",
		"internet_gateway_id", net.IGWID,
		"vpc_id", net.VPCID,
	)
	// Queue the internet gateway VPC detach.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("detaching internet gateway", "id", net.IGWID)
		return internetGatewayDetach(ctx, d.ec2Client, net.VPCID, net.IGWID)
	})

	// Locate the VPC's main route table (this is created automatically when
	// creating a VPC).
	//
	// Unfortunately, the object returned from the VPC creation contains no
	// information about the route table. But, we need the route table to add a
	// route to it to the internet gateway we created.
	net.RTBID, err = routeTableGetForVPC(ctx, d.ec2Client, net.VPCID)
	if err != nil {
		return net, err // No annotation required.
	}
	const defaultRouteCIDR = "0.0.0.0/0"
	err = routeTableIGWRouteCreate(
		ctx,
		d.ec2Client,
		net.RTBID, defaultRouteCIDR, net.IGWID,
	)
	if err != nil {
		return net, err // No annotation required.
	}
	log.Info(
		"default route creation to internet gateway is successful",
		"rtb_id", net.RTBID,
	)
	// Queue the route table route delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting route table route", "rtb_id", net.RTBID)
		return routeTableRouteDelete(ctx, d.ec2Client, net.RTBID, defaultRouteCIDR)
	})

	// Get the public address of this host.
	//
	// TODO: This seems like something we're going to want to create a dumb but
	// self-hosted service to handle.
	localPublicAddr, err := publicAddr()
	if err != nil {
		return net, fmt.Errorf("%w: %w", ErrPublicIPLookup, err)
	}
	log.Info(
		"local station public IP address identification is successful",
		"addr", localPublicAddr,
	)
	// Create a single-address CIDR notation representation of the public address.
	localPublicAddrCIDR, err := singleAddrCIDR(localPublicAddr)
	if err != nil {
		return net, err
	}
	log.Debug("generated local public address CIDR", "cidr", localPublicAddrCIDR)
	// Get the VPC's default security group.
	net.SGID, err = vpcDefaultSecurityGroup(ctx, d.ec2Client, net.VPCID)
	if err != nil {
		return net, err
	}
	log.Info("default VPC security group retrieval is successful", "id", net.SGID)
	// Apply default security group rules.
	//
	// By default we'll only open TCP/22 (SSH)/ to the host we're calling from.
	// Unfortunately, this can't be done in the initial request.
	err = sgInboundRuleCreate(
		ctx,
		d.ec2Client, localPublicAddrCIDR, portSSH, net.SGID,
	)
	if err != nil {
		return net, err
	}
	log.Info(
		"default VPC security group inbound rule creation is successful",
		"from", localPublicAddrCIDR,
		"port", portSSH,
		"proto", "tcp",
		"security_group_id", net.SGID,
	)

	// Allocate an elastic IP address (public IP) for this EC2 instance.
	net.ElasticIPID, net.ElasticIP, err = elasticIPCreate(
		ctx,
		d.ec2Client,
		tagName(net.ElasticIPName),
	)
	if err != nil {
		return net, err
	}
	log.Info("elastic IP creation is successful", "id", net.ElasticIPID)
	// Queue elastic IP delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting elastic IP", "id", net.ElasticIPID)
		return elasticIPDelete(ctx, d.ec2Client, net.ElasticIPID)
	})

	// Create an elastic network interface for the instance.
	net.InterfaceID, err = netIFCreate(
		ctx,
		d.ec2Client, net.SubnetID,
		tagName(net.InterfaceName),
	)
	if err != nil {
		return net, err
	}
	log.Info(
		"elastic network interface creation is successful",
		"id", net.InterfaceID,
	)
	// Queue the elastic network interface destructor.
	d.stack.Push(func(ctx context.Context) error {
		log.Info(
			"deleting EC2 network interface",
			"id", net.InterfaceID,
		)
		return netIFDelete(ctx, d.ec2Client, net.InterfaceID)
	})

	// Associate the elastic IP to the network interface.
	attachID, err := elasticIPAttach(ctx, d.ec2Client, net.ElasticIPID, net.InterfaceID)
	if err != nil {
		return net, err
	}
	log.Info(
		"elastic IP attachment to elastic network interface is successful",
		"attach_id", attachID,
	)
	// Queue the elastic IP detach.
	d.stack.Push(func(ctx context.Context) error {
		log.Info(
			"detaching elastic IP from network interface",
			"attach_id", attachID,
		)
		return elasticIPDetach(ctx, d.ec2Client, attachID)
	})

	/* 3.. hours.. later.. */
	return net, nil
}

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
	ElasticIPName string
	ElasticIP     string
	ElasticIPID   string
	// Network Interface
	InterfaceName string
	InterfaceID   string
}
