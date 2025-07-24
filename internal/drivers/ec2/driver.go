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

	// The desired EC2 instance type (ex: 't3.medium').
	InstanceType types.InstanceType

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
}

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
