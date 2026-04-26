package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed static/index.html
var indexHTML []byte

//go:embed static/login.html
var loginHTML []byte

//go:embed config.default.json
var defaultConfigJSON []byte

// ---------- Config ----------

type AdminConfig struct {
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	SessionHours    int    `json:"session_hours"`
	ServerBin       string `json:"server_bin"`
	ServerConfig    string `json:"server_config"`
	CredentialsFile string `json:"credentials_file"`
}

func loadConfig(path string) AdminConfig {
	var cfg AdminConfig
	json.Unmarshal(defaultConfigJSON, &cfg)

	// Try explicit path first, then admin_config.json / config.json next to binary, then CWD
	paths := []string{path}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		paths = append(paths, filepath.Join(dir, "admin_config.json"))
		paths = append(paths, filepath.Join(dir, "config.json"))
	}
	paths = append(paths, "admin_config.json", "config.json")

	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		json.Unmarshal(data, &cfg)
		log.Printf("Loaded config from %s", p)
		break
	}

	// Enforce minimums
	if cfg.Port == 0 {
		cfg.Port = 8090
	}
	if cfg.Username == "" {
		cfg.Username = "admin"
	}
	if cfg.SessionHours == 0 {
		cfg.SessionHours = 168
	}
	if cfg.ServerBin == "" {
		cfg.ServerBin = "server"
	}
	if cfg.ServerConfig == "" {
		cfg.ServerConfig = "server_config.json"
	}
	if cfg.CredentialsFile == "" {
		cfg.CredentialsFile = "credentials.json"
	}

	// Generate random password if not set
	if cfg.Password == "" {
		b := make([]byte, 8)
		rand.Read(b)
		cfg.Password = hex.EncodeToString(b)
	}

	return cfg
}

// ---------- Log Buffer ----------

type LogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
	Source  string `json:"source"` // stdout, stderr, admin
}

type LogBuffer struct {
	mu      sync.RWMutex
	entries []LogEntry
	subsMu  sync.Mutex
	subs    map[chan LogEntry]struct{}
}

func NewLogBuffer() *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, 0, 2000),
		subs:    make(map[chan LogEntry]struct{}),
	}
}

func (lb *LogBuffer) Add(source, message string) {
	entry := LogEntry{
		Time:    time.Now().Format("2006/01/02 15:04:05"),
		Message: message,
		Source:  source,
	}
	lb.mu.Lock()
	if len(lb.entries) >= 2000 {
		lb.entries = lb.entries[1:]
	}
	lb.entries = append(lb.entries, entry)
	lb.mu.Unlock()

	lb.subsMu.Lock()
	for ch := range lb.subs {
		select {
		case ch <- entry:
		default:
		}
	}
	lb.subsMu.Unlock()
}

func (lb *LogBuffer) History() []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	out := make([]LogEntry, len(lb.entries))
	copy(out, lb.entries)
	return out
}

func (lb *LogBuffer) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 64)
	lb.subsMu.Lock()
	lb.subs[ch] = struct{}{}
	lb.subsMu.Unlock()
	return ch
}

func (lb *LogBuffer) Unsubscribe(ch chan LogEntry) {
	lb.subsMu.Lock()
	delete(lb.subs, ch)
	lb.subsMu.Unlock()
}

// ---------- Process Manager ----------

type ProcessManager struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	running      bool
	pid          int
	startedAt    time.Time
	done         chan struct{}
	logs         *LogBuffer
	workDir      string
	serverBin    string
	serverConfig string
	credsFile    string
}

func NewProcessManager(workDir string, cfg AdminConfig) *ProcessManager {
	return &ProcessManager{
		workDir:      workDir,
		logs:         NewLogBuffer(),
		serverBin:    cfg.ServerBin,
		serverConfig: cfg.ServerConfig,
		credsFile:    cfg.CredentialsFile,
	}
}

