#!/bin/bash
# install.sh — one-time setup for Undertow server on a Linux machine.
#
# Run as root:
#   sudo bash install.sh
#
# What it does:
#   1. Creates an 'undertow' system user
#   2. Sets up config from example if missing
#   3. Makes binaries executable
#   4. Installs systemd service (if available)

set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
ADMIN_BIN="$DIR/admin"
SERVER_BIN="$DIR/server"

# ---------------------------------------------------------------------------
# Must run as root
# ---------------------------------------------------------------------------
if [[ $EUID -ne 0 ]]; then
    echo "  ERROR: install.sh must be run as root."
    echo "  Usage: sudo bash install.sh"
    exit 1
fi

echo ""
echo "  Undertow — server setup"
echo ""

# ---------------------------------------------------------------------------
# 1. Create undertow system user
# ---------------------------------------------------------------------------
if ! id -u undertow &>/dev/null; then
    echo "  [setup] Creating 'undertow' system user..."
    useradd --system --no-create-home --shell /usr/sbin/nologin undertow
else
    echo "  [setup] User 'undertow' already exists."
fi

# ---------------------------------------------------------------------------
# 2. Verify binaries
# ---------------------------------------------------------------------------
if [[ ! -f "$ADMIN_BIN" ]]; then
    echo "  ERROR: admin binary not found at $ADMIN_BIN" >&2
    exit 1
fi
if [[ ! -f "$SERVER_BIN" ]]; then
    echo "  ERROR: server binary not found at $SERVER_BIN" >&2
    exit 1
fi

chmod +x "$ADMIN_BIN" "$SERVER_BIN"
if [[ -f "$DIR/client" ]]; then
    chmod +x "$DIR/client"
fi

echo "  [setup] Binaries OK."

# ---------------------------------------------------------------------------
# 3. Create config files from examples
# ---------------------------------------------------------------------------
ADMIN_CONFIG="$DIR/admin_config.json"
if [[ ! -f "$ADMIN_CONFIG" && -f "$DIR/admin_config.json.example" ]]; then
    echo "  [setup] Creating admin_config.json from example..."
    cp "$DIR/admin_config.json.example" "$ADMIN_CONFIG"
    echo "  [setup] *** IMPORTANT: Edit admin_config.json to set your password ***"
fi

SERVER_CONFIG="$DIR/server_config.json"
if [[ ! -f "$SERVER_CONFIG" && -f "$DIR/server_config.json.example" ]]; then
    echo "  [setup] Creating server_config.json from example..."
    cp "$DIR/server_config.json.example" "$SERVER_CONFIG"
fi

# ---------------------------------------------------------------------------
# 4. Create working directories
# ---------------------------------------------------------------------------
mkdir -p "$DIR/clients"
mkdir -p "$DIR/logs"

# ---------------------------------------------------------------------------
# 5. Set ownership
# ---------------------------------------------------------------------------
chown -R undertow:undertow "$DIR"

echo ""
echo "  Setup complete. Start Undertow with:"
echo "    cd $DIR && sudo -u undertow ./admin"
echo ""
echo "  Then open http://$(hostname -I 2>/dev/null | awk '{print $1}' || echo 'your-server-ip'):8090"
echo "  and follow the setup wizard."
echo ""

# ---------------------------------------------------------------------------
# 6. Install systemd service (optional)
# ---------------------------------------------------------------------------
if command -v systemctl &>/dev/null; then
    SERVICE_FILE="/etc/systemd/system/undertow.service"
    TEMPLATE="$DIR/undertow.service"

    if [[ -f "$TEMPLATE" ]]; then
        echo "  [setup] Installing systemd service..."
        sed "s|__INSTALL_DIR__|$DIR|g" "$TEMPLATE" > "$SERVICE_FILE"
        systemctl daemon-reload
        systemctl enable undertow.service
        echo "  [setup] Service installed and enabled."
        echo ""
        echo "  Manage Undertow as a service:"
        echo "    sudo systemctl start undertow"
        echo "    sudo systemctl stop undertow"
        echo "    sudo systemctl status undertow"
        echo "    journalctl -u undertow -f"
        echo ""
    fi
fi
