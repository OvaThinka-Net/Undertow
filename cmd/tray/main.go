package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

var (
	appDataDir string
	app        *appState
	startupLog *os.File // kept open for the entire lifetime
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

	// Log to a file so we can diagnose startup issues (especially on Windows
	// where there is no console).
	startupLog, _ = os.OpenFile(filepath.Join(appDataDir, "startup.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if startupLog != nil {
		log.SetOutput(startupLog)
		log.SetFlags(log.Ldate | log.Ltime)
		log.Printf("Undertow starting, exe=%s", os.Args[0])
	}

	// Wait briefly so the Windows shell (explorer.exe) is ready for
	// Shell_NotifyIcon. Without this, systray.Run can fail silently
	// when launched from the registry Run key at boot.
	time.Sleep(3 * time.Second)

	// First-run: auto-copy config files from next to the binary
	firstRunSetup()

	// Remove any leftover system proxy from previous versions
	disableProxy()

	// Auto-enable start-at-login on first run
	if !isAutoStartEnabled() {
		if err := enableAutoStart(); err != nil {
			log.Printf("Warning: failed to enable auto-start: %v", err)
		} else {
			log.Println("Auto-start enabled")
		}
	}
	// Keep registry path in sync if the exe was moved
	ensureAutoStartPath()

	log.Println("Calling systray.Run...")
	systray.Run(onReady, onExit)
	log.Println("systray.Run returned")
}

// firstRunSetup copies credentials.json, token, and client_config.json from
// the directory containing the binary into ~/.undertow/. Files next to the
// binary are always the "source package" from the admin panel, so they
// always overwrite what's in the data dir to keep things in sync when users
// redeploy with a new server/project.
func firstRunSetup() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exeDir := filepath.Dir(exePath)

	filesToCopy := []string{"credentials.json", "credentials.json.token", "client_config.json"}
	for _, name := range filesToCopy {
		src := filepath.Join(exeDir, name)
		if !fileExists(src) {
			continue
		}
		dst := filepath.Join(appDataDir, name)
		srcData, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		// Skip copy if source and destination are identical
		if dstData, err := os.ReadFile(dst); err == nil && bytes.Equal(srcData, dstData) {
			continue
		}
		perm := os.FileMode(0644)
		if name == "credentials.json" || name == "credentials.json.token" {
			perm = 0600
		}
		if err := os.WriteFile(dst, srcData, perm); err == nil {
			log.Printf("Setup: synced %s → %s", name, appDataDir)
		}
	}
}

// safeWriter wraps a writer and swallows errors (for Windows GUI where stderr is invalid).
type safeWriter struct{ w io.Writer }

func (s safeWriter) Write(p []byte) (int, error) {
	s.w.Write(p)
	return len(p), nil
}

func onReady() {
	log.Println("onReady called")
	logs := NewLogBuffer(1000)
	writers := []io.Writer{logs, safeWriter{os.Stderr}}
	if startupLog != nil {
		writers = append(writers, startupLog)
	}
	log.SetOutput(io.MultiWriter(writers...))
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
	mAutoStart := systray.AddMenuItemCheckbox("Start at Login", "Launch Undertow at login", isAutoStartEnabled())

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

			case <-mAutoStart.ClickedCh:
				if mAutoStart.Checked() {
					if err := disableAutoStart(); err != nil {
						log.Printf("Failed to disable auto-start: %v", err)
					} else {
						mAutoStart.Uncheck()
						log.Println("Auto-start disabled")
					}
				} else {
					if err := enableAutoStart(); err != nil {
						log.Printf("Failed to enable auto-start: %v", err)
					} else {
						mAutoStart.Check()
						log.Println("Auto-start enabled")
					}
				}

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
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	radius := float64(size)/2 - 4

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
				if dist <= radius && dist >= radius-2.5 {
					img.Set(x, y, color.RGBA{r, g, b, 200})
				}
			}
		}
	}

	return encodeIcon(img)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
