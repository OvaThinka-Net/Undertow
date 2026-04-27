//go:build windows

package main

import "os/exec"

// cleanupProxy removes any system-wide proxy settings that may have been
// left behind by a previous version of the tray app.
func cleanupProxy() {
	exec.Command("netsh", "winhttp", "reset", "proxy").Run()
	exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	exec.Command("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyOverride", "/f").Run()
	exec.Command("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer", "/f").Run()
}
