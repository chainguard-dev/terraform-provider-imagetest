package ec2

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2/pricelist"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

type InstanceDeployment struct {
	// Instance
	InstanceName string
	InstanceID   string
	InstanceType types.InstanceType
	// Keys
	Keys    ssh.ED25519KeyPair
	KeyName string
	KeyID   string
}

func (d *Driver) deployInstance(ctx context.Context, net NetworkDeployment) (InstanceDeployment, error) {
	log := clog.FromContext(ctx)
	var inst InstanceDeployment
	// Provision an ED25519 keypair for SSH.
	var err error
	inst.Keys, inst.KeyID, inst.KeyName, err = d.provisionKeys(ctx)
	if err != nil {
		return inst, err // No wrapping required here.
	}
	log.Info("successfully generated ED25519 keypair")
	// Queue the keypair delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting keypair", "id", inst.KeyID)
		return keypairDelete(ctx, d.client, inst.KeyID)
	})
	// Launch the EC2 instance.
	//
	// Select the most cost-effective instance type.
	inst.InstanceType, err = d.selectInstanceType(ctx)
	if err != nil {
		return inst, fmt.Errorf("%w: %w", ErrInstanceTypeSelection, err)
	}
	log.Info("selected instance type", "instance_type", inst.InstanceType)
	// Select AMI.
	ami, err := d.selectAMI(ctx)
	if err != nil {
		return inst, fmt.Errorf("%w: %w", ErrAMISelection, err)
	}
	log.Info("selected machine image", "ami_id", ami)
	// Launch the instanceID.
	instanceID, err := instanceCreateWithNetIF(
		ctx,
		d.client,
		inst.InstanceType, ami, inst.KeyName, net.InterfaceID,
	)
	if err != nil {
		return inst, err
	}
	log.Info("EC2 instance launched", "instance_id", instanceID)
	// Queue the instance destructor.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting EC2 instance", "instance_id", instanceID)
		if err := instanceDelete(ctx, d.client, instanceID); err != nil {
			return err
		}
		// The EC2 instance actually hitting the 'Terminated' state is a hard
		// blocker on removing dependencies further up the chain. So, we need to
		// wait for it to actually be gone (state == 'Terminated').
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		log.Debug("waiting for instance to enter 'terminated' state")
		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("deadlined waiting for EC2 instance termination")
			case <-time.After(5 * time.Second):
				state, err := instanceState(ctx, d.client, instanceID)
				if err != nil {
					return err
				}
				if state == types.InstanceStateNameTerminated {
					log.Info("instance termination complete")
					return nil
				} else {
					log.Debug("instance still terminating, waiting longer", "state", state)
				}
			}
		}
	})
	// Wait for the host to become reachable via SSH.
	if err = waitTCP(ctx, net.ElasticIP, portSSH); err != nil {
		log.Error(
			"encountered error waiting for SSH to become available",
			"error", err,
		)
		return inst, fmt.Errorf("%w: %w", ErrInWait, err)
	}
	log.Info("instance is reachable via SSH")
	return inst, nil
}

var ErrKeyImport = fmt.Errorf("failed public key import to AWS")

func (d *Driver) provisionKeys(ctx context.Context) (ssh.ED25519KeyPair, string, string, error) {
	log := clog.FromContext(ctx)
	// Provision an SSH key to connect to the instance.
	keys, err := ssh.NewED25519KeyPair()
	if err != nil {
		return keys, "", "", err // No wrapping required here.
	}
	log.Info("keypair generated successfully")
	// Marshal the public key to the PEM-encoded OpenSSH format.
	pubKey, err := keys.Public.MarshalOpenSSH()
	if err != nil {
		return keys, "", "", err // No wrapping required here.
	}
	log.Debug("successfully marshaled public key")
	// Import the keypair to AWS.
	//
	// This allows us to assign it to the EC2 instance when we launch it.
	keyName := d.runID + "-kp"
	keyID, err := keypairImport(ctx, d.client, keyName, pubKey)
	if err != nil {
		return keys, "", "", err // No wrapping required here.
	}
	log.Info(
		"successfully imported generated keypair",
		"id", keyID,
		"name", keyName,
	)
	return keys, keyID, keyName, nil
}

