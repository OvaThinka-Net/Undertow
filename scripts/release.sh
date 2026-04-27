#!/bin/bash
# release.sh — tag, build, and publish a GitHub release for Undertow.
#
# Usage:
#   bash scripts/release.sh [tag]   (default: v1.0.0)
#
# Builds all platforms, creates/updates the GitHub release, and uploads zips.
set -euo pipefail

cd "$(dirname "$0")/.."

TAG="${1:-v1.0.0}"
REPO="OvaThinka-Net/Undertow"
RELEASE_DIR="release"
NOTES_FILE="RELEASE.md"

if ! command -v gh &>/dev/null; then
    echo "ERROR: GitHub CLI (gh) not found." >&2
    exit 1
fi

if [[ ! -f "$NOTES_FILE" ]]; then
    echo "ERROR: $NOTES_FILE not found." >&2
    exit 1
fi

# 1. Build
echo "=== Undertow Release ${TAG} ==="
echo ""
echo "Step 1: Building all platforms..."
rm -rf "$RELEASE_DIR"
bash scripts/build.sh "$TAG"
echo ""

ZIPS=("$RELEASE_DIR"/undertow-${TAG}-*.zip)
if [[ ${#ZIPS[@]} -eq 0 ]]; then
    echo "ERROR: No zip files found in $RELEASE_DIR/" >&2
    exit 1
fi

# 2. Tag
echo "Step 2: Tagging..."
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "  Tag $TAG already exists locally — moving to HEAD."
    git tag -d "$TAG"
    git push origin ":refs/tags/$TAG" 2>/dev/null || true
fi
git tag "$TAG"
git push origin "$TAG"
echo ""

# 3. Create or update GitHub release
echo "Step 3: Publishing release..."
if gh release view "$TAG" --repo "$REPO" >/dev/null 2>&1; then
    echo "  Release $TAG already exists — updating assets and notes."
    gh release edit "$TAG" --repo "$REPO" --title "$TAG" --notes-file "$NOTES_FILE"
    gh release upload "$TAG" "${ZIPS[@]}" --repo "$REPO" --clobber
else
    gh release create "$TAG" "${ZIPS[@]}" \
        --repo "$REPO" \
        --title "$TAG" \
        --notes-file "$NOTES_FILE"
fi

echo ""
echo "=== Release ${TAG} published ==="
echo "https://github.com/${REPO}/releases/tag/${TAG}"
