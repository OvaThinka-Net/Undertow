Covert SOCKS5 tunnel that routes traffic through Google Drive as a steganographic transport layer. Web admin panel with setup wizard, client packaging, and native system tray app.

**One-line install (Linux server):**

```bash
curl -fsSL https://raw.githubusercontent.com/OvaThinka-Net/Undertow/main/setup.sh | sudo bash
```

Auto-detects architecture, downloads the release, installs to `/opt/undertow`, and starts the systemd service. Then open `http://your-server-ip:8090` and follow the setup wizard.

**What's included:**

- Web Admin Panel with 6-step setup wizard — no CLI needed after install
- Forced password change on first login (default: admin / admin)
- Live server log streaming in the browser
- Client package downloads — ready-to-use zips with bundled credentials and config
- Native system tray app (macOS, Windows) — Connect/Disconnect/Proxy/AutoStart/Dashboard
- Web GUI client — CGO-free, cross-platform
- Windows auto-start at login via `HKCU\Run` registry key
- macOS auto-start via LaunchAgent plist
- Multi-IP transport with round-robin fallback (4 default Google IPs)
- SOCKS5 tunnel over Google Drive with binary protocol
- SSRF protection — blocks private/reserved IP ranges
- CSRF protection with SameSite cookies and origin checks

**Platforms:**

| File | Platform |
|------|----------|
| `undertow-*-linux-amd64.zip` | x86_64 servers |
| `undertow-*-linux-arm64.zip` | Raspberry Pi 4+, ARM servers |
| `undertow-*-linux-armv7.zip` | Raspberry Pi 3, older ARM |
| `undertow-*-linux-armv6.zip` | Raspberry Pi Zero |
| `undertow-*-darwin-amd64.zip` | macOS Intel |
| `undertow-*-darwin-arm64.zip` | macOS Apple Silicon |
| `undertow-*-windows-amd64.zip` | Windows x64 |
| `undertow-*-windows-arm64.zip` | Windows ARM |

Each zip contains: `admin`, `server`, `client` binaries + `clients/` directory with all platform client/GUI/tray binaries + example configs + install scripts.

**Quick start:**

1. Run the one-line installer or download the zip for your server platform
2. Open `http://your-server-ip:8090` and log in (default: admin / admin)
3. Change the default password when prompted
4. Follow the setup wizard to configure Google Drive credentials
5. Download ready-to-use client packages from the admin panel
