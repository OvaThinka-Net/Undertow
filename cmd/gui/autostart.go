package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"text/template"
)

const plistLabel = "net.ovathinka.undertow"

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.ExePath}}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>{{.LogPath}}</string>
    <key>StandardErrorPath</key>
    <string>{{.LogPath}}</string>
</dict>
</plist>
`))

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

func isAutoStartEnabled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(plistPath())
	return err == nil
}

func enableAutoStart() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("auto-start only supported on macOS")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".undertow", "undertow.log")

	plist := plistPath()
	os.MkdirAll(filepath.Dir(plist), 0755)

	f, err := os.Create(plist)
	if err != nil {
		return fmt.Errorf("create plist: %w", err)
	}
	defer f.Close()

	return plistTemplate.Execute(f, struct {
		Label, ExePath, LogPath string
	}{
		Label:   plistLabel,
		ExePath: exePath,
		LogPath: logPath,
	})
}

func disableAutoStart() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	err := os.Remove(plistPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
