#!/usr/bin/env bash

# >> Overview
#
#   stdopts.sh defines the standard Bash session configuration which should
#   apply to all sessions.

# Exit on the first non-zero exit we hit, failures terminate pipelines.
set -eo pipefail

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

# Maximum number of attempts for all steps retried.
readonly max_attempts=5

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
