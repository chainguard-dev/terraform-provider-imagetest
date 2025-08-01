#!/usr/bin/env bash

readonly default_registry="ttl.sh"

# Set a default 'IMAGETEST_REGISTRY' value, if necessary.
#
# The driver's acceptance test will do this automatically, but trying to add
# some clarity by handling here.
if [ -z $IMAGETEST_REGISTRY ]; then
  echo "No 'IMAGETEST_REGISTRY' set, using default [${default_registry}]."
  export IMAGETEST_REGISTRY="ttl.sh"
else
  echo "Using 'IMAGETEST_REGISTRY' registry [${IMAGETEST_REGISTRY}]."
fi

# Build the entrypoint image.
export IMAGETEST_ENTRYPOINT_REF=$(
	KO_DOCKER_REPO="${IMAGETEST_REGISTRY}/imagetest" \
		ko build "./cmd/entrypoint"
)
echo "Built entrypoint image ref [${IMAGETEST_ENTRYPOINT_REF}]."

# "Enable" acceptance tests.
export TF_ACC=1

# Run the tests.
echo "Beginning acceptance tests."
go test \
  -tags ec2 ./internal/provider \
  -run '^TestAccTestDriverEC2$' \
  -count 1 \
  -v

if [ $? -eq 0 ]; then
  echo "Acceptance tests completed successfully!"
  exit 0
else
  echo "Acceptance tests failed!"
  exit 1
fi
