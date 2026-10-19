// Package ssh provides an imagetest 'drivers.Tester' implementation that runs
// image tests on a host that already exists, reached over SSH.
//
// # Overview
//
// The driver is for a held machine, such as a GPU host that no cloud offers
// on demand. It creates no infrastructure and builds no cloud clients: a
// broken local cloud profile cannot fail it. The host runs Docker, and the
// driver talks to that daemon over an SSH tunnel.
//
// Lifecycle: Setup -> Run -> Teardown
//
// # Phase: Setup
//
//  1. Reads the private key and the known_hosts file. known_hosts is
//     required: the host is reached by address, often over the internet,
//     and image pulls send the caller's registry credentials over the
//     tunnel. With Config.PullECRWithHostCredentials, every image outside
//     a private ECR registry still does. Every SSH connection, the Docker
//     tunnel and the Go client alike, accepts only the keys the file
//     records for the host and fails on a mismatch. The file is in OpenSSH
//     known_hosts format, as written by `ssh-keyscan -p <port> -t ed25519
//     <host>`; the entry must name the host as given, as [host]:port when
//     the port is not 22. A host rebuild changes the key, so a rebuild also
//     updates the pinned file.
//  2. Opens one SSH connection, so a wrong key or a wrong pin fails here
//     with a clear error. The error is a *HostUnreachableError whose
//     message starts with HostUnreachablePrefix, so a caller can tell a
//     host it never reached from a test that failed.
//  3. With lock_path set, takes an exclusive flock on that file in an SSH
//     session that stays open until Teardown, so runs that share the host
//     run one at a time. The wait is bounded by the setup timeout and is
//     recorded as the span event ssh.lock.acquired. Then, since no other
//     run can have live containers while the lock is held, removes every
//     container on the host that carries an imagetest label from an
//     earlier run: a guest that died mid-run never reached its own
//     teardown.
//  4. Runs setup_commands, if any, in one session.
//  5. Logs GPU and NVIDIA driver information if nvidia-smi is present.
//     This is non-fatal.
//  6. Pulls the images under test (Config.PrePullImages, filled by the
//     provider from the resource's images map) into the host's Docker
//     daemon. By default each pull carries the caller's registry
//     credentials for that one request; the host never logs in and never
//     stores a token. With Config.PullECRWithHostCredentials, an image in
//     a private ECR registry is instead pulled by the docker CLI on the
//     host with the host's own AWS credentials through
//     docker-credential-ecr-login, which caches the ECR token under the
//     host user's home. Images are
//     pulled as given, by digest, never with a platform override: the
//     daemon selects its own platform from a multi-arch index.
//
// # Phase: Run
//
//  1. Connects to Docker on the host via the SSH tunnel
//  2. Pulls the test image, the same way as in Setup step 5
//  3. Runs the container under a unique name, streaming logs and waiting
//     for exit. The container carries the run's label (imagetest:run) and
//     the environment variables IMAGETEST_RUN_LABEL and IMAGETEST_RUN
//     name it, so a test that starts containers through the Docker socket
//     can label them with the run.
//
// # Phase: Teardown
//
// The host is shared and persistent, so teardown touches only containers.
// In order: the test containers this run created, then every container on
// the host carrying the run's label (a sibling the test started dies with
// the run even when its exit trap never fired), then the host lock.
// Pulled images stay in the daemon's cache for later runs; pruning them is
// the host operator's job. With IMAGETEST_SKIP_TEARDOWN set the containers
// are left in place and the lock is released when the provider exits.
//
// # Shared code
//
// The SSH, Docker and container code is internal/sshhost, shared with the
// ec2 driver, whose existing_instance mode this driver replaces.
package ssh

import "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"

var (
	_ drivers.Tester    = (*driver)(nil)
	_ drivers.Diagnoser = (*driver)(nil)
)
