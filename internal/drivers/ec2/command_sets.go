package ec2

// command_sets.go defines some well-known command sequences for easy addition
// to the 'Driver' and bootstrap within the created EC2 instance.

// Performs an install of the Docker CLI, containerd runtime and the buildx
// plugin.
//
// This mirrors the steps defined on the Docker website for Debian hosts:
// https://docs.docker.com/engine/install/debian/
var cmdSetInstallDocker = []string{
	// Update apt packages list.
	"sudo apt update",
	// Install some required prerequisites.
	"sudo apt install -y ca-certificates curl",
	// Install the Docker repository GPG key.
	"sudo install -m 0755 -d /etc/apt/keyrings",
	"sudo curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc",
	"sudo chmod a+r /etc/apt/keyrings/docker.asc",
	// Add the APT repository.
	`echo \
"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian \
$(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
sudo tee /etc/apt/sources.list.d/docker.list > /dev/null`,
	// Update apt package list.
	"sudo apt-get update",
	// Install core Docker components.
	"sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin",
}
