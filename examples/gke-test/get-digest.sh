#!/bin/bash
# Helper script to get image digests for use in imagetest configurations

set -e

# Check if crane is installed
if ! command -v crane &> /dev/null; then
    echo "Installing crane..."
    go install github.com/google/go-containerregistry/cmd/crane@latest
fi

# Default images if no arguments provided
if [ $# -eq 0 ]; then
    IMAGES=(
        "cgr.dev/chainguard/nginx:latest-dev"
        "cgr.dev/chainguard/kubectl:latest-dev"
        "cgr.dev/chainguard/busybox:latest"
    )
else
    IMAGES=("$@")
fi

echo "Getting digests for images..."
echo ""

for img in "${IMAGES[@]}"; do
    # Extract the image without tag
    base_img="${img%:*}"
    tag="${img##*:}"
    
    # Get the digest
    digest=$(crane digest "$img" 2>/dev/null || echo "ERROR")
    
    if [ "$digest" = "ERROR" ]; then
        echo "❌ Failed to get digest for: $img"
    else
        full_ref="${img}@${digest}"
        echo "✓ $full_ref"
    fi
done

echo ""
echo "Use these full references in your Terraform configuration:"
echo ""

for img in "${IMAGES[@]}"; do
    digest=$(crane digest "$img" 2>/dev/null || echo "")
    if [ -n "$digest" ]; then
        echo "  ${img}@${digest}"
    fi
done
