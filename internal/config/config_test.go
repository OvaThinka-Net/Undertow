package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"listen_addr": "127.0.0.1:1080",
		"client_id": "abc123",
		"storage_type": "local",
		"local_dir": "/tmp/test",
		"google_folder_id": "folder-id",
		"google_folder_name": "MyFolder",
		"refresh_rate_ms": 200,
		"flush_rate_ms": 100,
		"timezone": "Europe/Berlin"
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:1080" {
		t.Errorf("ListenAddr: got %q, want %q", cfg.ListenAddr, "127.0.0.1:1080")
	}
	if cfg.ClientID != "abc123" {
		t.Errorf("ClientID: got %q, want %q", cfg.ClientID, "abc123")
	}
	if cfg.StorageType != "local" {
		t.Errorf("StorageType: got %q, want %q", cfg.StorageType, "local")
	}
	if cfg.LocalDir != "/tmp/test" {
		t.Errorf("LocalDir: got %q, want %q", cfg.LocalDir, "/tmp/test")
	}
	if cfg.GoogleFolderID != "folder-id" {
		t.Errorf("GoogleFolderID: got %q, want %q", cfg.GoogleFolderID, "folder-id")
	}
	if cfg.GoogleFolderName != "MyFolder" {
		t.Errorf("GoogleFolderName: got %q, want %q", cfg.GoogleFolderName, "MyFolder")
	}
	if cfg.RefreshRateMs != 200 {
		t.Errorf("RefreshRateMs: got %d, want %d", cfg.RefreshRateMs, 200)
	}
	if cfg.FlushRateMs != 100 {
		t.Errorf("FlushRateMs: got %d, want %d", cfg.FlushRateMs, 100)
	}
	if cfg.Timezone != "Europe/Berlin" {
		t.Errorf("Timezone: got %q, want %q", cfg.Timezone, "Europe/Berlin")
	}
}

func TestLoad_MinimalConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"storage_type": "google"}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.StorageType != "google" {
		t.Errorf("StorageType: got %q, want %q", cfg.StorageType, "google")
	}
	if cfg.ListenAddr != "" {
		t.Errorf("ListenAddr should be empty, got %q", cfg.ListenAddr)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{invalid json}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFolderName_Default(t *testing.T) {
	cfg := &AppConfig{}
	if got := cfg.FolderName(); got != "Flow-Data" {
		t.Errorf("FolderName: got %q, want %q", got, "Flow-Data")
	}
}

func TestFolderName_Custom(t *testing.T) {
	cfg := &AppConfig{GoogleFolderName: "CustomName"}
	if got := cfg.FolderName(); got != "CustomName" {
		t.Errorf("FolderName: got %q, want %q", got, "CustomName")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "save_test.json")

	original := &AppConfig{
		ListenAddr:       "127.0.0.1:9090",
		ClientID:         "test-client",
		StorageType:      "local",
		LocalDir:         "/data",
		GoogleFolderID:   "fid",
		GoogleFolderName: "Test-Folder",
		RefreshRateMs:    300,
		FlushRateMs:      150,
		Timezone:         "UTC",
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save failed: %v", err)
	}

	if loaded.ListenAddr != original.ListenAddr {
		t.Errorf("ListenAddr mismatch: got %q, want %q", loaded.ListenAddr, original.ListenAddr)
	}
	if loaded.ClientID != original.ClientID {
		t.Errorf("ClientID mismatch")
	}
	if loaded.StorageType != original.StorageType {
		t.Errorf("StorageType mismatch")
	}
	if loaded.LocalDir != original.LocalDir {
		t.Errorf("LocalDir mismatch")
	}
	if loaded.GoogleFolderID != original.GoogleFolderID {
		t.Errorf("GoogleFolderID mismatch")
	}
	if loaded.GoogleFolderName != original.GoogleFolderName {
		t.Errorf("GoogleFolderName mismatch")
	}
	if loaded.RefreshRateMs != original.RefreshRateMs {
		t.Errorf("RefreshRateMs mismatch")
	}
	if loaded.FlushRateMs != original.FlushRateMs {
		t.Errorf("FlushRateMs mismatch")
	}
	if loaded.Timezone != original.Timezone {
		t.Errorf("Timezone mismatch")
	}
}

func TestLoad_WithTransportConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"storage_type": "google",
		"transport": {
			"TargetIP": "1.2.3.4:443,5.6.7.8:443",
			"SNI": "example.com",
			"HostHeader": "api.example.com",
			"InsecureSkipVerify": true
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Transport.TargetIP != "1.2.3.4:443,5.6.7.8:443" {
		t.Errorf("Transport.TargetIP: got %q", cfg.Transport.TargetIP)
	}
	if cfg.Transport.SNI != "example.com" {
		t.Errorf("Transport.SNI: got %q", cfg.Transport.SNI)
	}
	if cfg.Transport.HostHeader != "api.example.com" {
		t.Errorf("Transport.HostHeader: got %q", cfg.Transport.HostHeader)
	}
	if !cfg.Transport.InsecureSkipVerify {
		t.Error("Transport.InsecureSkipVerify should be true")
	}
}
