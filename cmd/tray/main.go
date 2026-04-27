package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/getlantern/systray"
)

var (
	appDataDir string
	app        *appState
)

type appState struct {
	tunnel   *Tunnel
	logs     *LogBuffer
	dash     *Dashboard
	useProxy bool
	mu       sync.Mutex

	mStatus     *systray.MenuItem
	mConnect    *systray.MenuItem
	mDisconnect *systray.MenuItem
}

func (a *appState) doConnect() error {
	if a.tunnel.IsRunning() {
		return fmt.Errorf("already connected")
	}

	if needsOAuth(appDataDir) {
		log.Println("Starting OAuth flow...")
		if err := doOAuth(appDataDir); err != nil {
			return fmt.Errorf("OAuth: %w", err)
		}
	}

	if err := a.tunnel.Start(); err != nil {
		return err
	}

	a.mu.Lock()
	if a.useProxy {
		if err := enableProxy("127.0.0.1", 1080); err != nil {
			log.Printf("Warning: failed to set system proxy: %v", err)
		}
	}
	a.mu.Unlock()

	systray.SetIcon(makeIcon(63, 185, 80, true))
	systray.SetTooltip("Undertow — Connected")
	a.mStatus.SetTitle("Status: Connected ✓")
	a.mConnect.Disable()
	a.mDisconnect.Enable()
	return nil
}

func (a *appState) doDisconnect() {
	a.mu.Lock()
	if a.useProxy {
		disableProxy()
	}
	a.mu.Unlock()

	a.tunnel.Stop()

	systray.SetIcon(makeIcon(140, 140, 140, false))
	systray.SetTooltip("Undertow — Disconnected")
	a.mStatus.SetTitle("Status: Disconnected")
	a.mConnect.Enable()
	a.mDisconnect.Disable()
}

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

	filesToCopy := []string{"credentials.json", "credentials.json.token", "client_config.json"}
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
		if name == "credentials.json" || name == "credentials.json.token" {
			perm = 0600
		}
		if err := os.WriteFile(dst, data, perm); err == nil {
			log.Printf("First run: copied %s to %s", name, appDataDir)
		}
	}
}

func onReady() {
	logs := NewLogBuffer(1000)
	log.SetOutput(io.MultiWriter(os.Stderr, logs))
	log.SetFlags(log.Ltime)

	tunnel := NewTunnel(appDataDir)

	app = &appState{
		tunnel:   tunnel,
		logs:     logs,
		useProxy: true,
	}

	// Start embedded dashboard
	app.dash = NewDashboard(tunnel, logs, appDataDir)
	app.dash.onConnect = app.doConnect
	app.dash.onDisconnect = app.doDisconnect
	if err := app.dash.Start(); err != nil {
		log.Printf("Dashboard failed to start: %v", err)
	} else {
		log.Printf("Dashboard at %s", app.dash.URL())
	}

	systray.SetIcon(makeIcon(140, 140, 140, false))
	systray.SetTooltip("Undertow — Disconnected")

	app.mStatus = systray.AddMenuItem("Status: Disconnected", "")
	app.mStatus.Disable()

	systray.AddSeparator()
	app.mConnect = systray.AddMenuItem("Connect", "Start the tunnel")
	app.mDisconnect = systray.AddMenuItem("Disconnect", "Stop the tunnel")
	app.mDisconnect.Disable()

	systray.AddSeparator()
	mProxy := systray.AddMenuItemCheckbox("Set System Proxy", "Auto-configure SOCKS proxy", true)

	systray.AddSeparator()
	mDashboard := systray.AddMenuItem("Dashboard", "Open web dashboard")
	mOpenFolder := systray.AddMenuItem("Open Config Folder", "")
	mQuit := systray.AddMenuItem("Quit", "")

	// Check setup state
	if !fileExists(filepath.Join(appDataDir, "credentials.json")) {
		app.mStatus.SetTitle("⚠ Setup: credentials.json missing")
		app.mConnect.Disable()
	}

	go func() {
		for {
			select {
			case <-app.mConnect.ClickedCh:
				app.mConnect.Disable()
				app.mStatus.SetTitle("Status: Connecting...")
				systray.SetTooltip("Undertow — Connecting...")

				if err := app.doConnect(); err != nil {
					app.mStatus.SetTitle(fmt.Sprintf("⚠ Error: %s", err))
					app.mConnect.Enable()
				}

			case <-app.mDisconnect.ClickedCh:
				app.mDisconnect.Disable()
				app.mStatus.SetTitle("Status: Disconnecting...")
				app.doDisconnect()

			case <-mProxy.ClickedCh:
				app.mu.Lock()
				if mProxy.Checked() {
					mProxy.Uncheck()
					app.useProxy = false
					if app.tunnel.IsRunning() {
						disableProxy()
					}
				} else {
					mProxy.Check()
					app.useProxy = true
					if app.tunnel.IsRunning() {
						enableProxy("127.0.0.1", 1080)
					}
				}
				app.mu.Unlock()

			case <-mDashboard.ClickedCh:
				app.dash.Open()

			case <-mOpenFolder.ClickedCh:
				openFolder(appDataDir)

			case <-mQuit.ClickedCh:
				if app.tunnel.IsRunning() {
					disableProxy()
					app.tunnel.Stop()
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
