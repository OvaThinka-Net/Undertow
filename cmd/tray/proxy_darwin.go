//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func getActiveNetworkService() string {
	// Get the default route interface
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return "Wi-Fi"
	}

	var iface string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface:") {
			iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
			break
		}
	}
	if iface == "" {
		return "Wi-Fi"
	}

	// Map interface to network service name
	services, err := exec.Command("networksetup", "-listallhardwareports").Output()
	if err != nil {
		return "Wi-Fi"
	}

	var currentService string
	for _, line := range strings.Split(string(services), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Hardware Port:") {
			currentService = strings.TrimPrefix(line, "Hardware Port: ")
		}
		if strings.HasPrefix(line, "Device:") {
			device := strings.TrimSpace(strings.TrimPrefix(line, "Device:"))
			if device == iface {
				return currentService
			}
		}
	}

	return "Wi-Fi"
}

func enableProxy(host string, port int) error {
	service := getActiveNetworkService()
	portStr := fmt.Sprintf("%d", port)

	if err := exec.Command("networksetup", "-setsocksfirewallproxy", service, host, portStr).Run(); err != nil {
		return err
	}
	return exec.Command("networksetup", "-setsocksfirewallproxystate", service, "on").Run()
}

func disableProxy() error {
	service := getActiveNetworkService()
	return exec.Command("networksetup", "-setsocksfirewallproxystate", service, "off").Run()
}
