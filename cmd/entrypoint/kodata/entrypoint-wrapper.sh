#!/bin/sh
set -eu

info() {
  printf '%s [INFO] %s\n' "$(date "+%Y-%m-%dT%H:%M:%S")" "$1" >&2
}

warn() {
  printf '%s [WARN] %s\n' "$(date "+%Y-%m-%dT%H:%M:%S")" "$1" >&2
}

error() {
  printf '%s [ERROR] %s\n' "$(date "+%Y-%m-%dT%H:%M:%S")" "$1" >&2
}

# Print usage and exit with error
usage() {
  error "Usage: $0 <test-script-path>"
  error "Environment variables:"
  error "  IMAGETEST_DRIVER: Type of test environment (docker_in_docker, k3s_in_docker, eks_with_eksctl)"
  exit 1
}

# Validate that a test script exists and is executable.
# This function is used by all drivers to ensure consistent validation.
# Arguments:
#   $1: Path to the test script
validate_cmd() {
  cmdarg="$1"

  # Only try to do validations if cmdarg is a file (presumably a script of sorts)
  if [ -f "$cmdarg" ]; then
    if [ ! -x "$cmdarg" ]; then
      chmod +x "$cmdarg" || warn "Failed to make script executable"
    fi
  fi
}

init_registry_proxy() {
  # Check if IMAGETEST_REGISTRY is set and contains "localhost"
  if [ -z "${IMAGETEST_REGISTRY:-}" ] || ! echo "$IMAGETEST_REGISTRY" | grep -q "localhost"; then
    return
  fi

  info "Local registry detected, attempting to start registry proxy"

  # Extract port number following "localhost:"
  port=$(echo "$IMAGETEST_REGISTRY" | sed -n 's/.*localhost:\([0-9]\+\).*/\1/p')

  if [ -n "$port" ]; then
    info "Detected localhost and port ${port}, starting registry proxy"

    # Start a non-forked socat process to proxy localhost to dockers magic dns
    setsid socat -d -lf /tmp/local-registry-proxy.log TCP-LISTEN:"${port}",fork,reuseaddr TCP:host.docker.internal:"${port}" </dev/null >/dev/null 2>&1 &
  fi
}

# Initialize and manage a Docker-in-Docker environment.
# This function handles the Docker daemon startup and monitoring.
# Arguments:
#   $1: Path to the test script (already validated)
init_docker_in_docker() {
  cmd="$1"
  timeout=30
  log_dir="/var/log/docker"

  # Set up logging directory
  mkdir -p "$log_dir"

  # Start Docker daemon in background, capturing logs
  /usr/bin/dockerd-entrypoint.sh dockerd >"$log_dir/dockerd.log" 2>&1 &

  # Wait for Docker to be ready
  info "Waiting for Docker daemon to be ready..."
  while [ "$timeout" -gt 0 ]; do
    if docker version >/dev/null 2>&1; then
      info "Docker daemon is ready"
      break
    fi
    timeout=$((timeout - 1))
    info "Waiting... ($timeout seconds remaining)"
    sleep 1
  done

  if [ "$timeout" -le 0 ]; then
    error "Docker daemon failed to start"
    exit 1
  fi

  # Maybe start a registry proxy
  init_registry_proxy

  exec "$cmd"
}

# Initialize and manage a K3s-in-Docker environment.
# Arguments:
#   $1: Path to the test script (already validated)
init_k3s_in_docker() {
  cmd="$1"

  # Ensure required environment variables are set
  if [ -z "${POD_NAME-}" ] || [ -z "${POD_NAMESPACE-}" ]; then
    error "POD_NAME and POD_NAMESPACE environment variables must be set"
    exit 1
  fi

  info "Waiting for pod ${POD_NAME} to be ready..."
  if ! kubectl wait --for=condition=Ready=true pod/"${POD_NAME}" -n "${POD_NAMESPACE}" --timeout=60s; then
    error "Pod ${POD_NAME} failed to become ready"
    exit 1
  fi

  exec "$cmd"
}

# Initialize and manage an EKS-with-Eksctl environment.
# Arguments:
#   $1: Path to the test script (already validated)
init_eks_with_eksctl() {
  cmd="$1"

  # Ensure required environment variables are set
  if [ -z "${POD_NAME-}" ] || [ -z "${POD_NAMESPACE-}" ]; then
    error "POD_NAME and POD_NAMESPACE environment variables must be set"
    exit 1
  fi

  info "Waiting for pod ${POD_NAME} to be ready..."
  if ! kubectl wait --for=condition=Ready=true pod/"${POD_NAME}" -n "${POD_NAMESPACE}" --timeout=60s; then
    error "Pod ${POD_NAME} failed to become ready"
    exit 1
  fi

  exec "$cmd"
}

# Validate command-line arguments
if [ $# -ne 1 ]; then
  usage
fi

cmd="$1"

# Make sure IMAGETEST_DRIVER is set
if [ -z "${IMAGETEST_DRIVER-}" ]; then
  error "IMAGETEST_DRIVER environment variable not set"
  usage
fi

# Validate the test script first, regardless of driver
validate_cmd "$cmd"

# Initialize the appropriate driver
case "$IMAGETEST_DRIVER" in
docker_in_docker)
  init_docker_in_docker "$cmd"
  ;;
k3s_in_docker)
  init_k3s_in_docker "$cmd"
  ;;
eks_with_eksctl)
  init_eks_with_eksctl "$cmd"
  ;;
*)
  error "Unknown driver '$IMAGETEST_DRIVER'"
  usage
  ;;
esac
