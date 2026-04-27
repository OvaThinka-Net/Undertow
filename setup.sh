#!/bin/bash
# Undertow — one-line remote setup & update script
# Fresh install:  curl -fsSL https://raw.githubusercontent.com/OvaThinka-Net/Undertow/main/setup.sh | sudo bash
# Pin a version:  curl -fsSL ... | sudo VERSION=v1.0.0 bash
set -euo pipefail

INSTALL_DIR="/opt/undertow"
REPO_OWNER="OvaThinka-Net"
REPO_NAME="Undertow"
REPO_DL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"

echo ""
echo "  ⚡ Undertow Setup"
echo ""

# Must run as root
if [[ $EUID -ne 0 ]]; then
    echo "  ERROR: This script must be run as root (use sudo)."
    exit 1
fi

# ---------------------------------------------------------------------------
# Resolve version — use $VERSION env var, else fetch latest from GitHub API
# ---------------------------------------------------------------------------
if [[ -z "${VERSION:-}" ]]; then
    echo "  Detecting latest release..."
    if command -v curl &>/dev/null; then
        VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    elif command -v wget &>/dev/null; then
        VERSION=$(wget -qO- "https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    else
        echo "  ERROR: Neither curl nor wget found."; exit 1
    fi
    if [[ -z "$VERSION" ]]; then
        echo "  ERROR: Could not determine latest version. Set VERSION manually:"
        echo "    curl ... | sudo VERSION=v1.0.0 bash"
        exit 1
    fi
fi

# Detect if this is an update
IS_UPDATE=false
if [[ -d "$INSTALL_DIR" && -f "$INSTALL_DIR/server" ]]; then
    IS_UPDATE=true
    echo "  Mode: UPDATE to ${VERSION}"
else
    echo "  Mode: FRESH INSTALL ${VERSION}"
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

ZIP="undertow-linux-${ARCH}.zip"
URL="${REPO_DL}/${VERSION}/${ZIP}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "  [1/5] Downloading ${ZIP}..."
if command -v curl &>/dev/null; then
    curl -fsSL -o "$TMP/$ZIP" "$URL"
elif command -v wget &>/dev/null; then
    wget -q -O "$TMP/$ZIP" "$URL"
fi

echo "  [2/5] Extracting..."
if ! command -v unzip &>/dev/null; then
    echo "  Installing unzip..."
    apt-get install -y unzip >/dev/null 2>&1 || yum install -y unzip >/dev/null 2>&1
fi
unzip -qo "$TMP/$ZIP" -d "$TMP"

# ---------------------------------------------------------------------------
# Stop running service before replacing binaries
# ---------------------------------------------------------------------------
if [[ "$IS_UPDATE" == true ]] && command -v systemctl &>/dev/null; then
    if systemctl is-active --quiet undertow 2>/dev/null; then
        echo "  [3/5] Stopping undertow service..."
        systemctl stop undertow
    else
        echo "  [3/5] Service not running, skipping stop."
    fi
else
    echo "  [3/5] Installing to ${INSTALL_DIR}..."
fi

# ---------------------------------------------------------------------------
# Preserve config, credentials, logs, and client data
# ---------------------------------------------------------------------------
PRESERVE_FILES=(admin_config.json server_config.json client_config.json credentials.json credentials.json.token)
PRESERVE_DIRS=(logs clients)

if [[ -d "$INSTALL_DIR" ]]; then
    for cfg in "${PRESERVE_FILES[@]}"; do
        [[ -f "$INSTALL_DIR/$cfg" ]] && cp "$INSTALL_DIR/$cfg" "$TMP/$cfg.bak" 2>/dev/null || true
    done
    for dir in "${PRESERVE_DIRS[@]}"; do
        [[ -d "$INSTALL_DIR/$dir" ]] && cp -r "$INSTALL_DIR/$dir" "$TMP/$dir.bak" 2>/dev/null || true
    done
    rm -rf "$INSTALL_DIR"
fi

mv "$TMP/undertow-linux-${ARCH}" "$INSTALL_DIR"

# Restore preserved files
for cfg in "${PRESERVE_FILES[@]}"; do
    [[ -f "$TMP/$cfg.bak" ]] && mv "$TMP/$cfg.bak" "$INSTALL_DIR/$cfg"
done
for dir in "${PRESERVE_DIRS[@]}"; do
    [[ -d "$TMP/$dir.bak" ]] && mv "$TMP/$dir.bak" "$INSTALL_DIR/$dir"
done

echo "  [4/5] Running installer..."
bash "$INSTALL_DIR/install.sh"

echo "  [5/5] Starting service..."
systemctl start undertow

IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo 'your-server-ip')
echo ""
if [[ "$IS_UPDATE" == true ]]; then
    echo "  ✅ Undertow updated to ${VERSION} and restarted!"
else
    echo "  ✅ Undertow ${VERSION} is running!"
    echo "  Open http://${IP}:8090 and follow the setup wizard."
fi
echo ""
