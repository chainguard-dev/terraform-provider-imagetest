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
#   install-nvidia-container-toolkit.sh is a script which:
#   - Checks for and installs, if not found, lspci (pciutils).
#   - Evaluate the presence of an nVIDIA GPU via 'lspci'.
#   - IF an nVIDIA GPU is found, the nVIDIA container toolkit is installed.

install_nvidia_container_toolkit () {
  # Download, dearmor and save the nVIDIA GPG key.
  info 'Fetching the nVIDIA GPG key.'
  curl -fsSL 'https://nvidia.github.io/libnvidia-container/gpgkey' \
    | sudo gpg --dearmor -o '/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg'

  # Add the nVIDIA container toolkit apt source list.
  info 'Adding the nVIDIA apt source list.'
  curl -s -L 'https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list' \
    | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
    | sudo tee '/etc/apt/sources.list.d/nvidia-container-toolkit.list'

  # Install the nVIDIA container toolkit and all related libraries.
  info 'Installing the nVIDIA container toolkit.'
  sudo apt update -qqq
  sudo apt install -y -qqq nvidia-container-toolkit

  # Restart the Docker service.
  info 'Restarting the Docker service.'
  sudo systemctl restart --now 'docker.service'

  info 'nVIDIA container toolkit install complete.'
}

# Look for lspci, insall it if we need to.
if ! which lspci 2>&1 >/dev/null; then
  info 'lspci not found, installing package pciutils.'
  sudo apt install -y -qqq pciutils
fi

# These steps are conditional depending on whether we have an nVIDIA GPU
# present.
if lspci | grep -i 'nvidia' 2>&1 >/dev/null; then
  info 'Beginning containerized workload GPU-enablement.'

  # Install nVIDIA GPU drivers.
  # info 'Installing nVIDIA driver and modules.'
  # install_nvidia_gpu_drivers

  # Install the nVIDIA container toolkit.
  info 'Installing the nVIDIA container toolkit.'
  install_nvidia_container_toolkit

  # Load the nVIDIA modules.
  info 'Loading nVIDIA modules.'
  sudo modprobe nvidia

  info 'Completed containerized workload GPU-enablement.'
else
  info 'nVIDIA GPU not found, skipping GPU-enabledment.'
fi
