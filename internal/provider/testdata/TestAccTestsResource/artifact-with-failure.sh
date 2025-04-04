#!/bin/sh
set -eux

# IMAGETEST_ARTIFACTS is set by the entrypoint based on entrypoint.ArtifactsDir
echo "Creating artifact in directory: ${IMAGETEST_ARTIFACTS}"

# Create a subdirectory structure for realism
mkdir -p "${IMAGETEST_ARTIFACTS}/results"

# Create the artifact file with known content
echo "hello artifact content 123" >"${IMAGETEST_ARTIFACTS}/results/output.txt"

echo "Artifact created successfully at ${IMAGETEST_ARTIFACTS}/results/output.txt"

cat imalittleteapot
