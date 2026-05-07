// dockeronhost runs each test in a sandbox container that shares the host's
// docker daemon ("docker out of docker"). The sandbox bind-mounts only
// /var/run/docker.sock from the host. Any container the test launches with
// `docker run` is therefore a sibling of the sandbox on the host daemon, with
// whatever privileges and namespaces the test requests via flags on its own
// `docker run` (--privileged, --cgroupns=host, etc.). This is what unblocks
// workloads such as RKE2 that need direct host cgroup access; in
// docker_in_docker the inner daemon's cgroup namespace is nested and a
// sibling's --cgroupns=host still ties to the dind namespace, not the host's.
//
// The sandbox itself runs unprivileged and in the default cgroup namespace —
// it only invokes the docker CLI, so it has no need for cgroup mounts or
// extra capabilities.
//
// Filesystem state shared between the sandbox and its siblings: tests should
// use named volumes (which the shim auto-labels and the driver reaps on
// teardown) rather than bind mounts of paths created in the sandbox's
// filesystem. The host daemon resolves bind sources against the host's
// filesystem, not the sandbox's, so a `mktemp -d` path created inside the
// sandbox does not exist on the host and a sibling's `-v` for that path
// would mount an empty directory.
//
// The sandbox image is layered the same way as docker_in_docker:
//
//	0: cgr.dev/chainguard/docker-dind (sandbox base, supplies the docker CLI;
//	   the bundled dockerd is dormant since the test image's entrypoint
//	   overrides the base's)
//	1: imagetest-built layer with test content, the entrypoint binary, and envs
//
// Because the daemon is shared with the host, sibling containers the test
// creates outlive the sandbox and must be cleaned up explicitly. The driver
// installs a `docker` shim at /usr/local/bin/docker (ahead of /usr/bin/docker
// on $PATH for the default sandbox image) that auto-injects
// `--label "$IMAGETEST_TEST_LABEL"` into create-like subcommands (run, create,
// network create, volume create). On Teardown the driver force-removes any
// container matching that label. Tests using a custom sandbox image must
// either keep /usr/local/bin earlier on $PATH than /usr/bin or pass the label
// explicitly on every `docker run`.
//
// The /tmp bind mount is load-bearing: any path the test creates inside the
// sandbox (e.g. via `mktemp -d`) must resolve to the same file when a sibling
// container bind-mounts it via `docker run -v ...`. Without sharing /tmp with
// the host, sibling bind mounts would point at empty host directories.
//
// Tradeoffs versus docker_in_docker: cross-test pollution is real (image
// cache, ports, leaked siblings on a shared daemon). Tests that bind fixed
// host ports will collide; prefer randomized ports.
package dockeronhost
