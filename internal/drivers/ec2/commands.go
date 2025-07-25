package ec2

// commands.go defines some well-known command sequences for easy addition
// to the 'Driver' and provisioning within the created EC2 instance.

import _ "embed"

// These are the default commands run against all provisioned hosts.
//
// NOTE: Currently, we only plan to use Ubuntu images. Should this change a more
// granular slicing will be required across this implementation.
var cmdSetDefault = []string{
	cmdStdOpts,
	cmdUbuntuInstallDocker,
}

// Contains standard shell configuration which should apply to all sessions.
//
//go:embed provision/stdopts.sh
var cmdStdOpts string

// Performs an install of the Docker CLI, containerd runtime and the buildx
// plugin. Also adds the current user (if not 'root') to the 'docker' group for
// non-sudo container interaction.
//
// This mirrors the steps defined on the Docker website for Debian hosts:
// https://docs.docker.com/engine/install/debian/
//
//go:embed provision/ubuntu/install-docker.sh
var cmdUbuntuInstallDocker string
