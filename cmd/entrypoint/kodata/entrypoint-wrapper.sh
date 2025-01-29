#!/bin/sh
set -eu

# Print usage and exit with error
usage() {
  echo "Usage: $0 <test-script-path>"
  echo "Environment variables:"
  echo "  IMAGETEST_DRIVER: Type of test environment (docker_in_docker, k3s_in_docker)"
  exit 1
}

# Validate that a test script exists and is executable.
# This function is used by all drivers to ensure consistent validation.
# Arguments:
#   $1: Path to the test script
validate_test_script() {
  script_path="$1"

  if [ ! -f "$script_path" ]; then
    echo "Error: Test script '$script_path' does not exist"
    exit 1
  fi

  if [ ! -x "$script_path" ]; then
    echo "Warning: Test script '$script_path' is not executable, attempting to set execute permission"
    if ! chmod +x "$script_path"; then
      echo "Error: Failed to make test script executable"
      exit 1
    fi
    echo "Successfully made test script executable"
  fi
}

# Initialize and manage a Docker-in-Docker environment.
# This function handles the Docker daemon startup and monitoring.
# Arguments:
#   $1: Path to the test script (already validated)
init_docker_in_docker() {
  test_script="$1"
  timeout=30
  log_dir="/var/log/docker"

  # Set up logging directory
  mkdir -p "$log_dir"

  # Start Docker daemon in background, capturing logs
  /usr/bin/dockerd-entrypoint.sh dockerd >"$log_dir/dockerd.log" 2>&1 &

  # Wait for Docker to be ready
  echo "Waiting for Docker daemon to be ready..."
  while [ "$timeout" -gt 0 ]; do
    if docker version >/dev/null 2>&1; then
      echo "Docker daemon is ready"
      break
    fi
    timeout=$((timeout - 1))
    echo "Waiting... ($timeout seconds remaining)"
    sleep 1
  done

  if [ "$timeout" -le 0 ]; then
    echo "Error: Docker daemon failed to start"
    exit 1
  fi

  # Execute test script with strict shell options
  exec /bin/sh -euxc ". $test_script"
}

# Initialize and manage a K3s-in-Docker environment.
# Arguments:
#   $1: Path to the test script (already validated)
init_k3s_in_docker() {
  test_script="$1"

  # Ensure required environment variables are set
  if [ -z "${POD_NAME-}" ] || [ -z "${POD_NAMESPACE-}" ]; then
    echo "Error: POD_NAME and POD_NAMESPACE environment variables must be set"
    exit 1
  fi

  echo "Waiting for pod ${POD_NAME} to be ready..."
  if ! kubectl wait --for=condition=Ready=true pod/${POD_NAME} -n "${POD_NAMESPACE}" --timeout=60s; then
    echo "Error: Pod ${POD_NAME} failed to become ready"
    exit 1
  fi

  # Execute test script with strict shell options
  exec /bin/sh -euxc ". $test_script"
}

# Validate command-line arguments
if [ $# -ne 1 ]; then
  usage
fi

test_script="$1"

# Make sure IMAGETEST_DRIVER is set
if [ -z "${IMAGETEST_DRIVER-}" ]; then
  echo "Error: IMAGETEST_DRIVER environment variable not set"
  usage
fi

# Validate the test script first, regardless of driver
validate_test_script "$test_script"

# Initialize the appropriate driver
case "$IMAGETEST_DRIVER" in
docker_in_docker)
  init_docker_in_docker "$test_script"
  ;;
k3s_in_docker)
  init_k3s_in_docker "$test_script"
  ;;
*)
  echo "Error: Unknown driver '$IMAGETEST_DRIVER'"
  usage
  ;;
esac
