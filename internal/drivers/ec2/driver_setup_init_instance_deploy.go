package ec2

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

type InstanceDeployment struct {
	// Instance
	AMI                 string
	InstanceName        string
	InstanceID          string
	InstanceType        types.InstanceType
	InstanceProfileName string
	// Keys
	Keys    ssh.ED25519KeyPair
	KeyName string
	KeyID   string
	KeyPath string
}

// deployInstance orchestrates the creation of an EC2 instance with all necessary
// infrastructure including SSH keys, IAM roles, and instance profiles.
func (d *Driver) deployInstance(ctx context.Context, net NetworkDeployment) (InstanceDeployment, error) {
	var inst InstanceDeployment

	// Initialize basic instance properties
	inst.InstanceName = d.runID + "-instance"
	inst.InstanceType = d.InstanceType
	inst.InstanceProfileName = d.InstanceProfileName
	inst.AMI = d.AMI

	// Setup SSH keys for instance access
	if err := d.setupSSHKeys(ctx, &inst, net); err != nil {
		return inst, fmt.Errorf("failed to setup SSH keys: %w", err)
	}

	// Ensure IAM instance profile exists (create if needed)
	profileName, err := d.ensureInstanceProfile(ctx, inst.InstanceProfileName)
	if err != nil {
		return inst, fmt.Errorf("failed to ensure instance profile: %w", err)
	}
	inst.InstanceProfileName = profileName

	// Launch the EC2 instance
	if err := d.launchEC2Instance(ctx, &inst, net); err != nil {
		return inst, fmt.Errorf("failed to launch EC2 instance: %w", err)
	}

	// Wait for the instance to be ready
	if err := awaitInstanceLaunch(ctx, d.ec2Client, inst.InstanceID, net.ElasticIP, portSSH); err != nil {
		return inst, fmt.Errorf("failed waiting for instance to be ready: %w", err)
	}

	return inst, nil
}

var ErrKeyImport = fmt.Errorf("failed public key import to AWS")

// ensureInstanceProfile creates IAM role and instance profile if none provided.
func (d *Driver) ensureInstanceProfile(ctx context.Context, currentProfileName string) (string, error) {
	log := clog.FromContext(ctx)

	// If user provided a profile, use it.
	if currentProfileName != "" {
		return currentProfileName, nil
	}

	log.Info("no instance profile specified, creating IAM role and instance profile")

	// Create IAM role.
	roleName := d.runID + "-role"
	roleArn, err := iamRoleCreate(ctx, d.iamClient, roleName, iamTagsDefaultWithName(roleName)...)
	if err != nil {
		return "", fmt.Errorf("failed to create IAM role: %w", err)
	}
	log.Info("IAM role created", "role_name", roleName, "role_arn", roleArn)

	// Queue role deletion.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting IAM role", "role_name", roleName)
		return iamRoleDelete(ctx, d.iamClient, roleName)
	})

	// Attach ECR ReadOnly policy to the role.
	err = iamRoleAttachPolicy(ctx, d.iamClient, roleName, ecrReadOnlyPolicyArn)
	if err != nil {
		return "", fmt.Errorf("failed to attach policy to IAM role: %w", err)
	}

	// Queue policy detachment
	d.stack.Push(func(ctx context.Context) error {
		log.Info("detaching policy from IAM role", "role_name", roleName, "policy_arn", ecrReadOnlyPolicyArn)
		return iamRoleDetachPolicy(ctx, d.iamClient, roleName, ecrReadOnlyPolicyArn)
	})

	// Create instance profile.
	profileName := d.runID + "-profile"
	profileArn, err := iamInstanceProfileCreate(ctx, d.iamClient, profileName, iamTagsDefaultWithName(profileName)...)
	if err != nil {
		return "", fmt.Errorf("failed to create instance profile: %w", err)
	}
	log.Info("instance profile created", "profile_name", profileName, "profile_arn", profileArn)

	// Queue instance profile deletion.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting instance profile", "profile_name", profileName)
		return iamInstanceProfileDelete(ctx, d.iamClient, profileName)
	})

	// Add role to instance profile.
	err = iamInstanceProfileAddRole(ctx, d.iamClient, profileName, roleName)
	if err != nil {
		return "", fmt.Errorf("failed to add role to instance profile: %w", err)
	}

	// Queue role removal from instance profile.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("removing role from instance profile", "profile_name", profileName, "role_name", roleName)
		return iamInstanceProfileRemoveRole(ctx, d.iamClient, profileName, roleName)
	})

	log.Info("using created instance profile", "profile_name", profileName)
	return profileName, nil
}

