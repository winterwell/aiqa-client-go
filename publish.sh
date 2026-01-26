#!/bin/bash
set -e  # Exit on error

# Read version from version.json
VERSION=$(grep -o '"VERSION": "[^"]*"' version.json | cut -d'"' -f4)

if [ -z "$VERSION" ]; then
    echo "Error: Could not read VERSION from version.json"
    exit 1
fi

echo "Publishing version: $VERSION"

# Create git tag
TAG="v$VERSION"
echo "Creating git tag: $TAG"
git tag "$TAG"

# Push the tag
echo "Pushing tag to remote..."
git push origin "$TAG"

# Trigger Go module indexing by fetching from the proxy
# This will cause pkg.go.dev to index the new version
MODULE_PATH="github.com/winterwell/aiqa-client-go"
echo "Triggering Go module indexing..."
curl -s "https://proxy.golang.org/$MODULE_PATH/@v/$TAG.info" > /dev/null || echo "Warning: Failed to trigger indexing (this is usually fine, indexing happens automatically)"

echo "Done! Version $VERSION has been published."
echo "Check https://pkg.go.dev/$MODULE_PATH for the updated module (may take a few minutes)."