func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		return fmt.Errorf("server is already running")
	}

	serverBin := filepath.Join(pm.workDir, pm.serverBin)
	if _, err := os.Stat(serverBin); os.IsNotExist(err) {
		return fmt.Errorf("server binary not found")
	}
	if _, err := os.Stat(filepath.Join(pm.workDir, pm.serverConfig)); os.IsNotExist(err) {
		return fmt.Errorf("%s not found", pm.serverConfig)
	}
	if _, err := os.Stat(filepath.Join(pm.workDir, pm.credsFile)); os.IsNotExist(err) {
		return fmt.Errorf("%s not found — upload it first", pm.credsFile)
	}
	if _, err := os.Stat(filepath.Join(pm.workDir, pm.credsFile+".token")); os.IsNotExist(err) {
		return fmt.Errorf("no OAuth token — complete OAuth setup first")
	}

	cmd := exec.Command(serverBin, "-c", pm.serverConfig, "-gc", pm.credsFile)
	cmd.Dir = pm.workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start: %w", err)
	}

	pm.cmd = cmd
	pm.running = true
	pm.pid = cmd.Process.Pid
	pm.startedAt = time.Now()
	pm.done = make(chan struct{})

	pm.logs.Add("admin", fmt.Sprintf("Server started (PID %d)", pm.pid))

	go pm.pipeReader(stdout, "server")
	go pm.pipeReader(stderr, "server")
	go func() {
		cmd.Wait()
		pm.mu.Lock()
		pm.running = false
		pm.pid = 0
		pm.mu.Unlock()
		pm.logs.Add("admin", "Server process exited")
		close(pm.done)
	}()

	return nil
}

func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	if !pm.running {
		pm.mu.Unlock()
		return fmt.Errorf("server is not running")
	}
	done := pm.done
	process := pm.cmd.Process
	pm.mu.Unlock()

	pm.logs.Add("admin", "Stopping server...")
	process.Signal(syscall.SIGTERM)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		process.Kill()
		<-done
	}
	return nil
}

func (pm *ProcessManager) pipeReader(pipe io.ReadCloser, source string) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		pm.logs.Add(source, scanner.Text())
	}
}

type StatusResponse struct {
	Running      bool   `json:"running"`
	PID          int    `json:"pid"`
	Uptime       string `json:"uptime"`
	ConfigExists bool   `json:"config_exists"`
	CredsExists  bool   `json:"creds_exists"`
	TokenExists  bool   `json:"token_exists"`
	ServerExists bool   `json:"server_exists"`
}

func (pm *ProcessManager) Status() StatusResponse {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var uptime string
	if pm.running {
		uptime = time.Since(pm.startedAt).Round(time.Second).String()
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(pm.workDir, name))
		return err == nil
	}

	return StatusResponse{
		Running:      pm.running,
		PID:          pm.pid,
		Uptime:       uptime,
		ConfigExists: exists(pm.serverConfig),
		CredsExists:  exists(pm.credsFile),
		TokenExists:  exists(pm.credsFile + ".token"),
		ServerExists: exists(pm.serverBin),
	}
}

// ---------- HTTP Handlers ----------

func (pm *ProcessManager) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}

func (pm *ProcessManager) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, pm.Status())
}

func (pm *ProcessManager) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := pm.Start(); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"ok": "server started"})
}

func (pm *ProcessManager) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := pm.Stop(); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"ok": "server stopped"})
}

func (pm *ProcessManager) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	pm.Stop() // ignore error if not running
	time.Sleep(500 * time.Millisecond)
	if err := pm.Start(); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"ok": "server restarted"})
}