var ErrNoAMI = fmt.Errorf("an AMI ID was not provided")

func (d *Driver) selectAMI(ctx context.Context) (string, error) {
	const amiEnvVar = "IMAGE_TEST_AMI"
	log := clog.FromContext(ctx)
	if d.AMI != "" {
		log.Debug("using user-provided machine image")
		return d.AMI, nil
	} else if ami, ok := os.LookupEnv(amiEnvVar); ok && ami != "" {
		log.Debug("using machine image provided via environment")
		return ami, nil
	} else {
		log.Error("failed to identify a machine image")
		return "", ErrNoAMI
	}
}

var (
	ErrPreFiltersBuild       = fmt.Errorf("failed to build instance filters")
	ErrDescribeInstanceTypes = fmt.Errorf("failed to describe instance types")
	ErrNoInstanceTypes       = fmt.Errorf("found no instance types which satisfy all input requirements")
)

func (d *Driver) selectInstanceType(ctx context.Context) (types.InstanceType, error) {
	log := clog.FromContext(ctx)
	// If the 'Driver' has 'InstanceType' set, skip the selection process and use
	// that.
	if d.InstanceType != "" {
		log.Info("using provided EC2 instance type", "instance_type", d.InstanceType)
		return d.InstanceType, nil
	}
	log.Debug("proceeding to automatic instance type selection")
	// Assemble pre filters
	//
	// A number of things (GPU kind, disk capacity) cannot be filtered in-request
	// so we try to pack as much into the request as we can, then filter the rest
	// further down.
	//
	// NOTE: yes, there is a listed filter for storage capacity in the docs - it
	// does not work. \o/ Also there just are no filters for GPUs.
	filters, err := filtersPreBuild(ctx, d)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPreFiltersBuild, err)
	}
	log.Info("assembled filters for instance type selection", "count", len(filters))
	// Init the 'DescribeInstanceTypesInput'.
	//
	// 'DescribeInstanceTypesInput' is the "request body" used to ask the AWS Go
	// SDK v2 for a list of EC2 instance types
	describe := &ec2.DescribeInstanceTypesInput{
		Filters: filters,
	}
	// Roll all paginated EC2 'InstanceTypeInfo' results up.
	var instanceTypeInfos []types.InstanceTypeInfo
	var nextToken *string
	for {
		// If we caught a next-page token on the previous iteration, apply it.
		if nextToken != nil {
			describe.NextToken = nextToken
		}
		// Fetch the next page of results.
		results, err := d.client.DescribeInstanceTypes(ctx, describe)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrDescribeInstanceTypes, err)
		}
		instanceTypeInfos = append(instanceTypeInfos, results.InstanceTypes...)
		// If we don't have a 'NextToken' (next page of results hint), we're done.
		if results.NextToken == nil {
			break
		}
		// Set the next-page token for the next iteration.
		nextToken = results.NextToken
	}
	log.Info(
		"completed EC2 instance type fetch",
		"instance_type_count", len(instanceTypeInfos),
	)
	// Make sure we received a non-zero number of instance types.
	if len(instanceTypeInfos) == 0 {
		return "", ErrNoInstanceTypes
	}
	// Apply post-request filters.
	instanceTypeInfos = filtersPostApply(ctx, d, instanceTypeInfos)
	log.Debug(
		"post-request filters applied",
		"remaining_instance_count", len(instanceTypeInfos),
	)
	// Of the instance types which fulfill our requirements, select the cheapest.
	instanceTypes := make([]types.InstanceType, len(instanceTypeInfos))
	for i := range len(instanceTypeInfos) {
		instanceTypes[i] = instanceTypeInfos[i].InstanceType
	}
	instanceType, price := pricelist.SelectCheapest(instanceTypes)
	if instanceType == "" || price == 0 {
		return "", ErrNoInstanceTypes
	}
	log.Info(
		"selected EC2 instance type",
		"type", instanceType,
		"cost", fmt.Sprintf("$%.2f/hr", price),
	)
	return instanceType, nil
}
