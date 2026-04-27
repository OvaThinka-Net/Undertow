package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/OvaThinka-Net/Undertow/internal/config"
	"github.com/OvaThinka-Net/Undertow/internal/httpclient"
	"github.com/OvaThinka-Net/Undertow/internal/storage"
	"github.com/OvaThinka-Net/Undertow/internal/transport"
	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"
)

var Version = "dev"

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type rawResolver struct{}

func (rawResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Defends comprehensively against Local DNS leaks by doing absolutely nothing.
	return ctx, nil, nil
}

func resolveNextToExe(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	alt := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return ""
}

func resolveFile(names ...string) string {
	// Try CWD first
	for _, n := range names {
		if _, err := os.Stat(n); err == nil {
			return n
		}
	}
	// Try next to executable
	for _, n := range names {
		if alt := resolveNextToExe(n); alt != "" {
			return alt
		}
	}
	// Return first name as default (will produce a clear error)
	return names[0]
}

func main() {
	var configPath, gcPath string
	flag.StringVar(&configPath, "c", "", "Path to config file")
	flag.StringVar(&gcPath, "gc", "", "Path to Google credentials JSON")
	flag.Parse()

	// Resolve config: try CWD then next to executable, for both config.json and client_config.json
	if configPath == "" {
		configPath = resolveFile("client_config.json", "config.json")
	} else if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if alt := resolveNextToExe(configPath); alt != "" {
			configPath = alt
		}
	}
	if gcPath == "" {
		gcPath = resolveFile("credentials.json")
	} else if _, err := os.Stat(gcPath); os.IsNotExist(err) {
		if alt := resolveNextToExe(gcPath); alt != "" {
			gcPath = alt
		}
	}

	log.Println("Starting Flow Client...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appCfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	var inner storage.Backend
	if appCfg.StorageType == "google" {
		customHttpClient := httpclient.NewCustomClient(appCfg.Transport)
		inner = storage.NewGoogleBackend(customHttpClient, gcPath, appCfg.GoogleFolderID)
	} else {
		inner, err = storage.NewLocalBackend(appCfg.LocalDir)
		if err != nil {
			log.Fatalf("Failed to init local storage: %v", err)
		}
	}
	if err := inner.Login(ctx); err != nil {
		log.Fatalf("Backend login failed: %v", err)
	}
	backend := storage.NewRetryBackend(inner)

	// AUTOMATION: If folder ID is missing, find or create it
	if appCfg.StorageType == "google" && appCfg.GoogleFolderID == "" {
		folderName := appCfg.FolderName()
		log.Printf("Zero-Config: Searching for existing Google Drive folder '%s'...", folderName)
		folderID, err := backend.FindFolder(ctx, folderName)
		if err != nil {
			log.Fatalf("Failed to search for folder: %v", err)
		}

		if folderID == "" {
			log.Printf("Zero-Config: '%s' not found. Creating new folder...", folderName)
			folderID, err = backend.CreateFolder(ctx, folderName)
			if err != nil {
				log.Fatalf("Failed to auto-create folder: %v", err)
			}
		} else {
			log.Printf("Zero-Config: Found existing folder with ID %s", folderID)
		}

		appCfg.GoogleFolderID = folderID
		if err := appCfg.Save(configPath); err != nil {
			log.Printf("Warning: Failed to save folder ID to %s: %v", configPath, err)
		} else {
			log.Printf("Zero-Config: Config updated with folder ID %s", folderID)
		}
	}

	cid := appCfg.ClientID
	if cid == "" {
		cid = generateSessionID()[:8] // Short random ID as fallback
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

	// Create the library SOCKS5 server wrapping our custom Google Drive Engine tunnel
	server := socks5.NewServer(
		socks5.WithDial(func(dc context.Context, network, addr string) (net.Conn, error) {
			sessionID := generateSessionID()

			// Intelligently parse the address string to warn users if their browser is natively leaking DNS
			host, port, err := net.SplitHostPort(addr)
			if err == nil {
				if net.ParseIP(host) != nil {
					log.Printf("New covert session %s targeting RAW IP %s:%s (Warning: Local DNS Leak?)", sessionID, host, port)
				} else {
					log.Printf("New covert session %s targeting SECURE DOMAIN %s:%s", sessionID, host, port)
				}
			} else {
				log.Printf("New covert session %s targeting %s", sessionID, addr)
			}

			session := transport.NewSession(sessionID)
			session.TargetAddr = addr
			engine.AddSession(session)

			// Instantly ping a blank payload so the remote end opens the actual TCP destination
			session.EnqueueTx(nil)

			return transport.NewVirtualConn(session, engine), nil
		}),
		socks5.WithAssociateHandle(func(ctx context.Context, w io.Writer, req *socks5.Request) error {
			// Explicitly block UDP routing to confidently prevent ISP endpoint leakage
			socks5.SendReply(w, statute.RepCommandNotSupported, nil)
			return fmt.Errorf("covert UDP not supported")
		}),
		// DEFEND AGAINST LOCAL DNS LEAKS:
		// The library natively performs system DNS lookups for all FQDNs before proxying!
		// We explicitly override the resolver with a NoOp dummy to force raw strings into the pipe.
		socks5.WithResolver(rawResolver{}),
	)

	log.Printf("Listening for SOCKS5 on %s...", listenAddr)

	go func() {
		if err := server.ListenAndServe("tcp", listenAddr); err != nil {
			log.Fatalf("SOCKS5 server failed: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down client...")
	cancel()
}
