//go:build windows

package main

import "golang.org/x/sys/windows/registry"

// cleanupProxy removes any system-wide proxy settings that may have been
// left behind by a previous version of the tray app.
func cleanupProxy() {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.SetDWordValue("ProxyEnable", 0)
	k.DeleteValue("ProxyOverride")
	k.DeleteValue("ProxyServer")
}
