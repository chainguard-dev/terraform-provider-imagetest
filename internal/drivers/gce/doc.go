// gce provides a standard imagetest 'drivers.Tester' implementation for
// performing image tests on Google Compute Engine instances.
//
// # Overview
//
// This driver runs container tests on a GCE instance using Docker over SSH.
// It requires a pre-provisioned VPC network with internet access.
//
// Lifecycle: Setup -> Run -> Teardown
//
// # Phase: Setup
//
// The driver creates the following resources in order:
//  1. SSH Key - ED25519 key generated locally, public key added via instance metadata
//  2. Firewall Rule - allows SSH from the caller's public IP only (target tag)
//  3. Instance - launched with external IP, startup-script metadata
//
// After launch, the driver waits for:
//   - Instance to reach "RUNNING" state
//   - SSH port to become reachable
//   - Cloud-init to complete (if startup_script provided)
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
//  2. Instance deleted
//  3. Firewall rule deleted
//  4. SSH key temp file removed
//
// All resources are labeled with imagetest-expires (2h from creation) for
// fallback cleanup if teardown fails.
//
// # GPU Support
//
// GCE attaches GPUs separately from the machine type. Set AcceleratorType
// and AcceleratorCount in the config. The driver automatically sets
// OnHostMaintenance to TERMINATE (required for GPU instances).
//
// # Existing Instance Mode
//
// For faster iteration, the driver can reuse an existing instance:
//
//	existing_instance = {
//	  ip      = "1.2.3.4"
//	  ssh_key = "/path/to/key.pem"
//	}
//
// In this mode, no GCE resources are created or destroyed.
package gce

import "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"

var _ drivers.Tester = (*driver)(nil)
