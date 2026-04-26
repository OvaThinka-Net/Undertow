#!/bin/bash
# uninstall.sh — remove Undertow systemd service, user, and optionally all data.
#
# Run as root:
#   sudo bash uninstall.sh

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "  ERROR: uninstall.sh must be run as root."
    echo "  Usage: sudo bash uninstall.sh"
    exit 1
fi

SERVICE_FILE="/etc/systemd/system/undertow.service"
DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "  Undertow — uninstall"
echo ""

# Stop and remove systemd service
if [[ -f "$SERVICE_FILE" ]]; then
    echo "  [uninstall] Stopping service..."
    systemctl stop undertow.service 2>/dev/null || true
    systemctl disable undertow.service 2>/dev/null || true
    rm -f "$SERVICE_FILE"
    systemctl daemon-reload
    systemctl reset-failed undertow.service 2>/dev/null || true
    echo "  [uninstall] Service removed."
else
    echo "  [uninstall] No systemd service found — skipping."
fi

# Remove undertow system user
if id -u undertow &>/dev/null; then
    echo "  [uninstall] Removing 'undertow' system user..."
    userdel undertow 2>/dev/null || true
    echo "  [uninstall] User removed."
fi

# Optionally remove all data
echo ""
read -rp "  Delete all Undertow files ($DIR)? [y/N] " answer
if [[ "$answer" =~ ^[Yy]$ ]]; then
    rm -rf "$DIR"
    echo "  [uninstall] Directory removed."
else
    echo "  [uninstall] Directory kept. You can remove it manually:"
    echo "    rm -rf $DIR"
fi

echo ""
echo "  Done."
echo ""
