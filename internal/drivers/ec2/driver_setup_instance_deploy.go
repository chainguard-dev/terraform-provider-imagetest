package ec2

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

// deployInstance provisions an ED25519 keypair (for SSH), imports it to AWS and
// launches an EC2 instance assigned this keypair, as well as the Elastic
// Network Interface constructed earlier in 'deployNetwork'.
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
	inst.InstanceName = d.runID + "-instance"
	inst.InstanceType = d.InstanceType
	inst.AMI = d.AMI
	log.Info(
		"launching EC2 instance",
		"instance_type", inst.InstanceType,
		"ami", inst.AMI,
	)
	instanceID, err := instanceCreateWithNetIF(
		ctx,
		d.client,
		inst.InstanceType, inst.AMI, inst.KeyName, net.InterfaceID,
		tagName(inst.InstanceName),
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
		log.Info("waiting for instance to enter 'terminated' state")
		err = awaitInstanceState(ctx, d.client, instanceID, types.InstanceStateNameTerminated)
		if err != nil {
			return fmt.Errorf("encountered error in instance state transition await: %w", err)
		}
		log.Info("instance termination is successful")
		return nil
	})

	// Wait for the host to become reachable via SSH.
	log.Info("beginning wait for instance to become reachable via SSH")
	if err = waitTCP(ctx, net.ElasticIP, portSSH); err != nil {
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
	log.Info("ED25519 keypair generate is successful")

	// Marshal the public key to the PEM-encoded OpenSSH format.
	pubKey, err := keys.Public.MarshalOpenSSH()
	if err != nil {
		return keys, "", "", err // No wrapping required here.
	}
	log.Debug("ED25519 public key marshal to PEM-encoded OpenSSH format is successful")

	// Import the keypair to AWS.
	//
	// This allows us to assign it to the EC2 instance when we launch it.
	keyName := d.runID + "-kp"
	keyID, err := keypairImport(ctx, d.client, keyName, pubKey)
	if err != nil {
		return keys, "", "", err // No wrapping required here.
	}
	log.Info(
		"ED25519 keypair import to AWS is successful",
		"id", keyID,
		"name", keyName,
	)

	return keys, keyID, keyName, nil
}

type InstanceDeployment struct {
	// Instance
	AMI          string
	InstanceName string
	InstanceID   string
	InstanceType types.InstanceType
	// Keys
	Keys    ssh.ED25519KeyPair
	KeyName string
	KeyID   string
}