// setupSSHKeys provisions SSH keys, saves them to disk, and sets up cleanup.
func (d *Driver) setupSSHKeys(ctx context.Context, inst *InstanceDeployment, net NetworkDeployment) error {
	log := clog.FromContext(ctx)

	// Provision an ED25519 keypair for SSH.
	var err error
	inst.Keys, inst.KeyID, inst.KeyName, err = d.provisionKeys(ctx)
	if err != nil {
		return err
	}
	log.Info("successfully generated ED25519 keypair")

	// Queue the keypair delete.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting keypair", "id", inst.KeyID)
		return keypairDelete(ctx, d.ec2Client, inst.KeyID)
	})

	// Marshal and write the ED25519 private key to disk.
	inst.KeyPath, err = sshKeyPath(d.runID)
	if err != nil {
		return err
	}
	log.Info("saving ED25519 private key to disk", "path", inst.KeyPath)
	err = sshSaveKey(ctx, inst.Keys.Private, inst.KeyPath)
	if err != nil {
		return err
	}

	// Queue key file cleanup
	d.stack.Push(func(ctx context.Context) error {
		err := os.Remove(inst.KeyPath)
		if err != nil {
			return fmt.Errorf("failed to delete SSH key: %w", err)
		}
		return nil
	})

	// If we're in debug mode, output the SSH command.
	if debugSet() {
		log.Warn("SSH connection args: " + fmt.Sprintf(
			"ssh -i %s -l %s %s",
			inst.KeyPath, d.Exec.User, net.ElasticIP,
		))
	}

	return nil
}

// launchEC2Instance creates the EC2 instance and sets up its cleanup.
func (d *Driver) launchEC2Instance(ctx context.Context, inst *InstanceDeployment, net NetworkDeployment) error {
	log := clog.FromContext(ctx)

	log.Info(
		"launching EC2 instance",
		"instance_type", inst.InstanceType,
		"instance_profile", inst.InstanceProfileName,
		"ami", inst.AMI,
	)

	var err error
	inst.InstanceID, err = instanceCreateWithNetIF(
		ctx,
		d.ec2Client,
		inst.InstanceType, inst.InstanceProfileName, inst.AMI, inst.KeyName, net.InterfaceID, d.Exec.UserData,
		tagName(inst.InstanceName),
	)
	if err != nil {
		return err
	}
	log.Info("EC2 instance launched", "instance_id", inst.InstanceID)

	// Queue the instance destructor.
	d.stack.Push(func(ctx context.Context) error {
		log.Info("deleting EC2 instance", "instance_id", inst.InstanceID)
		if err := instanceDelete(ctx, d.ec2Client, inst.InstanceID); err != nil {
			return err
		}

		// The EC2 instance actually hitting the 'Terminated' state is a hard
		// blocker on removing dependencies further up the chain. So, we need to
		// wait for it to actually be gone (state == 'Terminated').
		//
		// 10-minutes might seem like a long time, but for some reason certain
		// instance types (g5g.xlarge, for example) take a reaaaaaaaal long time to
		// actually hit the 'Terminated' state.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		log.Info("waiting for instance to enter 'terminated' state")
		err = awaitInstanceState(ctx, d.ec2Client, inst.InstanceID, types.InstanceStateNameTerminated)
		if err != nil {
			return fmt.Errorf("encountered error in instance state transition await: %w", err)
		}
		log.Info("instance termination is successful")
		return nil
	})

	return nil
}

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
	keyID, err := keypairImport(ctx, d.ec2Client, keyName, pubKey)
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

// EC2 instances do a lot of things when you launch them, and the EC2 SDK does
// not make it easy to be aware of when those things are "done" and the
// instance is ready.
//
// The "acceptance criteria" for an instance being "ready" for us to use is:
// 1. The instance 'Status' is 'Ok'.
// 2. TCP port 22 (SSH) is reachable.
//
// Sounds simple, right? Except, you can't query the 'Status' until the 'State'
// is 'Running'. So.. For us to know when an instance is "ready", we have to:
// - Monitor the 'State' until it is 'Running' (IMPORTANT: if we try to skip
// this step and just check the 'Status', the 'Status' check will error!).
// - Monitor the 'Status' until it is 'Ok'.
// - Monitor port TCP/22 until it ACKs.
func awaitInstanceLaunch(ctx context.Context, client *ec2.Client, instanceID, IP string, port uint16) error {
	log := clog.FromContext(ctx)
	// Wait for the instance to enter the 'Running' 'State'.
	log.Info("waiting for instance to enter the 'running' state")
	err := awaitInstanceState(ctx, client, instanceID, types.InstanceStateNameRunning)
	if err != nil {
		return err
	}
	log.Info("instance entered the 'running' state")

	// Wait for the instance to enter the 'Ok' 'Status'.
	log.Info("waiting for instance to enter the 'ok' status")
	err = awaitInstanceStatus(ctx, client, instanceID, types.SummaryStatusOk)
	if err != nil {
		return err
	}
	log.Info("instance entered the 'ok' status")

	// Wait for TCP/22 to become reachable.
	log.Info("waiting for instance port tcp/22 to become reachable")
	err = waitTCP(ctx, IP, port)
	if err != nil {
		return err
	}
	log.Info("instance port tcp/22 is reachable, instance is live")

	return nil
}
