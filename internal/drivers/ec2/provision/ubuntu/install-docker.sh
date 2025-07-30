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
#   install-docker.sh is a script which:
#   - Installs the Docker GPG key.
#   - Adds the Docker apt repository.
#   - Installs the core Docker components.
#   - Adds the current (if non-root) user to the 'docker' group.

################################################################################
# Install Dependencies

update_package_cache () {
  info 'Updating apt package cache.'
  timeout_msg='Failed to update the apt package cache.' \
    retry sudo apt update -qqq
}

install_dependencies () {
  # Install some required prerequisites.
  info 'Installing Docker dependencies ('ca-certificates', 'curl').'
  timeout_msg='Failed Docker dependency install.' \
    retry sudo apt install -qqq -y ca-certificates curl
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
  # In local testing, about ~50% of the time when the EC2 instance launched some
  # Python process held the apt lock, hence the retries.
  timeout_msg='Failed to install Docker.' \
    retry sudo apt install -qqq -y docker-ce
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
