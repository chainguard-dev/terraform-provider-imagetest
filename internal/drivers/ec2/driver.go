package ec2

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
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
	Region string

	// The AMI to launch the instance with
	AMI string

	// The desired EC2 instance type (ex: 't3.medium').
	InstanceType types.InstanceType

	// An IP address of the instance.
	//
	// NOTE: If this is provided, fields 'AMI' + 'InstanceType' will be ignored
	// and no AWS resources will be created! This is primarily intended for local
	// dev.
	InstanceIP string

	// The IAM instance profile name to be associated with.
	InstanceProfileName string

	// Post-launch provisioning commands to be executed within the EC2 instance.
	Exec Exec

	// User-provided volume mounts from the EC2 instance to the container.
	VolumeMounts []string

	// User-provided device mounts from the EC2 instance to the container.
	DeviceMounts []string

	// Indicates all GPUs should be passed through to the container.
	//
	// This is the equivalent of the '--gpus all' command-line flag.
	MountAllGPUs bool

	// Instance holds all configuration information relating to the EC2 Instance
	// and associated SSH keys.
	Instance InstanceDeployment

	// Network holds all configuration information relating to the VPC and related
	// networking components.
	Network NetworkDeployment

	// skipInit means an instance IP was provided against which we will perform
	// our tests. All EC2 resource creation will be skipped.
	SkipCreate bool

	// SkipTeardown hopefully is self-explanatory!
	SkipTeardown bool

	// runID holds a unique identifier generated for this run.
	runID string

	// client holds a configured EC2 client for use in the 'Setup' and 'Teardown'
	// phases.
	client *ec2.Client

	// stack is a LIFO queue of 'destructor's which, when called, perform a
	// teardown of a resource created during the 'Setup' method call.
	stack stack
}

func (d *Driver) deviceMappings(ctx context.Context) []container.DeviceMapping {
	if len(d.DeviceMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)

	mounts := make([]container.DeviceMapping, len(d.DeviceMounts))
	for i, dev := range d.DeviceMounts {
		// Split the local+in-container paths.
		if local, incontainer, ok := strings.Cut(dev, ":"); ok {
			log.Debug("adding device mount", "from", local, "to", incontainer)
			mounts[i] = container.DeviceMapping{
				PathOnHost:      local,
				PathInContainer: incontainer,
			}
		} else {
			log.Error("found ill-formed device mapping (device mounts must "+
				"be in the form of '/host/path:/container/path')", "mapping", dev)
		}
	}

	return mounts
}

func (d *Driver) mounts(ctx context.Context) []mount.Mount {
	if len(d.VolumeMounts) == 0 {
		return nil
	}

	log := clog.FromContext(ctx)

	mounts := make([]mount.Mount, len(d.VolumeMounts))
	for i, volume := range d.VolumeMounts {
		// Split the local+in-container paths.
		if local, incontainer, ok := strings.Cut(volume, ":"); ok {
			log.Debug("adding volume mount", "from", local, "to", incontainer)
			mounts[i] = mount.Mount{
				Type:   mount.TypeBind,
				Source: local,
				Target: incontainer,
			}
		} else {
			log.Error("found ill-formed bind mount (bind mounts must "+
				"be in the form of '/host/path:/container/path')", "mapping", volume)
		}
	}

	return mounts
}

// Exec maps to user-configurable inputs and pre-test instance preparation
// constraints which will be executed on the launched EC2 instance.
type Exec struct {
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

	// UserData contains CloudInit userdata.
	UserData string
}
