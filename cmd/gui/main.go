package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/OvaThinka-Net/Undertow/internal/config"
	"github.com/OvaThinka-Net/Undertow/internal/httpclient"
	"github.com/OvaThinka-Net/Undertow/internal/storage"
	"github.com/OvaThinka-Net/Undertow/internal/transport"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

//go:embed dashboard.html
var dashboardHTML []byte

var Version = "dev"

// ---------- Log Buffer ----------

type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type LogBuffer struct {
	mu    sync.Mutex
	lines []LogEntry
	max   int
	subs  map[chan LogEntry]struct{}
}

func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{max: max, subs: make(map[chan LogEntry]struct{})}
}

func (lb *LogBuffer) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	lb.Add(msg)
	return len(p), nil
}

func (lb *LogBuffer) Add(msg string) {
	entry := LogEntry{Time: time.Now(), Message: msg}
	lb.mu.Lock()
	lb.lines = append(lb.lines, entry)
	if len(lb.lines) > lb.max {
		lb.lines = lb.lines[len(lb.lines)-lb.max:]
	}
	subs := make([]chan LogEntry, 0, len(lb.subs))
	for ch := range lb.subs {
		subs = append(subs, ch)
	}
	lb.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (lb *LogBuffer) Lines() []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := make([]LogEntry, len(lb.lines))
	copy(out, lb.lines)
	return out
}

func (lb *LogBuffer) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 64)
	lb.mu.Lock()
	lb.subs[ch] = struct{}{}
	lb.mu.Unlock()
	return ch
}

func (lb *LogBuffer) Unsubscribe(ch chan LogEntry) {
	lb.mu.Lock()
	delete(lb.subs, ch)
	lb.mu.Unlock()
}

// ---------- Tunnel ----------

type Tunnel struct {
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	listener net.Listener
	dataDir  string
}

func (t *Tunnel) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

func (t *Tunnel) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return fmt.Errorf("already connected")
	}

	configPath := filepath.Join(t.dataDir, "client_config.json")
	credsPath := filepath.Join(t.dataDir, "credentials.json")

	appCfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	customHttpClient := httpclient.NewCustomClient(appCfg.Transport)
	inner := storage.NewGoogleBackend(customHttpClient, credsPath, appCfg.GoogleFolderID)
	if err := inner.Login(ctx); err != nil {
		cancel()
		return fmt.Errorf("login: %w", err)
	}
	backend := storage.NewRetryBackend(inner)

	if appCfg.StorageType == "google" && appCfg.GoogleFolderID == "" {
		folderName := appCfg.FolderName()
		log.Printf("Searching for folder '%s'...", folderName)
		folderID, err := backend.FindFolder(ctx, folderName)
		if err != nil {
			cancel()
			return fmt.Errorf("find folder: %w", err)
		}
		if folderID == "" {
			cancel()
			return fmt.Errorf("folder '%s' not found", folderName)
		}
		appCfg.GoogleFolderID = folderID
		appCfg.Save(configPath)
		log.Printf("Found folder: %s", folderID)
	}

	cid := appCfg.ClientID
	if cid == "" {
		cid = generateSessionID()[:8]
	}
	engine := transport.NewEngine(backend, true, cid)
	if appCfg.RefreshRateMs > 0 {
		engine.SetPollRate(appCfg.RefreshRateMs)
	}
	if appCfg.FlushRateMs > 0 {
		engine.SetFlushRate(appCfg.FlushRateMs)
	}
	engine.Start(ctx)

	listenAddr := appCfg.ListenAddr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:1080"
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		cancel()
		return fmt.Errorf("port %s in use", listenAddr)
	}

	server := socks5.NewServer(
		socks5.WithDial(func(dc context.Context, network, addr string) (net.Conn, error) {
			sessionID := generateSessionID()
			host, port, parseErr := net.SplitHostPort(addr)
			if parseErr == nil {
				if net.ParseIP(host) != nil {
					log.Printf("Session %s → %s:%s (raw IP)", sessionID[:8], host, port)
				} else {
					log.Printf("Session %s → %s:%s", sessionID[:8], host, port)
				}
			}
			session := transport.NewSession(sessionID)
			session.TargetAddr = addr
			engine.AddSession(session)
			session.EnqueueTx(nil)
			return transport.NewVirtualConn(session, engine), nil
		}),
		socks5.WithAssociateHandle(func(ctx context.Context, w io.Writer, req *socks5.Request) error {
			socks5.SendReply(w, statute.RepCommandNotSupported, nil)
			return fmt.Errorf("UDP not supported")
		}),
		socks5.WithResolver(rawResolver{}),
	)

	go server.Serve(listener)

	t.running = true
	t.cancel = cancel
	t.listener = listener
	log.Printf("Connected — SOCKS5 on %s", listenAddr)
	return nil
}

