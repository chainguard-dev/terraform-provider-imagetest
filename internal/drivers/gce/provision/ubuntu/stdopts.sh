# NOTE: There is no shebang here! This code will be piped as stdin to the shell
# chosen by the test author, which may be any of: sh, bash, fish or zsh.
#
# NOTE: Multiple scripts are piped to stdin end-to-end, this means an exit code
# provided here is an exit code to _all_ sequenced preparation commands.
# Considering this, all workflows should be implemented with functions which are
# conditionally called and only exit if a truly test-breaking scenario occurs
# and the entire run should fail.

# >> Overview
#
#   stdopts.sh defines the standard Bash session configuration and some helper
#   functions. These are prefixed to all ec2 driver commands.

# Exit on the first non-zero exit we hit, failures terminate pipelines.
set -eo pipefail

################################################################################
# Cloud-Init

# Wait for cloud-init to complete if it's running (user_data may be installing
# packages/drivers that we depend on). Timeout after 10 minutes.
if command -v cloud-init &>/dev/null; then
  echo "$(date --rfc-3339=s) INFO Waiting for cloud-init to complete (timeout: 10m)..." >&2
  if timeout 600 sudo cloud-init status --wait; then
    echo "$(date --rfc-3339=s) INFO Cloud-init complete." >&2
  else
    echo "$(date --rfc-3339=s) WARN Cloud-init wait timed out or failed, continuing anyway." >&2
  fi
fi

################################################################################
# Logging

# Define some ANSI escape sequences for coloring log levels
readonly YELLOW="\e[33m"
readonly RED="\e[31m"
readonly GRAY="\e[90m"
readonly GREEN="\e[32m"
readonly RESET="\e[0m"

timestamp() {
  echo "$(date --rfc-3339=s)"
}

log () {
  msg="${1}"
  level="${2:-INFO}"
  echo -e "$(timestamp) ${level} ${msg}" >&2
}

debug () {
  log "${1}" "${GRAY}DEBUG${RESET}"
}

info () {
  log "${1}" "${GREEN}INFO${RESET}"
}

warn () {
log "${1}" "${YELLOW}WARN${RESET}"
}

error () {
  log "${1}" "${RED}ERROR${RESET}"
}

fatal () {
  log "${1}" "${RED}FATAL${RESET}"
  exit 1
}

################################################################################
# Retry Behavior

# Maximum number of attempts for all steps retried.
readonly max_attempts=5

# retry retries an arbitrary provided command 'max_attempts' number of times. If
# the final attempt fails, the script exits with an exit code of 1.
retry () {
  local timeout_msg
  for i in $(seq $max_attempts); do
    if "$@"; then
      return 0
    elif [ $i -eq $max_attempts ]; then
      fatal "${timeout_msg}"
    else
      warn "Operation failed, retrying (attempt=${i})."
    fi
  done
}
