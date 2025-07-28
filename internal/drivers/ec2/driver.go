package ec2

import (
	"crypto/rand"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
)

// NewDriver constructs a 'Driver'.
//
// NOTE: Driver _must_ be constructed via this function as the 'ec2.Client' is
// required and _not_ exported.
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

// newRunID generates an 8-byte run identifier appended to the constant prefix
// 'imagetest-' as base-16 for use as a unique run identifier.
func newRunID() (string, error) {
	buf := make([]byte, 8)
	_, err := rand.Reader.Read(buf)
	if err != nil {
		return "", fmt.Errorf("failed to generate run unique identifier")
	}
	return fmt.Sprintf("imagetest-%x", buf), nil
}

type Driver struct {
	// The targeted EC2 region to deploy resources to.
	Region string `tfsdk:"region"`

	// The AMI to launch the instance with
	AMI string `tfsdk:"ami"`

	// The desired EC2 instance type (ex: 't3.medium').
	InstanceType types.InstanceType `tfsdk:"instance_type"`

	// Post-launch provisioning commands to be executed within the EC2 instance.
	Exec Exec `tfsdk:"commands"`

	// User-provided volume mounts from the EC2 instance to the container.
	VolumeMounts []string `tfsdk:"volume_mounts"`

	// User-provided device mounts from the EC2 instance to the container.
	DeviceMounts []string `tfask:"device_mounts"`

	// runID holds a unique identifier generated for this run.
	runID string

	// client holds a configured EC2 client for use in the 'Setup' and 'Teardown'
	// phases.
	client *ec2.Client

	// stack is a LIFO queue of 'destructor's which, when called, perform a
	// teardown of a resource created during the 'Setup' method call.
	stack stack

	//////////////////////////////////////////////////////////////////////////////
	// Here there be dragons.
	//
	// Considering the control flow indirection between 'Setup' and 'Run' /
	// 'Teardown', we're not able to make the whole driver workflow black-box
	// functional.
	//
	// Everything below this line is stuff we might mutate within this driver's
	// lifecycle.
	instance InstanceDeployment
	net      NetworkDeployment
}

// Exec maps to user-configurable inputs which will be executed on the
// launched EC2 instance.
type Exec struct {
	// If specified, commands will be executed in the context of this user.
	User string `tfsdk:"user"`

	// If 'Shell' is provided, commands will be run in the shell specified.
	//
	// NOTE: 'Shell', if provided, must be one of: 'sh', 'bash', 'zsh' or 'fish'.
	Shell ssh.Shell `tfsdk:"shell"`

	// Commands which will be run in sequence. If 'Shell' is provided, these will
	// be run within a single SSH and terminal session. If 'Shell' is NOT
	// provided, these will be executed across individual SSH channels.
	Commands []string `tfsdk:"commands"`

	// Env reflects environment variables which will be exported in the SSH
	// session on the EC2 instance.
	Env map[string]string `tfsdk:"env"`
}