func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	if t.listener != nil {
		t.listener.Close()
	}
	if t.cancel != nil {
		t.cancel()
	}
	t.running = false
	log.Println("Disconnected")
}

// ---------- Dashboard HTTP ----------

func startDashboard(tunnel *Tunnel, logs *LogBuffer, dataDir string) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("Dashboard listen failed: %v", err)
		return 0
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(dashboardHTML)
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":  tunnel.IsRunning(),
			"has_creds":  fileExists(filepath.Join(dataDir, "credentials.json")),
			"has_token":  fileExists(filepath.Join(dataDir, "credentials.json.token")),
			"has_config": fileExists(filepath.Join(dataDir, "client_config.json")),
			"version":    Version,
			"autostart":  isAutoStartEnabled(),
			"os":         runtime.GOOS,
		})
	})

	mux.HandleFunc("/api/autostart", func(w http.ResponseWriter, r *http.Request) {
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
	})

	mux.HandleFunc("/api/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := tunnel.Start(); err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "connected"})
	})

	mux.HandleFunc("/api/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		tunnel.Stop()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"ok": "disconnected"})
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		for _, entry := range logs.Lines() {
			data, _ := json.Marshal(entry)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		flusher.Flush()

		ch := logs.Subscribe()
		defer logs.Unsubscribe(ch)

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
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		configPath := filepath.Join(dataDir, "client_config.json")
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
			os.Rename(tmpPath, configPath)
			json.NewEncoder(w).Encode(map[string]string{"ok": "saved"})
		default:
			http.Error(w, "method not allowed", 405)
		}
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
	}
	go srv.Serve(ln)
	return port
}

// ---------- Helpers ----------

type rawResolver struct{}

func (rawResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", url).Start()
	case "windows":
		exec.Command("cmd", "/c", "start", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

// safeWriter wraps a writer and swallows errors (for Windows GUI where stderr is invalid).
type safeWriter struct{ w io.Writer }

func (s safeWriter) Write(p []byte) (int, error) {
	n, _ := s.w.Write(p)
	if n == 0 {
		n = len(p)
	}
	return n, nil
}

// ---------- Main ----------

func main() {
	headless := flag.Bool("headless", false, "Run without opening browser")
	flag.Parse()

	logs := NewLogBuffer(1000)
	log.SetOutput(io.MultiWriter(logs, safeWriter{os.Stderr}))
	log.SetFlags(log.Ltime)

	// Resolve data directory: use dir containing the executable
	exePath, _ := os.Executable()
	dataDir := filepath.Dir(exePath)

	// If credentials not next to exe, try CWD
	if !fileExists(filepath.Join(dataDir, "credentials.json")) {
		if cwd, err := os.Getwd(); err == nil {
			if fileExists(filepath.Join(cwd, "credentials.json")) {
				dataDir = cwd
			}
		}
	}

	tunnel := &Tunnel{dataDir: dataDir}

	port := startDashboard(tunnel, logs, dataDir)
	if port > 0 {
		url := fmt.Sprintf("http://localhost:%d", port)
		log.Printf("Dashboard: %s", url)
		if !*headless {
			openBrowser(url)
		}
	}

	log.Printf("Undertow GUI %s — waiting for connection...", Version)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	if tunnel.IsRunning() {
		tunnel.Stop()
	}
	log.Println("Shutting down")
}
