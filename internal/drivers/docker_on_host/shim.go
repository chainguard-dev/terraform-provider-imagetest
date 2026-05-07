package dockeronhost

// shimPath is where the driver installs a docker shim inside the sandbox.
// /usr/local/bin precedes /usr/bin on PATH in the standard sandbox image, so
// invocations of `docker` resolve to the shim and the real binary at
// /usr/bin/docker is reached only via exec.
const shimPath = "/usr/local/bin/docker"

// dockerShim wraps the real docker CLI and auto-injects
// `--label "$IMAGETEST_TEST_LABEL"` into create-like subcommands so the driver
// can reap siblings on Teardown without tests having to remember the flag.
//
// Pass-through (no injection) when:
//   - $IMAGETEST_TEST_LABEL is empty (lets users opt out by unsetting it),
//   - the subcommand is not run/create/network create/volume create,
//   - or the first argument starts with `-` (e.g. `docker --version`,
//     `docker -H tcp://...`); we don't try to parse global flags.
const dockerShim = `#!/bin/sh
set -e

REAL=/usr/bin/docker

if [ -z "${IMAGETEST_TEST_LABEL:-}" ] || [ $# -eq 0 ]; then
  exec "$REAL" "$@"
fi

case "$1" in
  -*)
    exec "$REAL" "$@"
    ;;
  run|create)
    cmd=$1
    shift
    exec "$REAL" "$cmd" --label "$IMAGETEST_TEST_LABEL" "$@"
    ;;
  network|volume)
    if [ "${2:-}" = "create" ]; then
      cmd=$1
      shift 2
      exec "$REAL" "$cmd" create --label "$IMAGETEST_TEST_LABEL" "$@"
    fi
    exec "$REAL" "$@"
    ;;
  *)
    exec "$REAL" "$@"
    ;;
esac
`