func (pm *ProcessManager) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfgPath := filepath.Join(pm.workDir, pm.serverConfig)

	if r.Method == http.MethodGet {
		data, err := os.ReadFile(cfgPath)
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"storage_type":"google","refresh_rate_ms":200,"flush_rate_ms":300}`))
			return
		} else if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	}

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, 10240))
		if err != nil {
			writeJSON(w, map[string]string{"error": "failed to read body"})
			return
		}
		var check json.RawMessage
		if json.Unmarshal(body, &check) != nil {
			writeJSON(w, map[string]string{"error": "invalid JSON"})
			return
		}
		// Pretty-print
		var pretty map[string]interface{}
		json.Unmarshal(body, &pretty)
		formatted, _ := json.MarshalIndent(pretty, "", "  ")

		if err := os.WriteFile(cfgPath, append(formatted, '\n'), 0644); err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		pm.logs.Add("admin", "Configuration saved")
		writeJSON(w, map[string]string{"ok": "config saved"})
		return
	}
	http.Error(w, "method not allowed", 405)
}

func (pm *ProcessManager) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768) // 32KB max
	if err := r.ParseMultipartForm(32768); err != nil {
		writeJSON(w, map[string]string{"error": "upload too large or invalid"})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, map[string]string{"error": "no file in upload"})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, map[string]string{"error": "failed to read file"})
		return
	}

	// Validate it's proper OAuth JSON
	var oauthJSON struct {
		Installed struct {
			ClientID string `json:"client_id"`
		} `json:"installed"`
	}
	if json.Unmarshal(data, &oauthJSON) != nil || oauthJSON.Installed.ClientID == "" {
		writeJSON(w, map[string]string{"error": "invalid credentials JSON — must have installed.client_id"})
		return
	}

	if err := os.WriteFile(filepath.Join(pm.workDir, pm.credsFile), data, 0600); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	pm.logs.Add("admin", "Credentials uploaded")
	writeJSON(w, map[string]string{"ok": "credentials saved"})
}

// ---------- OAuth Handlers ----------

type oauthCreds struct {
	Installed struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		AuthURI      string   `json:"auth_uri"`
		RedirectURIs []string `json:"redirect_uris"`
	} `json:"installed"`
}

func (pm *ProcessManager) readOAuthCreds() (*oauthCreds, error) {
	data, err := os.ReadFile(filepath.Join(pm.workDir, pm.credsFile))
	if err != nil {
		return nil, fmt.Errorf("credentials.json not found — upload it first")
	}
	var creds oauthCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("invalid credentials.json: %w", err)
	}
	return &creds, nil
}

func (pm *ProcessManager) handleOAuthURL(w http.ResponseWriter, r *http.Request) {
	creds, err := pm.readOAuthCreds()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	redirectURI := "http://localhost"
	if len(creds.Installed.RedirectURIs) > 0 {
		redirectURI = creds.Installed.RedirectURIs[0]
	}
	authURI := creds.Installed.AuthURI
	if authURI == "" {
		authURI = "https://accounts.google.com/o/oauth2/auth"
	}

	link := fmt.Sprintf("%s?client_id=%s&redirect_uri=%s&response_type=code&scope=https://www.googleapis.com/auth/drive.file&access_type=offline",
		authURI, url.QueryEscape(creds.Installed.ClientID), url.QueryEscape(redirectURI))

	writeJSON(w, map[string]string{"url": link})
}

func (pm *ProcessManager) handleOAuthExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "invalid request"})
		return
	}

	// Extract code from URL or raw code
	code := strings.TrimSpace(req.Input)
	if strings.HasPrefix(code, "http") {
		u, err := url.Parse(code)
		if err == nil {
			if qc := u.Query().Get("code"); qc != "" {
				code = qc
			}
		}
	}
	if code == "" {
		writeJSON(w, map[string]string{"error": "no authorization code found"})
		return
	}

	creds, err := pm.readOAuthCreds()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	redirectURI := "http://localhost"
	if len(creds.Installed.RedirectURIs) > 0 {
		redirectURI = creds.Installed.RedirectURIs[0]
	}

	// Exchange code for token
	v := url.Values{}
	v.Set("grant_type", "authorization_code")
	v.Set("code", code)
	v.Set("client_id", creds.Installed.ClientID)
	v.Set("client_secret", creds.Installed.ClientSecret)
	v.Set("redirect_uri", redirectURI)

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", v)
	if err != nil {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("token request failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	json.NewDecoder(resp.Body).Decode(&tokenResp)

	if tokenResp.Error != "" {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("%s: %s", tokenResp.Error, tokenResp.ErrorDesc)})
		return
	}
	if tokenResp.RefreshToken == "" {
		writeJSON(w, map[string]string{"error": "no refresh token received"})
		return
	}

	// Save token
	tokenData, _ := json.MarshalIndent(map[string]string{"refresh_token": tokenResp.RefreshToken}, "", "  ")
	tokenPath := filepath.Join(pm.workDir, pm.credsFile+".token")
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("failed to save token: %v", err)})
		return
	}

	pm.logs.Add("admin", "OAuth token saved successfully")
	writeJSON(w, map[string]string{"ok": "authenticated"})
}

// ---------- Google Drive Helpers ----------

func (pm *ProcessManager) driveAccessToken() (string, error) {
	creds, err := pm.readOAuthCreds()
	if err != nil {
		return "", err
	}
	tokenData, err := os.ReadFile(filepath.Join(pm.workDir, pm.credsFile+".token"))
	if err != nil {
		return "", fmt.Errorf("no OAuth token — complete OAuth setup first")
	}
	var saved struct {
		RefreshToken string `json:"refresh_token"`
	}
	json.Unmarshal(tokenData, &saved)
	if saved.RefreshToken == "" {
		return "", fmt.Errorf("invalid token file")
	}

	v := url.Values{}
	v.Set("grant_type", "refresh_token")
	v.Set("refresh_token", saved.RefreshToken)
	v.Set("client_id", creds.Installed.ClientID)
	v.Set("client_secret", creds.Installed.ClientSecret)

	resp, err := http.PostForm("https://www.googleapis.com/oauth2/v4/token", v)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&tok)
	if tok.Error != "" {
		return "", fmt.Errorf("token refresh failed: %s", tok.Error)
	}
	return tok.AccessToken, nil
}

func (pm *ProcessManager) handleSetupFolder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	accessToken, err := pm.driveAccessToken()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}

	// Read optional folder name from request body
	var reqBody struct {
		FolderName string `json:"folder_name"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&reqBody)
	folderName := reqBody.FolderName
	if folderName == "" {
		folderName = "Flow-Data"
	}

	// Check if folder already exists
	q := fmt.Sprintf("name = '%s' and mimeType = 'application/vnd.google-apps.folder' and trashed = false", folderName)
	u, _ := url.Parse("https://www.googleapis.com/drive/v3/files")
	qv := u.Query()
	qv.Set("q", q)
	qv.Set("fields", "files(id, name)")
	u.RawQuery = qv.Encode()

	req, _ := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	listResp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("Drive API error: %v", err)})
		return
	}
	defer listResp.Body.Close()

	var listData struct {
		Files []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"files"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}
	json.NewDecoder(listResp.Body).Decode(&listData)

	if listData.Error != nil {
		writeJSON(w, map[string]string{"error": fmt.Sprintf("Drive API: %s (code %d)", listData.Error.Message, listData.Error.Code)})
		return
	}

	var folderID string
	if len(listData.Files) > 0 {
		folderID = listData.Files[0].ID
		pm.logs.Add("admin", fmt.Sprintf("Found existing %s folder: %s", folderName, folderID))
	} else {
		// Create folder
		meta, _ := json.Marshal(map[string]string{
			"name":     folderName,
			"mimeType": "application/vnd.google-apps.folder",
		})
		createReq, _ := http.NewRequestWithContext(r.Context(), "POST",
			"https://www.googleapis.com/drive/v3/files", bytes.NewReader(meta))
		createReq.Header.Set("Authorization", "Bearer "+accessToken)
		createReq.Header.Set("Content-Type", "application/json")
		createResp, err := http.DefaultClient.Do(createReq)
		if err != nil {
			writeJSON(w, map[string]string{"error": fmt.Sprintf("folder creation failed: %v", err)})
			return
		}
		defer createResp.Body.Close()
		var createData struct {
			ID    string `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(createResp.Body).Decode(&createData)
		if createData.Error != nil {
			writeJSON(w, map[string]string{"error": createData.Error.Message})
			return
		}
		folderID = createData.ID
		pm.logs.Add("admin", fmt.Sprintf("Created %s folder: %s", folderName, folderID))
	}

	// Auto-save folder ID into server_config.json
	cfgPath := filepath.Join(pm.workDir, pm.serverConfig)
	cfg := map[string]interface{}{
		"storage_type":     "google",
		"google_folder_id": folderID,
		"refresh_rate_ms":  200,
		"flush_rate_ms":    300,
	}
	// Load existing config to preserve any extra fields
	if existing, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(existing, &cfg)
	}
	cfg["google_folder_id"] = folderID
	cfg["storage_type"] = "google"
	if folderName != "Flow-Data" {
		cfg["google_folder_name"] = folderName
	} else {
		delete(cfg, "google_folder_name")
	}
	formatted, _ := json.MarshalIndent(cfg, "", "  ")
	os.WriteFile(cfgPath, append(formatted, '\n'), 0644)

	pm.logs.Add("admin", "Server config updated with folder ID")
	writeJSON(w, map[string]interface{}{"ok": "folder ready", "folder_id": folderID, "folder_name": folderName})
}

func (pm *ProcessManager) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(pm.workDir, name))
		return err == nil
	}

	// Check if server_config.json has a folder ID
	hasFolderID := false
	if data, err := os.ReadFile(filepath.Join(pm.workDir, pm.serverConfig)); err == nil {
		var cfg map[string]interface{}
		if json.Unmarshal(data, &cfg) == nil {
			if fid, ok := cfg["google_folder_id"].(string); ok && fid != "" {
				hasFolderID = true
			}
		}
	}

	pm.mu.Lock()
	running := pm.running
	pm.mu.Unlock()

	writeJSON(w, map[string]interface{}{
		"has_credentials": exists(pm.credsFile),
		"has_token":       exists(pm.credsFile + ".token"),
		"has_config":      exists(pm.serverConfig),
		"has_folder_id":   hasFolderID,
		"has_server_bin":  exists(pm.serverBin),
		"server_running":  running,
	})
}

// ---------- Log Streaming ----------

func (pm *ProcessManager) handleLogsHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, pm.logs.History())
}

func (pm *ProcessManager) handleLogsSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := pm.logs.Subscribe()
	defer pm.logs.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-ch:
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ---------- Helpers ----------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---------- Session Auth ----------

type SessionManager struct {
	username  string
	password  string
	secretKey []byte
	maxAge    int
}

func NewSessionManager(username, password string, sessionHours int) *SessionManager {
	key := make([]byte, 32)
	rand.Read(key)
	maxAge := sessionHours * 3600
	return &SessionManager{username: username, password: password, secretKey: key, maxAge: maxAge}
}

func (sm *SessionManager) createToken() string {
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	mac := hmac.New(sha256.New, sm.secretKey)
	mac.Write([]byte(ts))
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + "." + sig
}

func (sm *SessionManager) validateToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	mac := hmac.New(sha256.New, sm.secretKey)
	mac.Write([]byte(parts[0]))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}

func (sm *SessionManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(loginHTML)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil {
		writeJSON(w, map[string]string{"error": "invalid request"})
		return
	}
	if req.Username != sm.username || req.Password != sm.password {
		time.Sleep(500 * time.Millisecond) // brute-force delay
		w.WriteHeader(401)
		writeJSON(w, map[string]string{"error": "wrong username or password"})
		return
	}
	token := sm.createToken()
	http.SetCookie(w, &http.Cookie{
		Name:     "fd_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sm.maxAge,
	})
	writeJSON(w, map[string]string{"ok": "logged in"})
}

func (sm *SessionManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "fd_session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (sm *SessionManager) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow login page and login API without auth
		if r.URL.Path == "/login" || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("fd_session")
		if err != nil || !sm.validateToken(cookie.Value) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.WriteHeader(401)
				writeJSON(w, map[string]string{"error": "not authenticated"})
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- Client Package ----------

//go:embed config.client.json
var defaultClientConfigJSON []byte

type clientPlatform struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Binary    string `json:"binary"`
	Available bool   `json:"available"`
}

var knownPlatforms = []struct {
	key, label, file string
}{
	{"darwin-arm64", "macOS (Apple Silicon)", "Undertow-darwin-arm64"},
	{"darwin-amd64", "macOS (Intel)", "Undertow-darwin-amd64"},
	{"windows-amd64", "Windows (x86_64)", "Undertow-windows-amd64.exe"},
	{"windows-arm64", "Windows (ARM64)", "Undertow-windows-arm64.exe"},
	{"linux-amd64", "Linux (x86_64)", "client-linux-amd64"},
	{"linux-arm64", "Linux (ARM64)", "client-linux-arm64"},
}

func (pm *ProcessManager) clientsDir() string {
	return filepath.Join(pm.workDir, "clients")
}

func (pm *ProcessManager) handleClientPlatforms(w http.ResponseWriter, r *http.Request) {
	dir := pm.clientsDir()
	var platforms []clientPlatform
	for _, p := range knownPlatforms {
		avail := false
		if _, err := os.Stat(filepath.Join(dir, p.file)); err == nil {
			avail = true
		}
		platforms = append(platforms, clientPlatform{Key: p.key, Label: p.label, Binary: p.file, Available: avail})
	}

	// Detect best default for the requesting client
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	suggested := ""
	switch {
	case strings.Contains(ua, "macintosh") || strings.Contains(ua, "mac os"):
		if runtime.GOARCH == "arm64" {
			suggested = "darwin-arm64"
		} else {
			suggested = "darwin-amd64"
		}
	case strings.Contains(ua, "windows"):
		suggested = "windows-amd64"
	case strings.Contains(ua, "linux"):
		suggested = "linux-amd64"
	}

	writeJSON(w, map[string]interface{}{
		"platforms": platforms,
		"suggested": suggested,
	})
}

func (pm *ProcessManager) handleClientDownload(w http.ResponseWriter, r *http.Request) {
	platform := r.URL.Query().Get("platform")
	if platform == "" {
		http.Error(w, "missing platform parameter", 400)
		return
	}

	// Find the platform
	var binFile, label string
	for _, p := range knownPlatforms {
		if p.key == platform {
			binFile = p.file
			label = p.label
			break
		}
	}
	if binFile == "" {
		http.Error(w, "unknown platform", 400)
		return
	}

	binPath := filepath.Join(pm.clientsDir(), binFile)
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		http.Error(w, "client binary not available for "+label, 404)
		return
	}

	credsPath := filepath.Join(pm.workDir, pm.credsFile)
	if _, err := os.Stat(credsPath); os.IsNotExist(err) {
		writeJSON(w, map[string]string{"error": "credentials.json not found — complete setup first"})
		return
	}

	// Build zip in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Add client binary
	binData, err := os.ReadFile(binPath)
	if err != nil {
		http.Error(w, "failed to read binary", 500)
		return
	}
	bh := &zip.FileHeader{Name: "undertow-client/" + binFile, Method: zip.Deflate}
	bh.SetMode(0755)
	bw, _ := zw.CreateHeader(bh)
	bw.Write(binData)

	// Add credentials.json
	credsData, _ := os.ReadFile(credsPath)
	cw, _ := zw.Create("undertow-client/credentials.json")
	cw.Write(credsData)

	// Add client_config.json (use embedded default, inject folder name if custom)
	clientCfg := make(map[string]interface{})
	json.Unmarshal(defaultClientConfigJSON, &clientCfg)
	// Read server config to check for custom folder name
	if srvCfgData, err := os.ReadFile(filepath.Join(pm.workDir, pm.serverConfig)); err == nil {
		var srvCfg map[string]interface{}
		if json.Unmarshal(srvCfgData, &srvCfg) == nil {
			if fn, ok := srvCfg["google_folder_name"].(string); ok && fn != "" {
				clientCfg["google_folder_name"] = fn
			}
		}
	}
	clientCfgJSON, _ := json.MarshalIndent(clientCfg, "", "  ")
	ccw, _ := zw.Create("undertow-client/client_config.json")
	ccw.Write(clientCfgJSON)

	// Add a README
	isTray := strings.HasPrefix(binFile, "Undertow")
	var readme string
	if isTray {
		readme = fmt.Sprintf(`Undertow Client
==================

Platform: %s

Quick Start:
1. Double-click %s to launch
2. A menu bar icon (grey circle) appears in your system tray
3. Click it → "Connect"
4. On first run, your browser opens for Google sign-in — click Allow
5. The icon turns green — you're connected!

The app automatically sets your system SOCKS proxy.
All your internet traffic now goes through the tunnel.

To disconnect: click the tray icon → "Disconnect"
To quit: click the tray icon → "Quit"

Config folder: ~/.undertow/
`, label, binFile)
	} else if strings.Contains(platform, "windows") {
		readme = fmt.Sprintf(`Undertow Client
==================

Platform: %s

Quick Start:
1. Open a terminal in this folder
2. Run: %s -c client_config.json -gc credentials.json
3. On first run, a browser window opens — sign in with your Google account and click Allow
4. Copy the redirect URL and paste it back into the terminal
5. The SOCKS5 proxy is now running on 127.0.0.1:1080

Configure your browser or apps to use SOCKS5 proxy: 127.0.0.1:1080
`, label, binFile)
	} else {
		readme = fmt.Sprintf(`Undertow Client
==================

Platform: %s

Quick Start:
1. Open a terminal in this folder
2. Make the binary executable: chmod +x %s
3. Run: ./%s -c client_config.json -gc credentials.json
4. On first run, a browser window opens — sign in with your Google account and click Allow
5. Copy the redirect URL and paste it back into the terminal
6. The SOCKS5 proxy is now running on 127.0.0.1:1080

Configure your browser or apps to use SOCKS5 proxy: 127.0.0.1:1080
`, label, binFile, binFile)
	}
	rw, _ := zw.Create("undertow-client/README.txt")
	rw.Write([]byte(readme))

	zw.Close()

	zipName := fmt.Sprintf("undertow-client-%s.zip", platform)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", zipName))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.Write(buf.Bytes())

	pm.logs.Add("admin", fmt.Sprintf("Client package downloaded (%s)", label))
}

// ---------- Main ----------

func main() {
	var configPath string
	flag.StringVar(&configPath, "c", "", "Path to config.json")
	flag.Parse()

	cfg := loadConfig(configPath)

	listenAddr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	absDir, _ := filepath.Abs(".")
	if exe, err := os.Executable(); err == nil {
		absDir = filepath.Dir(exe)
	}

	pm := NewProcessManager(absDir, cfg)
	sm := NewSessionManager(cfg.Username, cfg.Password, cfg.SessionHours)

	mux := http.NewServeMux()
	mux.HandleFunc("/", pm.handleIndex)
	mux.HandleFunc("/login", sm.handleLogin)
	mux.HandleFunc("/api/login", sm.handleLogin)
	mux.HandleFunc("/logout", sm.handleLogout)
	mux.HandleFunc("/api/status", pm.handleStatus)
	mux.HandleFunc("/api/start", pm.handleStart)
	mux.HandleFunc("/api/stop", pm.handleStop)
	mux.HandleFunc("/api/restart", pm.handleRestart)
	mux.HandleFunc("/api/config", pm.handleConfig)
	mux.HandleFunc("/api/credentials", pm.handleCredentials)
	mux.HandleFunc("/api/oauth/url", pm.handleOAuthURL)
	mux.HandleFunc("/api/oauth/exchange", pm.handleOAuthExchange)
	mux.HandleFunc("/api/setup/status", pm.handleSetupStatus)
	mux.HandleFunc("/api/setup/folder", pm.handleSetupFolder)
	mux.HandleFunc("/api/logs/history", pm.handleLogsHistory)
	mux.HandleFunc("/api/logs/stream", pm.handleLogsSSE)
	mux.HandleFunc("/api/client/platforms", pm.handleClientPlatforms)
	mux.HandleFunc("/api/client/download", pm.handleClientDownload)

	handler := sm.authMiddleware(mux)

	pm.logs.Add("admin", fmt.Sprintf("Admin panel starting on %s", listenAddr))
	pm.logs.Add("admin", fmt.Sprintf("Working directory: %s", absDir))

	log.Printf("╔══════════════════════════════════════╗")
	log.Printf("║      Undertow Admin Panel         ║")
	log.Printf("╠══════════════════════════════════════╣")
	log.Printf("║  URL:  http://%-22s║", listenAddr)
	log.Printf("║  User: %-30s║", cfg.Username)
	log.Printf("║  Pass: %-30s║", cfg.Password)
	log.Printf("╚══════════════════════════════════════╝")

	srv := &http.Server{Addr: listenAddr, Handler: handler}
	go func() {
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	pm.Stop()
	srv.Shutdown(context.Background())
}
