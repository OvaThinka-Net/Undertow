//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func enableProxy(host string, port int) error {
	proxy := fmt.Sprintf("socks=%s:%d", host, port)
	bypass := "<local>;localhost;127.0.0.1"
	// Set via netsh for WinHTTP
	exec.Command("netsh", "winhttp", "set", "proxy", "proxy-server="+proxy, "bypass-list="+bypass).Run()
	// Set via reg for WinINET (browsers)
	exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyServer", "/t", "REG_SZ", "/d", proxy, "/f").Run()
	exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyOverride", "/t", "REG_SZ", "/d", bypass, "/f").Run()
	return nil
}

func disableProxy() error {
	exec.Command("netsh", "winhttp", "reset", "proxy").Run()
	exec.Command("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyEnable", "/t", "REG_DWORD", "/d", "0", "/f").Run()
	exec.Command("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		"/v", "ProxyOverride", "/f").Run()
	return nil
}
