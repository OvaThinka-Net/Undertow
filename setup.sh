#!/bin/bash
# Undertow — one-line remote setup script
# Usage: curl -fsSL https://raw.githubusercontent.com/OvaThinka-Net/Undertow/main/setup.sh | sudo bash
set -euo pipefail

VERSION="v1.0.1"
INSTALL_DIR="/opt/undertow"
REPO="https://github.com/OvaThinka-Net/Undertow/releases/download"

echo ""
echo "  ⚡ Undertow Setup"
echo ""

# Must run as root
if [[ $EUID -ne 0 ]]; then
    echo "  ERROR: This script must be run as root (use sudo)."
    exit 1
fi

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH=amd64 ;;
    aarch64) ARCH=arm64 ;;
    armv7l)  ARCH=armv7 ;;
    armv6l)  ARCH=armv6 ;;
    *)       echo "  ERROR: Unsupported architecture: $ARCH"; exit 1 ;;
esac

ZIP="undertow-${VERSION}-linux-${ARCH}.zip"
URL="${REPO}/${VERSION}/${ZIP}"
TMP=$(mktemp -d)

echo "  [1/5] Downloading ${ZIP}..."
if command -v wget &>/dev/null; then
    wget -q -O "$TMP/$ZIP" "$URL"
elif command -v curl &>/dev/null; then
    curl -fsSL -o "$TMP/$ZIP" "$URL"
else
    echo "  ERROR: Neither wget nor curl found."; exit 1
fi

echo "  [2/5] Extracting..."
if ! command -v unzip &>/dev/null; then
    echo "  Installing unzip..."
    apt-get install -y unzip >/dev/null 2>&1 || yum install -y unzip >/dev/null 2>&1
fi
unzip -qo "$TMP/$ZIP" -d "$TMP"

echo "  [3/5] Installing to ${INSTALL_DIR}..."
if [[ -d "$INSTALL_DIR" ]]; then
    # Preserve existing configs
    for cfg in admin_config.json server_config.json credentials.json token.json; do
        [[ -f "$INSTALL_DIR/$cfg" ]] && cp "$INSTALL_DIR/$cfg" "$TMP/$cfg.bak" 2>/dev/null || true
    done
    rm -rf "$INSTALL_DIR"
fi
mv "$TMP/undertow-${VERSION}-linux-${ARCH}" "$INSTALL_DIR"

# Restore configs
for cfg in admin_config.json server_config.json credentials.json token.json; do
    [[ -f "$TMP/$cfg.bak" ]] && mv "$TMP/$cfg.bak" "$INSTALL_DIR/$cfg"
done

echo "  [4/5] Running installer..."
bash "$INSTALL_DIR/install.sh"

echo "  [5/5] Starting service..."
systemctl start undertow

rm -rf "$TMP"

IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo 'your-server-ip')
echo ""
echo "  ✅ Undertow is running!"
echo "  Open http://${IP}:8090 and follow the setup wizard."
echo ""
