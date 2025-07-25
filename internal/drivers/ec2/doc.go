// ec2 provides a standard imagetest 'drivers.Tester' implementation for
// performing image tests on AWS EC2 instances.
//
// # Overview
//
// This driver's lifecycle follows the standard 'drivers.Tester' states:
// - Setup
// - Run
// - Teardown
//
// # Phase: Setup
//
// During the 'Setup' phase, the driver will:
// - Provision a new VPC, 'vpc'.
// - Provision a new VPC subnet, 'vpc-sn', under parent 'vpc'.
// - Provision an Internet Gateway, 'igw', and attach it to 'vpc'.
// - Lookup 'vpc's default route table, and add to it a default route
// (dest: 0.0.0.0/0) to 'igw'.
// - Identify the public IP address of the local device (where the Terraform
// provider is running), lookup the default security group for 'vpc', and add an
// inbound 'ALLOW' rule for port '22' (SSH) from only this IP.
// - Allocate an Elastic IP, 'eip'.
// - Create an Elastic Network Interface, 'eni', in 'vpc-sn'.
// - Attach 'eip', to 'eni'.
// - Generate an ED25519 keypair, 'kp'.
// - Import 'kp' to AWS.
// - Launch EC2 instance, 'instance', with the instance type and AMI specified
// by user input, and 'eni' attached.
// - Wait for TCP port 22 (SSH) to become reachable on 'eip'.
// - Connect to 'instance' via SSH, install Docker and add the user (if
// non-root) to the 'docker' group.
//
// ## Why Allocate the Elastic IP and Attach it to an ENI Manually?
//
// This is _intentional_! There does exist a simple bool field
// ('AssociatePublicIpAddress') on the struct passed to the method which
// launches the instance ('client.RunInstances') that, when set, means the
// instance automatically gets a public IP. However:
// 1. We need to add security group rules for this IP! This would've made it
// impossible to separate cleanly network+instance construction.
// 2. There was a lot of logical complexity added, considering the struct
// returned from 'client.RunInstances' does _not_ contain the public IP! It must
// be queried after the fact.
// 3. It worsened clarity around the teardown process. This entire process
// defines the 'destructor' for that resource immediately after it's created.
// Using an automatically provisioned address would've meant we have to define
// 'destructor's far from their 'constructor's otherwise a resource dependency
// conflict.
//
// # Phase: Run
//
// During the 'Run' phase, this driver will:
// - Marshal the ED25519 private key (generated in 'Setup') to the PEM-encoded
// OpenSSH format.
// - Write the marshaled private key to disk (required for SSH access to the
// instance via the Docker client).
// - Construct a Docker client over SSH to the remote EC2 instance.
// - Resolve auth (ex: chainctl cred helper) for the registry defined by the
// provided 'name.Reference' parameter.
// - Perform an authenticated (if necessary) pull of the Docker image on the
// remote instance.
// - Run the image, observing stdout+stderr and producing an error if an
// unexpected (non-zero) exit code is found.
//
// # Phase: Teardown
//
// The benefit to this package's implementation(discussed above) is that all
// resources can be torn down in the exact reverse order they were created.
// Considering this, all resources have a 'destructor' 'Push'ed onto the 'stack'
// as they are constructed and in the 'Teardown' phase this stack is simply
// destroyed back-to-front.
//
// # Notes
//
// The Go files prefixed 'ec2_' contain functions which form a facade over the
// AWS Go SDK v2's own capabilities. The primary reason for this is:
// - Nil checks. In the AWS Go SDK everything is a pointer: struct fields,
// function args, everything. In building this package I segfaulted so many
// times trying to dereference nil fields on structs returned from _successful_
// calls that it became clear some defensive programming was necessary. All
// facade functions nil check across the board.
// - Well-known errors. The AWS SDK doesn't provide much utility around
// handling returned errors. All facade functions return well-known
// ('errors.Is'able) errors.
// - If you read this far into the notes, let me know on Slack! You're probably
// the only one.
// - Provide the batteries. The AWS SDK is engineered to serve every possible
// use case engineers could throw at it. This means 80% of it we will never use.
// All facade functions chop down inputs to the essential values and standardize
// on returning the field which would constitute a handle to that resource,
// rather than 500-1000-byte struct pointers which contain date of birth, car
// insurance information, etc.
package ec2

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
)

// For more reading on abstracted inputs, see 'ec2.RunInstancesInput'.
var (
	_ ec2.RunInstancesInput
	_ drivers.Tester = (*Driver)(nil)
)
