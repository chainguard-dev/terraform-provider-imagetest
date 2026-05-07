#!/bin/sh
set -eux

echo "Hello from docker-on-host"

# The shim at /usr/local/bin/docker auto-injects --label "$IMAGETEST_TEST_LABEL".
docker run --rm hello-world
