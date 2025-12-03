#!/usr/bin/env bash

readonly default_registry="ttl.sh"

# Require VPC_ID to be set - EC2 driver needs a pre-provisioned VPC.
if [ -z "$VPC_ID" ]; then
  echo "ERROR: VPC_ID environment variable is required."
  echo "The EC2 driver requires a pre-provisioned VPC."
  exit 1
fi
echo "Using VPC_ID [${VPC_ID}]."
export TF_VAR_vpc_id="${VPC_ID}"

# Set a default 'IMAGETEST_REGISTRY' value, if necessary.
#
# The driver's acceptance test will do this automatically, but trying to add
# some clarity by handling here.
if [ -z "$IMAGETEST_REGISTRY" ]; then
  echo "No 'IMAGETEST_REGISTRY' set, using default [${default_registry}]."
  export IMAGETEST_REGISTRY="ttl.sh"
else
  echo "Using 'IMAGETEST_REGISTRY' registry [${IMAGETEST_REGISTRY}]."
fi

# Build the entrypoint image.
entrypoint_ref=$(
	KO_DOCKER_REPO="${IMAGETEST_REGISTRY}/imagetest" \
		ko build "./cmd/entrypoint"
)
if [ $? -ne 0 ]; then
  echo 'ERROR: Failed entrypoint build.'
  exit 1
fi
echo "Built entrypoint image ref [${entrypoint_ref}]."

# Run the tests.
echo "Beginning acceptance tests."

# Run the EC2 acceptance tests.
IMAGETEST_ENTRYPOINT_REF="${entrypoint_ref}" TF_ACC=1 \
  go test \
    -tags ec2 ./internal/provider \
    -run '^TestAccTestDriverEC2$' \
    -count 1 \
    -timeout 0 \
    -v
if [ $? -eq 0 ]; then
  echo "Acceptance tests completed successfully!"
  exit 0
else
  echo "Acceptance tests failed!"
  exit 1
fi
