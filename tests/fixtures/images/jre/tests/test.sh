#!/usr/bin/env bash

set -o errexit -o nounset -o errtrace -o pipefail -x

image=$(echo "$IMAGES" | jq -r '.jre.ref')

docker run --rm --entrypoint java "$image" -version

# Compile HelloWorld.java with the SDK image, then run it on the JRE.
work=$(mktemp -d)
cp HelloWorld.java "$work"
chmod 777 "$work"

docker run --rm -v "$work:/data" --workdir /data --entrypoint javac "$SDK_IMAGE" \
  -source "$JAVA_SOURCE_VERSION" -target "$JAVA_TARGET_VERSION" HelloWorld.java

docker run --rm -v "$work:/data" --workdir /data --entrypoint java "$image" HelloWorld
