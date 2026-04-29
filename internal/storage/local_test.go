package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalBackend_NewLocalBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep")
	lb, err := NewLocalBackend(path)
	if err != nil {
		t.Fatalf("NewLocalBackend: %v", err)
	}
	if lb == nil {
		t.Fatal("expected non-nil backend")
	}
	// Verify directory was created
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("path is not a directory")
	}
}

func TestLocalBackend_Login(t *testing.T) {
	dir := t.TempDir()
	lb, _ := NewLocalBackend(dir)

	if err := lb.Login(context.Background()); err != nil {
		t.Fatalf("Login should succeed: %v", err)
	}
}

func TestLocalBackend_Login_BadDir(t *testing.T) {
	// Point at a file, not a directory
	dir := t.TempDir()
	fpath := filepath.Join(dir, "afile")
	os.WriteFile(fpath, []byte("x"), 0644)

	lb := &LocalBackend{baseDir: fpath}
	if err := lb.Login(context.Background()); err == nil {
		t.Fatal("expected error for non-directory path")
	}
}

func TestLocalBackend_UploadAndDownload(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	data := []byte("hello local backend")
	err := lb.Upload(ctx, "test-file.bin", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	rc, err := lb.Download(ctx, "test-file.bin")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer rc.Close()

	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

func TestLocalBackend_Upload_AtomicWrite(t *testing.T) {
	lb, dir := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "atomic.bin", bytes.NewReader([]byte("content")))

	// Verify no .tmp file remains
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLocalBackend_Download_NotFound(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	_, err := lb.Download(ctx, "nonexistent.bin")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocalBackend_ListQuery_PrefixFilter(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "req-abc-mux-100.bin", bytes.NewReader([]byte("a")))
	lb.Upload(ctx, "req-def-mux-200.bin", bytes.NewReader([]byte("b")))
	lb.Upload(ctx, "res-abc-mux-300.bin", bytes.NewReader([]byte("c")))

	files, err := lb.ListQuery(ctx, "req-")
	if err != nil {
		t.Fatalf("ListQuery: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files with prefix 'req-', got %d: %v", len(files), files)
	}

	files, err = lb.ListQuery(ctx, "res-")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file with prefix 'res-', got %d", len(files))
	}
}

func TestLocalBackend_ListQuery_ExcludesTmpFiles(t *testing.T) {
	lb, dir := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "good.bin", bytes.NewReader([]byte("ok")))
	// Create a .tmp file directly
	os.WriteFile(filepath.Join(dir, "partial.bin.tmp"), []byte("incomplete"), 0644)

	files, err := lb.ListQuery(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "partial.bin.tmp" {
			t.Error("ListQuery should exclude .tmp files")
		}
	}
}

func TestLocalBackend_ListQuery_EmptyPrefix(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "a.bin", bytes.NewReader([]byte("1")))
	lb.Upload(ctx, "b.bin", bytes.NewReader([]byte("2")))

	files, _ := lb.ListQuery(ctx, "")
	if len(files) != 2 {
		t.Errorf("expected 2 files with empty prefix, got %d", len(files))
	}
}

func TestLocalBackend_Delete(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "to-delete.bin", bytes.NewReader([]byte("x")))

	if err := lb.Delete(ctx, "to-delete.bin"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify deleted
	_, err := lb.Download(ctx, "to-delete.bin")
	if err == nil {
		t.Error("file should be deleted")
	}
}

func TestLocalBackend_Delete_Idempotent(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	// Deleting a nonexistent file should not error
	if err := lb.Delete(ctx, "never-existed.bin"); err != nil {
		t.Fatalf("Delete of nonexistent file should not error: %v", err)
	}
}

func TestLocalBackend_BatchDelete(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "batch-1.bin", bytes.NewReader([]byte("a")))
	lb.Upload(ctx, "batch-2.bin", bytes.NewReader([]byte("b")))
	lb.Upload(ctx, "batch-3.bin", bytes.NewReader([]byte("c")))

	err := lb.BatchDelete(ctx, []string{"batch-1.bin", "batch-2.bin", "batch-3.bin"})
	if err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}

	files, _ := lb.ListQuery(ctx, "batch-")
	if len(files) != 0 {
		t.Errorf("expected 0 files after batch delete, got %d", len(files))
	}
}

func TestLocalBackend_CreateFolder(t *testing.T) {
	lb, dir := setupTestBackend(t)
	ctx := context.Background()

	id, err := lb.CreateFolder(ctx, "subfolder")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if id != "subfolder" {
		t.Errorf("expected id 'subfolder', got %q", id)
	}

	info, err := os.Stat(filepath.Join(dir, "subfolder"))
	if err != nil {
		t.Fatal("folder not created")
	}
	if !info.IsDir() {
		t.Error("not a directory")
	}
}

func TestLocalBackend_FindFolder(t *testing.T) {
	lb, dir := setupTestBackend(t)
	ctx := context.Background()

	// Not found
	id, err := lb.FindFolder(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Errorf("expected empty id for missing folder, got %q", id)
	}

	// Create and find
	os.Mkdir(filepath.Join(dir, "existing"), 0755)
	id, err = lb.FindFolder(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if id != "existing" {
		t.Errorf("expected 'existing', got %q", id)
	}
}

func TestLocalBackend_ListQuery_ExcludesDirectories(t *testing.T) {
	lb, dir := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "file.bin", bytes.NewReader([]byte("data")))
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)

	files, _ := lb.ListQuery(ctx, "")
	for _, f := range files {
		if f == "subdir" {
			t.Error("ListQuery should not include directories")
		}
	}
}
