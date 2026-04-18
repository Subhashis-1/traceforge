#!/usr/bin/env bash
set -euo pipefail

# Change to the SDK directory
cd pkg/sdk-js

# Pack the package
echo "Packing @traceforge/sdk-js..."
TARBALL=$(npm pack)

# Echo the tarball name and size
echo "Tarball: $TARBALL"
TARBALL_SIZE=$(du -h "$TARBALL" | cut -f1)
echo "Size: $TARBALL_SIZE"

# Optional: Publish to custom registry if NPM_REGISTRY is set
if [[ -n "${NPM_REGISTRY:-}" ]]; then
    echo "Publishing to registry: $NPM_REGISTRY"
    npm publish --registry "$NPM_REGISTRY" --access public
else
    echo "NPM_REGISTRY not set. Skipping publish step."
    echo "To publish, set NPM_REGISTRY environment variable and re-run."
fi

echo "Done!"
