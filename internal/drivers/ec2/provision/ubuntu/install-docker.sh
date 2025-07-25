#!/usr/bin/env bash

# >> Overview
#
#   install-docker.sh is a script which:
#   - Installs the Docker GPG key.
#   - Adds the Docker apt repository.
#   - Installs the core Docker components.
#   - Adds the current (if non-root) user to the 'docker' group.

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

################################################################################
# Install Dependencies

update_package_cache () {
  info 'Updating apt package cache.'
  timeout_msg='Failed to update the apt package cache.' \
    retry sudo apt update -qq
}

install_dependencies () {
  # Install some required prerequisites.
  info 'Installing Docker dependencies ('ca-certificates', 'curl').'
  timeout_msg='Failed Docker dependency install.' \
    retry sudo apt install -y ca-certificates curl -qq
}

################################################################################
# Install Docker Repository GPG Key

install_docker_gpg_key () {
  # Install the Docker repository GPG key.
  info 'Installing the Docker repository GPG key.'
  sudo install \
    -m 0755 \
    -d /etc/apt/keyrings \
    || fatal 'Failed to set keyring file mode.'
  sudo curl \
    -fsSL https://download.docker.com/linux/ubuntu/gpg \
    -o /etc/apt/keyrings/docker.asc \
    || fatal 'Failed to download Docker GPG key.'
  sudo chmod a+r /etc/apt/keyrings/docker.asc \
    || fatal 'Failed to chmod Docker GPG key.'
}

################################################################################
# Add the Docker APT Repository

add_docker_repo () {
  info 'Adding the Docker apt repository.'
  type='deb'
  arch=$(dpkg --print-architecture)
  signer='/etc/apt/keyrings/docker.asc'
  url='https://download.docker.com/linux/ubuntu'
  distro="$(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")"
  comp='stable'
  echo "${type} [arch=${arch} signed-by=${signer}] ${url} ${distro} ${comp}" \
    | sudo tee /etc/apt/sources.list.d/docker.list
}

################################################################################
# Install Docker

install_docker () {
  timeout_msg='Failed to install Docker.' \
  retry sudo apt install -y \
       docker-ce \
       docker-ce-cli \
       containerd.io \
       docker-buildx-plugin -qq
}

################################################################################
# Add User to Docker Group

add_user_to_docker_group () {
  # Add the user to the 'docker' group.
  if [ "${USER}" != 'root' ]; then
    info "Adding [${USER}] to the 'docker' group."
    sudo usermod -aG docker "${USER}" \
      || fatal "Failed to add [${USER}] to the 'docker' group."
  else
    info "User is [root], skipping 'docker' group add."
  fi
}

################################################################################
# Zhu Li, do the thing!

update_package_cache
install_dependencies
install_docker_gpg_key
add_docker_repo
update_package_cache
install_docker
add_user_to_docker_group
