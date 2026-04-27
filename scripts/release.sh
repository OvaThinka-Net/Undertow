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

### One-Line Install (Linux)
\`\`\`bash
curl -fsSL https://raw.githubusercontent.com/${REPO}/main/setup.sh | sudo bash
\`\`\`
Auto-detects architecture, downloads, installs, and starts the service.

### What's New
- **Admin Credentials Panel**: Change username/password directly from the dashboard
- **Shared OAuth Token**: Client packages now include the server's OAuth token — no Google sign-in needed on clients
- **Shared Folder ID**: Client config includes the Drive folder ID — clients connect to the correct shared folder automatically
- **Forced Password Change**: Default credentials (admin/admin) must be changed on first login

### Downloads
Each zip contains: \`admin\`, \`server\`, \`client\` binaries + \`clients/\` directory with all platform client binaries + example configs + install scripts.

### Platforms
- **linux-amd64** — x86_64 servers
- **linux-arm64** — Raspberry Pi 4+, ARM servers
- **linux-armv7** — Raspberry Pi 3, older ARM
- **linux-armv6** — Raspberry Pi Zero
- **darwin-amd64** — macOS Intel
- **darwin-arm64** — macOS Apple Silicon
- **windows-amd64** — Windows x64
- **windows-arm64** — Windows ARM

### Manual Server Install (Linux)
\`\`\`bash
ARCH=\$(uname -m); case \$ARCH in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; armv7l) ARCH=armv7;; armv6l) ARCH=armv6;; esac
wget https://github.com/${REPO}/releases/download/${TAG}/undertow-${TAG}-linux-\${ARCH}.zip
unzip undertow-${TAG}-linux-\${ARCH}.zip
sudo mv undertow-${TAG}-linux-\${ARCH} /opt/undertow
cd /opt/undertow && sudo bash install.sh
sudo systemctl start undertow
\`\`\`

### Quick Start
1. Run the one-line installer or download the zip for your server platform
2. Open **http://your-server-ip:8090** and log in (default: admin / admin)
3. You will be prompted to change the default password on first login
4. Follow the setup wizard to configure Google Drive credentials
5. Download ready-to-use client packages from the admin panel (bundles client binary + credentials + config)

See [README](https://github.com/${REPO}#readme) for full instructions." \
    "${ZIPS[@]}"

echo ""
echo "=== Release ${TAG} published ==="
echo "https://github.com/${REPO}/releases/tag/${TAG}"
