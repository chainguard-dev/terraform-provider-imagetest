#!/bin/sh
set -eux

# The driver runs the sandbox with --cgroupns=host and bind-mounts
# /sys/fs/cgroup. Spawning a sibling with --cgroupns=host should let it read
# the host's root cgroup.procs file. This is the operation that fails inside
# dind ("operation not supported") because dind nests its own cgroup namespace,
# and is the entire reason the docker_on_host driver exists.
docker run --rm --cgroupns=host \
  cgr.dev/chainguard/busybox:latest \
  sh -c 'cat /sys/fs/cgroup/cgroup.procs > /dev/null'

echo "host-cgroup-read-ok"
