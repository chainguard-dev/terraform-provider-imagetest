// ec2 provides a standard imagetest 'drivers.Tester' implementation for
// performing image tests on AWS EC2 instances.
//
// # Overview
//
// This driver runs container tests on an EC2 instance using Docker over SSH.
// It requires a pre-provisioned VPC with an internet gateway and public routing.
//
// Lifecycle: Setup -> Run -> Teardown
//
// # Phase: Setup
//
// The driver creates the following resources in order:
//  1. Subnet - a /24 subnet within the provided VPC (random CIDR, retry on conflict)
//  2. Security Group - allows SSH (port 22) from the caller's public IP only
//  3. Key Pair - ED25519 key generated locally, public key imported to AWS
//  4. Instance Profile - IAM role with ECR read-only access (unless provided)
//  5. Instance - launched with auto-assigned public IP
//
// After launch, the driver waits for:
//   - Instance to reach "running" state
//   - Instance status checks to pass
//   - SSH port to become reachable
//   - Cloud-init to complete (if user_data provided)
//
// # Phase: Run
//
// The driver:
//  1. Connects to Docker on the instance via SSH tunnel
//  2. Pulls the test image (with registry auth from default keychain)
//  3. Runs the container, streaming logs and waiting for exit
//
// # Phase: Teardown
//
// Resources are torn down in reverse creation order using a stack:
//  1. Container removed
//  2. Instance terminated (waits for full termination so ENI is released)
//  3. Instance profile deleted (role detached, profile deleted, role deleted)
//  4. Key pair deleted (AWS key and local file)
//  5. Security group deleted
//  6. Subnet deleted
//
// All resources are tagged with imagetest:expires (2h from creation) for
// aws-nuke fallback cleanup if teardown fails.
//
// # Shared host code
//
// Everything after the instance is reachable (SSH with an optional pinned
// host key, Docker over SSH, image pulls with the caller's registry
// credentials, the test container and its removal, setup commands, GPU info
// and on_failure diagnostics) lives in internal/sshhost and is shared with
// the ssh driver.
//
// # Existing Instance Mode (deprecated)
//
// This mode is deprecated in favor of the ssh driver (internal/drivers/ssh),
// which runs the same host code on a host that already exists without
// building AWS clients and requires a pinned host key. It stays for one
// release and is then removed.
//
//	existing_instance = {
//	  ip          = "1.2.3.4"
//	  ssh_key     = "/path/to/key.pem"
//	  known_hosts = "/path/to/known_hosts" # optional
//	}
//
// In this mode, no AWS resources are created or destroyed. The driver still
// builds its EC2 and IAM clients from the ambient AWS configuration, but
// never calls them. With known_hosts set, every SSH connection verifies the
// instance's host key against that file; without it, host keys are not
// checked. Setup pulls the images under test (Config.PrePullImages) into the
// instance's Docker daemon before any test container starts, and Teardown
// removes the test containers; the instance itself is left untouched.
package ec2

import "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"

var (
	_ drivers.Tester    = (*driver)(nil)
	_ drivers.Diagnoser = (*driver)(nil)
)
