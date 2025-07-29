#!/usr/bin/env bash

# If we have `lspci`, check if we have a GPU we actually need to install the
# container toolkit for.
if which lspci 2>&1 >/dev/null; then
  if ! lspci | grep -i 'nvidia' 2>&1 >/dev/null; then
    info 'No nVIDIA devices found, exiting.'
    exit 0
  fi
fi

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
export nver=1.17.8-1
sudo apt update -qq
sudo apt-get install -y -qq \
  nvidia-container-toolkit=${nver} \
  nvidia-container-toolkit-base=${nver} \
  libnvidia-container-tools=${nver} \
  libnvidia-container1=${nver}

# Restart the Docker service.
info 'Restarting the Docker service.'
sudo systemctl restart --now 'docker.service'

info 'nVIDIA container toolkit install complete.'
exit 0
