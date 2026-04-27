//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const regKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
const regValue = "Undertow"

func isAutoStartEnabled() bool {
	out, err := exec.Command("reg", "query", regKey, "/v", regValue).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), regValue)
}

func enableAutoStart() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	return exec.Command("reg", "add", regKey,
		"/v", regValue, "/t", "REG_SZ", "/d", exePath, "/f").Run()
}

func disableAutoStart() error {
	err := exec.Command("reg", "delete", regKey, "/v", regValue, "/f").Run()
	if err != nil {
		// If the key doesn't exist, that's fine
		out, _ := exec.Command("reg", "query", regKey, "/v", regValue).CombinedOutput()
		if strings.Contains(string(out), "ERROR") {
			return nil
		}
		return err
	}
	return nil
}
