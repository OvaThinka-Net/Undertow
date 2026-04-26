package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/getlantern/systray"
)

var appDataDir string

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	appDataDir = filepath.Join(home, ".undertow")
	os.MkdirAll(appDataDir, 0700)

	// First-run: auto-copy config files from next to the binary
	firstRunSetup()

	systray.Run(onReady, onExit)
}

// firstRunSetup copies credentials.json and client_config.json from the
// directory containing the binary into ~/.undertow/ if they don't
// already exist there. This means the user just unzips and double-clicks.
func firstRunSetup() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exeDir := filepath.Dir(exePath)

	filesToCopy := []string{"credentials.json", "client_config.json"}
	for _, name := range filesToCopy {
		dst := filepath.Join(appDataDir, name)
		if fileExists(dst) {
			continue // already set up
		}
		src := filepath.Join(exeDir, name)
		if !fileExists(src) {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		perm := os.FileMode(0644)
		if name == "credentials.json" {
			perm = 0600
		}
		if err := os.WriteFile(dst, data, perm); err == nil {
			log.Printf("First run: copied %s to %s", name, appDataDir)
		}
	}
}

func onReady() {
	systray.SetIcon(makeIcon(140, 140, 140, false))
	systray.SetTooltip("Undertow — Disconnected")

	mStatus := systray.AddMenuItem("Status: Disconnected", "")
	mStatus.Disable()

	systray.AddSeparator()
	mConnect := systray.AddMenuItem("Connect", "Start the tunnel")
	mDisconnect := systray.AddMenuItem("Disconnect", "Stop the tunnel")
	mDisconnect.Disable()

	systray.AddSeparator()
	mProxy := systray.AddMenuItemCheckbox("Set System Proxy", "Auto-configure macOS SOCKS proxy", true)

	systray.AddSeparator()
	mOpenFolder := systray.AddMenuItem("Open Config Folder", "")
	mQuit := systray.AddMenuItem("Quit", "")

	tunnel := NewTunnel(appDataDir)
	useProxy := true

	// Check setup state
	if !fileExists(filepath.Join(appDataDir, "credentials.json")) {
		mStatus.SetTitle("⚠ Setup: credentials.json missing")
		mConnect.Disable()
	}

	go func() {
		for {
			select {
			case <-mConnect.ClickedCh:
				mConnect.Disable()
				mStatus.SetTitle("Status: Connecting...")
				systray.SetTooltip("Undertow — Connecting...")

				// OAuth if needed
				if needsOAuth(appDataDir) {
					mStatus.SetTitle("Status: Waiting for OAuth...")
					if err := doOAuth(appDataDir); err != nil {
						mStatus.SetTitle(fmt.Sprintf("⚠ OAuth: %s", err))
						mConnect.Enable()
						continue
					}
				}

				if err := tunnel.Start(); err != nil {
					mStatus.SetTitle(fmt.Sprintf("⚠ Error: %s", err))
					mConnect.Enable()
					continue
				}

				if useProxy {
					if err := enableProxy("127.0.0.1", 1080); err != nil {
						log.Printf("Warning: failed to set system proxy: %v", err)
					}
				}

				systray.SetIcon(makeIcon(63, 185, 80, true))
				systray.SetTooltip("Undertow — Connected")
				mStatus.SetTitle("Status: Connected ✓")
				mDisconnect.Enable()

			case <-mDisconnect.ClickedCh:
				mDisconnect.Disable()
				mStatus.SetTitle("Status: Disconnecting...")

				if useProxy {
					disableProxy()
				}
				tunnel.Stop()

				systray.SetIcon(makeIcon(140, 140, 140, false))
				systray.SetTooltip("Undertow — Disconnected")
				mStatus.SetTitle("Status: Disconnected")
				mConnect.Enable()

			case <-mProxy.ClickedCh:
				if mProxy.Checked() {
					mProxy.Uncheck()
					useProxy = false
					if tunnel.IsRunning() {
						disableProxy()
					}
				} else {
					mProxy.Check()
					useProxy = true
					if tunnel.IsRunning() {
						enableProxy("127.0.0.1", 1080)
					}
				}

			case <-mOpenFolder.ClickedCh:
				openFolder(appDataDir)

			case <-mQuit.ClickedCh:
				if tunnel.IsRunning() {
					disableProxy()
					tunnel.Stop()
				}
				systray.Quit()
			}
		}
	}()
}

func onExit() {
	disableProxy()
}

func makeIcon(r, g, b uint8, filled bool) []byte {
	const size = 22
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	radius := float64(size)/2 - 2

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Sqrt(dx*dx + dy*dy)
			if filled {
				if dist <= radius {
					img.Set(x, y, color.RGBA{r, g, b, 255})
				}
			} else {
				if dist <= radius && dist >= radius-1.8 {
					img.Set(x, y, color.RGBA{r, g, b, 200})
				}
			}
		}
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
