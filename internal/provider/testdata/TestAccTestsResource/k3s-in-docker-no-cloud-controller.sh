#!/bin/sh
set -eu

# The embedded k3s cloud controller sets the node's providerID to k3s://<node>.
# With it disabled, nothing initializes the node, so the providerID stays empty.
provider_id=$(kubectl get nodes -o jsonpath='{.items[0].spec.providerID}')
if [ -n "$provider_id" ]; then
  echo "expected no providerID with the cloud controller disabled, got: $provider_id"
  exit 1
fi
