#!/bin/bash
set -e

cd "$(dirname "$0")/.."

TAG="${1:-v1.0.0}"
REPO="OvaThinka-Net/Undertow"

echo "=== Undertow Release ${TAG} ==="
echo ""

# 1. Build all platforms
echo "Step 1: Building all platforms..."
bash scripts/build.sh "$TAG"
echo ""

# 2. Tag
if git rev-parse "$TAG" >/dev/null 2>&1; then
    echo "Tag $TAG already exists, skipping..."
else
    echo "Step 2: Creating tag $TAG..."
    git tag -a "$TAG" -m "Release $TAG"
    git push origin "$TAG"
fi
echo ""

# 3. Create GitHub release and upload zips
echo "Step 3: Creating GitHub release..."
ZIPS=(release/undertow-${TAG}-*.zip)

if [ ${#ZIPS[@]} -eq 0 ]; then
    echo "ERROR: No zip files found in release/"
    exit 1
fi

gh release create "$TAG" \
    --repo "$REPO" \
    --title "Undertow ${TAG}" \
    --notes "## Undertow ${TAG}

### Downloads
Each zip contains: \`client\`, \`server\`, \`admin\` binaries + example configs + README.

### Platforms
- **linux-amd64** — x86_64 servers
- **linux-arm64** — Raspberry Pi 4+, ARM servers
- **linux-armv7** — Raspberry Pi 3, older ARM
- **linux-armv6** — Raspberry Pi Zero
- **darwin-amd64** — macOS Intel
- **darwin-arm64** — macOS Apple Silicon
- **windows-amd64** — Windows x64
- **windows-arm64** — Windows ARM

### Server Install (Linux)
\`\`\`bash
# Download and extract (replace ARCH with your platform, e.g. linux-amd64)
wget https://github.com/${REPO}/releases/download/${TAG}/undertow-${TAG}-linux-amd64.zip
unzip undertow-${TAG}-linux-amd64.zip
cd undertow-${TAG}-linux-amd64

# Run the installer (creates user, configs, systemd service)
sudo bash install.sh

# Start the service
sudo systemctl start undertow

# Open the admin panel and follow the setup wizard
# http://your-server-ip:8090
\`\`\`

### Quick Start
1. Download the zip for your server platform
2. Extract and run \`./admin\` with an \`admin_config.json\`
3. Open the web UI and follow the setup wizard
4. Download client packages from the admin panel

See [README](https://github.com/${REPO}#readme) for full instructions." \
    "${ZIPS[@]}"

echo ""
echo "=== Release ${TAG} published ==="
echo "https://github.com/${REPO}/releases/tag/${TAG}"
