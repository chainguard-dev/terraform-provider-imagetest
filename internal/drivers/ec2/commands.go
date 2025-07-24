package ec2

// commands.go defines some well-known command sequences for easy addition
// to the 'Driver' and bootstrap within the created EC2 instance.

import _ "embed"

// Performs an install of the Docker CLI, containerd runtime and the buildx
// plugin.
//
// This mirrors the steps defined on the Docker website for Debian hosts:
// https://docs.docker.com/engine/install/debian/

//go:embed provision/ubuntu/install-docker.sh
var cmdSetInstallDockerUbuntu string
