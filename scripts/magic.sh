#!/bin/bash
# magic.sh — rebuild and publish/update the GitHub release.
#
# Usage: bash scripts/magic.sh [tag]   (default: v1.0.0)
set -euo pipefail

cd "$(dirname "$0")/.."

TAG="${1:-v1.0.0}"

bash scripts/release.sh "$TAG"
