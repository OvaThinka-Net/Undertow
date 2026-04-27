package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/OvaThinka-Net/Undertow/internal/config"
	"github.com/OvaThinka-Net/Undertow/internal/httpclient"
	"github.com/OvaThinka-Net/Undertow/internal/storage"
	"github.com/OvaThinka-Net/Undertow/internal/transport"
)

var Version = "dev"

func main() {
	var configPath, gcPath string
	flag.StringVar(&configPath, "c", "config.json", "Path to config file")
	flag.StringVar(&gcPath, "gc", "credentials.json", "Path to Google Service Account JSON")
	flag.Parse()

	log.Println("Starting Flow Server...")
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

	engine := transport.NewEngine(backend, false, "")
	if appCfg.RefreshRateMs > 0 {
		engine.SetPollRate(appCfg.RefreshRateMs)
	}
	if appCfg.FlushRateMs > 0 {
		engine.SetFlushRate(appCfg.FlushRateMs)
	}

	// Called by polling loop when a new incoming session file is found
	engine.OnNewSession = func(sessionID, targetAddr string, session *transport.Session) {
		log.Printf("Server received new session %s destined for %s", sessionID, targetAddr)
		go handleServerConn(sessionID, targetAddr, session, engine)
	}

	engine.Start(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down server...")
	cancel()
}

// isPrivateIP returns true if the IP is in a private, loopback, or reserved range.
func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func handleServerConn(sessionID, targetAddr string, session *transport.Session, engine *transport.Engine) {
	defer engine.RemoveSession(sessionID)

	// SSRF protection: resolve the target and reject private/reserved IPs
	host, port, err := net.SplitHostPort(targetAddr)
	if err != nil {
		log.Printf("Invalid target address %s: %v", targetAddr, err)
		return
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		log.Printf("DNS lookup failed for %s: %v", host, err)
		return
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			log.Printf("BLOCKED session %s: destination %s resolves to private IP %s", sessionID, targetAddr, ip)
			// Send an HTTP redirect to a fun page so the browser doesn't just hang
			redirect := "HTTP/1.1 302 Found\r\n" +
				"Location: https://www.google.com/teapot\r\n" +
				"Content-Length: 0\r\n" +
				"Connection: close\r\n\r\n"
			session.EnqueueTx([]byte(redirect))
			return
		}
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 10*time.Second)
	if err != nil {
		log.Printf("Dial error to %s: %v", targetAddr, err)
		return
	}
	defer conn.Close()

	errCh := make(chan error, 2)

	// Conn -> Tx (Res)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				session.EnqueueTx(buf[:n])
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Rx (Req) -> Conn
	go func() {
		for {
			data, ok := <-session.RxChan
			if !ok {
				errCh <- fmt.Errorf("session closed by remote")
				return
			}
			if len(data) > 0 {
				if _, err := conn.Write(data); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	<-errCh
}
