# Overview

Directory `provision` contains files related to the provisioning of EC2
instances.

# Notes, Discussion

- Currently, this is simply a single Bash script which installs and configures
some basic Docker details on an Ubuntu host. This is achieved by `go:embed`ing
`provision/ubuntu/install-docker.sh` in `commands.go` and running that via SSH
on each EC2 instance's construction.
- A more capable future state could be transitioning this to an `embed.FS`
rather than a `string` and using the FS as a namespace to lookup the appropriate
scripts to run based on actual executing environment details (ex: `Alpine`).
