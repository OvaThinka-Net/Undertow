package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed dashboard.html
var dashboardHTML []byte

type Dashboard struct {
	tunnel       *Tunnel
	logs         *LogBuffer
	dataDir      string
	port         int
	onConnect    func() error
	onDisconnect func()
}

func NewDashboard(tunnel *Tunnel, logs *LogBuffer, dataDir string) *Dashboard {
	return &Dashboard{
		tunnel:  tunnel,
		logs:    logs,
		dataDir: dataDir,
	}
}

func (d *Dashboard) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	d.port = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/", d.handleIndex)
	mux.HandleFunc("/api/status", d.handleStatus)
	mux.HandleFunc("/api/connect", d.handleConnect)
	mux.HandleFunc("/api/disconnect", d.handleDisconnect)
	mux.HandleFunc("/api/logs", d.handleLogs)
	mux.HandleFunc("/api/config", d.handleConfig)
	mux.HandleFunc("/api/autostart", d.handleAutoStart)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	go srv.Serve(ln)
	return nil
}

func (d *Dashboard) Open() {
	openBrowser(fmt.Sprintf("http://localhost:%d", d.port))
}

func (d *Dashboard) URL() string {
	return fmt.Sprintf("http://localhost:%d", d.port)
}

func (d *Dashboard) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func (d *Dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"connected":  d.tunnel.IsRunning(),
		"has_creds":  fileExists(filepath.Join(d.dataDir, "credentials.json")),
		"has_token":  fileExists(filepath.Join(d.dataDir, "credentials.json.token")),
		"has_config": fileExists(filepath.Join(d.dataDir, "client_config.json")),
		"autostart":  isAutoStartEnabled(),
		"os":         runtime.GOOS,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (d *Dashboard) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if d.onConnect != nil {
		if err := d.onConnect(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]string{"ok": "connected"})
}

func (d *Dashboard) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if d.onDisconnect != nil {
		d.onDisconnect()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"ok": "disconnected"})
}

func (d *Dashboard) handleLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, entry := range d.logs.Lines() {
		data, _ := json.Marshal(entry)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
	}
	flusher.Flush()

	ch := d.logs.Subscribe()
	defer d.logs.Unsubscribe(ch)

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case entry := <-ch:
			data, _ := json.Marshal(entry)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (d *Dashboard) handleConfig(w http.ResponseWriter, r *http.Request) {
	configPath := filepath.Join(d.dataDir, "client_config.json")
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(configPath)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": "config not found"})
			return
		}
		w.Write(data)

	case http.MethodPost:
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON"})
			return
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		tmpPath := configPath + ".tmp"
		if err := os.WriteFile(tmpPath, pretty, 0644); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if err := os.Rename(tmpPath, configPath); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "saved"})

	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (d *Dashboard) handleAutoStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"enabled":   isAutoStartEnabled(),
			"supported": runtime.GOOS == "darwin",
		})
	case http.MethodPost:
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
			return
		}
		var err error
		if body.Enabled {
			err = enableAutoStart()
		} else {
			err = disableAutoStart()
		}
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		log.Printf("Auto-start %s", map[bool]string{true: "enabled", false: "disabled"}[body.Enabled])
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "enabled": body.Enabled})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
