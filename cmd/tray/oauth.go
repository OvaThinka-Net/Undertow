package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type oauthCreds struct {
	Installed struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		AuthURI      string   `json:"auth_uri"`
		RedirectURIs []string `json:"redirect_uris"`
	} `json:"installed"`
}

func needsOAuth(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "credentials.json.token"))
	return os.IsNotExist(err)
}

func doOAuth(dataDir string) error {
	credsPath := filepath.Join(dataDir, "credentials.json")
	data, err := os.ReadFile(credsPath)
	if err != nil {
		return fmt.Errorf("credentials.json not found")
	}

	var creds oauthCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("invalid credentials.json: %w", err)
	}

	// Find a free port for the local callback server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("no free port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	redirectURI := fmt.Sprintf("http://localhost:%d", port)

	authURI := creds.Installed.AuthURI
	if authURI == "" {
		authURI = "https://accounts.google.com/o/oauth2/auth"
	}

	authURL := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=https://www.googleapis.com/auth/drive.file&access_type=offline",
		authURI, url.QueryEscape(creds.Installed.ClientID), url.QueryEscape(redirectURI))

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			if errMsg == "" {
				errMsg = "unknown error"
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><body style="font-family:system-ui;text-align:center;padding:60px;background:#0d1117;color:#c9d1d9">
				<h2 style="color:#f85149">✗ Authorization Failed</h2><p>%s</p></body></html>`, errMsg)
			errCh <- fmt.Errorf("auth denied: %s", errMsg)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body style="font-family:system-ui;text-align:center;padding:60px;background:#0d1117;color:#c9d1d9">
			<h2 style="color:#3fb950">✓ Authorization Successful</h2>
			<p>You can close this tab and return to Undertow.</p></body></html>`)
		codeCh <- code
	})

	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())

	// Open browser
	openBrowser(authURL)

	// Wait for callback
	select {
	case code := <-codeCh:
		return exchangeAndSave(creds, code, redirectURI, dataDir)
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timed out waiting for authorization (5 min)")
	}
}

func exchangeAndSave(creds oauthCreds, code, redirectURI, dataDir string) error {
	v := url.Values{}
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("client_id", creds.Installed.ClientID)
	v.Set("client_secret", creds.Installed.ClientSecret)
	v.Set("redirect_uri", redirectURI)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", v)
	if err != nil {
		return fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	json.Unmarshal(body, &tokenResp)

	if tokenResp.Error != "" {
		return fmt.Errorf("%s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.RefreshToken == "" {
		return fmt.Errorf("no refresh token received")
	}

	tokenData, _ := json.MarshalIndent(map[string]string{
		"refresh_token": tokenResp.RefreshToken,
	}, "", "  ")

	tokenPath := filepath.Join(dataDir, "credentials.json.token")
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	return nil
}

func openBrowser(u string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", u).Start()
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		exec.Command("xdg-open", u).Start()
	}
}

func openFolder(path string) {
	switch runtime.GOOS {
	case "darwin":
		exec.Command("open", path).Start()
	case "windows":
		exec.Command("explorer", path).Start()
	default:
		exec.Command("xdg-open", path).Start()
	}
}
