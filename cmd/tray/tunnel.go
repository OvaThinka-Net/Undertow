package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/OvaThinka-Net/Undertow/internal/config"
	"github.com/OvaThinka-Net/Undertow/internal/httpclient"
	"github.com/OvaThinka-Net/Undertow/internal/storage"
	"github.com/OvaThinka-Net/Undertow/internal/transport"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

type rawResolver struct{}

func (rawResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

type Tunnel struct {
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	listener net.Listener
	dataDir  string
}

func NewTunnel(dataDir string) *Tunnel {
	return &Tunnel{dataDir: dataDir}
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

	configPath := t.dataDir + "/client_config.json"
	credsPath := t.dataDir + "/credentials.json"

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

	// Auto-discover folder
	if appCfg.GoogleFolderID == "" {
		folderName := appCfg.FolderName()
		log.Printf("Searching for %s folder...", folderName)
		folderID, err := backend.FindFolder(ctx, folderName)
		if err != nil {
			cancel()
			return fmt.Errorf("find folder: %w", err)
		}
		if folderID == "" {
			cancel()
			return fmt.Errorf("%s folder not found — ask the server admin to share it with your Google account", folderName)
		}
		appCfg.GoogleFolderID = folderID
		appCfg.Save(configPath)
		log.Printf("Found folder: %s", folderID)
	}

	cid := generateSessionID()[:8]
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
		return fmt.Errorf("port %s in use — is another client running?", listenAddr)
	}

	server := socks5.NewServer(
		socks5.WithDial(func(dc context.Context, network, addr string) (net.Conn, error) {
			sessionID := generateSessionID()
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

	log.Printf("Tunnel connected — SOCKS5 on %s", listenAddr)
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
	log.Println("Tunnel disconnected")
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
