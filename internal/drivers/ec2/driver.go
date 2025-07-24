package ec2

import (
	"crypto/rand"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

var _ drivers.Tester = (*Driver)(nil)

func NewDriver(client *ec2.Client) (*Driver, error) {
	runID, err := newRunID()
	if err != nil {
		return nil, err
	}
	return &Driver{
		runID:  runID,
		client: client,
	}, nil
}

func newRunID() (string, error) {
	buf := make([]byte, 8)
	_, err := rand.Reader.Read(buf)
	if err != nil {
		return "", fmt.Errorf("failed to generate run unique identifier")
	}
	return fmt.Sprintf("imagetest-%x", buf), nil
}

type Driver struct {
	// The AMI to launch the instance with
	AMI string

	// The instance architecture
	Arch types.ArchitectureType

	// The desired instance type (ex: 't3.medium').
	//
	// NOTE: If provided, this input supersedes all other configuration (VCPUs,
	// memory, GPUs, etc.)!
	InstanceType types.InstanceType

	// Instance virtual processor configuration
	Proc Proc

	// Instance physical memory configuration
	Memory Memory

	// Instance storage configuration
	Disks []Disk

	// Instance accelerator configuration
	GPU GPU

	// Post-launch provisioning commands to be executed within the EC2 instance.
	Commands Commands

	// runID holds a unique identifier generated for this run.
	runID string

	// client holds a configured ec2 client for use in the 'Setup' and 'Teardown'
	// phases.
	client *ec2.Client

	// stack is a LIFO queue of 'Destructor's which, when called, perform a
	// teardown of a resource created during the 'Setup' method call.
	stack Stack

	//////////////////////////////////////////////////////////////////////////////
	// Here there be dragons.
	//
	// Considering the control flow indirection between 'Setup' and 'Run' /
	// 'Teardown', we're not able to make the whole driver workflow functional.
	// Everything below this line is stuff we might mutate within this driver's
	// lifecycle.

	instance InstanceDeployment
	net      NetworkDeployment

	// The provisioned public IP address of the network interface we attach to the
	// instance.
	// instanceAddr string

	// The ED25519 keypair we create and upload to AWS to use for SSH into the EC2
	// instance.
	// keys ssh.ED25519KeyPair

	// The name we provided as the identifier for the keypair when importing it
	// to AWS.
	// keyName string

	// The ID we received back for the keypair after importing it to AWS.
	// keyID string
}

// GPU describes an input-configurable GPU which will be applied as a constraint
// against the selectable instances
type GPU struct {
	// The desired GPU kind.
	//
	// Default: 'GPUKindNone'.
	Kind GPUKind

	// The number of desired GPUs for the instance.
	//
	// If 'Kind' is set but this is not, it will default to '1'.
	Count uint8

	// The desired GPU driver version
	Driver string
}

// Describes the kinds of GPUs from which we can choose.
type GPUKind = string

const (
	GPUKindNone GPUKind = "none"
	GPUKindM60  GPUKind = "M60"
	GPUKindK80  GPUKind = "K80"
	GPUKindA10G GPUKind = "A10G"
	GPUKindL4   GPUKind = "L4"
	GPUKindL40S GPUKind = "L40S"
	GPUKindV100 GPUKind = "V100"
	GPUKindA100 GPUKind = "A100"
	GPUKindH100 GPUKind = "H100"
	GPUKindH200 GPUKind = "H200"
)

// Commands maps to user-configurable inputs which will be executed on the
// launched EC2 instance.
type Commands struct {
	// If specified, commands will be executed in the context of this user.
	User string

	// If 'Shell' is provided, commands will be run in the shell specified.
	//
	// NOTE: 'Shell', if provided, must be one of: 'sh', 'bash', 'zsh' or 'fish'.
	Shell ssh.Shell

	// Commands which will be run in sequence. If 'Shell' is provided, these will
	// be run within a single SSH and terminal session. If 'Shell' is NOT
	// provided, these will be executed across individual SSH channels.
	Commands []string

	// Env reflects environment variables which will be exported in the SSH
	// session on the EC2 instance.
	Env map[string]string
}
