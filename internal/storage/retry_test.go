package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
)

// failNBackend wraps a LocalBackend and fails the first N calls to each method.
type failNBackend struct {
	Backend
	uploadFails   atomic.Int32
	downloadFails atomic.Int32
	listFails     atomic.Int32
	deleteFails   atomic.Int32
}

func (f *failNBackend) Upload(ctx context.Context, filename string, data io.Reader) error {
	if f.uploadFails.Add(-1) >= 0 {
		// Consume the reader so the retry can replay from buffer
		io.ReadAll(data)
		return fmt.Errorf("transient upload error")
	}
	return f.Backend.Upload(ctx, filename, data)
}

func (f *failNBackend) Download(ctx context.Context, filename string) (io.ReadCloser, error) {
	if f.downloadFails.Add(-1) >= 0 {
		return nil, fmt.Errorf("transient download error")
	}
	return f.Backend.Download(ctx, filename)
}

func (f *failNBackend) ListQuery(ctx context.Context, prefix string) ([]string, error) {
	if f.listFails.Add(-1) >= 0 {
		return nil, fmt.Errorf("transient list error")
	}
	return f.Backend.ListQuery(ctx, prefix)
}

func (f *failNBackend) Delete(ctx context.Context, filename string) error {
	if f.deleteFails.Add(-1) >= 0 {
		return fmt.Errorf("transient delete error")
	}
	return f.Backend.Delete(ctx, filename)
}

func setupTestBackend(t *testing.T) (*LocalBackend, string) {
	t.Helper()
	dir := t.TempDir()
	lb, err := NewLocalBackend(dir)
	if err != nil {
		t.Fatalf("NewLocalBackend: %v", err)
	}
	return lb, dir
}

func TestRetryBackend_UploadSucceedsAfterRetries(t *testing.T) {
	lb, _ := setupTestBackend(t)
	fb := &failNBackend{Backend: lb}
	fb.uploadFails.Store(2) // Fail first 2 attempts, succeed on 3rd

	rb := NewRetryBackend(fb)
	ctx := context.Background()

	err := rb.Upload(ctx, "test-retry.bin", bytes.NewReader([]byte("hello retry")))
	if err != nil {
		t.Fatalf("expected upload to succeed after retries, got: %v", err)
	}

	// Verify file was written
	rc, err := lb.Download(ctx, "test-retry.bin")
	if err != nil {
		t.Fatalf("download after retry upload: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "hello retry" {
		t.Errorf("expected 'hello retry', got %q", string(data))
	}
}

func TestRetryBackend_UploadFailsAfterMaxRetries(t *testing.T) {
	lb, _ := setupTestBackend(t)
	fb := &failNBackend{Backend: lb}
	fb.uploadFails.Store(10) // More than maxRetries+1

	rb := NewRetryBackend(fb)
	ctx := context.Background()

	err := rb.Upload(ctx, "test-fail.bin", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected upload to fail after max retries")
	}
}

func TestRetryBackend_DownloadSucceedsAfterRetries(t *testing.T) {
	lb, _ := setupTestBackend(t)

	ctx := context.Background()
	// Pre-upload a file
	lb.Upload(ctx, "dl-test.bin", bytes.NewReader([]byte("download me")))

	fb := &failNBackend{Backend: lb}
	fb.downloadFails.Store(1) // Fail first attempt

	rb := NewRetryBackend(fb)
	rc, err := rb.Download(ctx, "dl-test.bin")
	if err != nil {
		t.Fatalf("expected download to succeed after retry, got: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "download me" {
		t.Errorf("expected 'download me', got %q", string(data))
	}
}

func TestRetryBackend_ListQuerySucceedsAfterRetries(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "req-abc-mux-123.bin", bytes.NewReader([]byte("x")))

	fb := &failNBackend{Backend: lb}
	fb.listFails.Store(2) // Fail 2 times

	rb := NewRetryBackend(fb)
	files, err := rb.ListQuery(ctx, "req-")
	if err != nil {
		t.Fatalf("expected list to succeed after retries, got: %v", err)
	}
	if len(files) != 1 || files[0] != "req-abc-mux-123.bin" {
		t.Errorf("unexpected files: %v", files)
	}
}

func TestRetryBackend_DeleteSucceedsAfterRetries(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "del-test.bin", bytes.NewReader([]byte("x")))

	fb := &failNBackend{Backend: lb}
	fb.deleteFails.Store(1)

	rb := NewRetryBackend(fb)
	err := rb.Delete(ctx, "del-test.bin")
	if err != nil {
		t.Fatalf("expected delete to succeed after retry, got: %v", err)
	}

	// Verify file is gone
	_, err = lb.Download(ctx, "del-test.bin")
	if err == nil {
		t.Error("expected file to be deleted")
	}
}

func TestRetryBackend_ContextCancellation(t *testing.T) {
	lb, _ := setupTestBackend(t)
	fb := &failNBackend{Backend: lb}
	fb.uploadFails.Store(10) // Always fail

	rb := NewRetryBackend(fb)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := rb.Upload(ctx, "cancel-test.bin", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestRetryBackend_DownloadNotFound_NoRetry(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	// Don't upload any file — download should get ErrNotFound immediately
	rb := NewRetryBackend(lb)
	_, err := rb.Download(ctx, "nonexistent.bin")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestRetryBackend_DownloadTransientError_Retries(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "retry-dl.bin", bytes.NewReader([]byte("content")))

	fb := &failNBackend{Backend: lb}
	fb.downloadFails.Store(2) // Fail first 2, succeed on 3rd

	rb := NewRetryBackend(fb)
	rc, err := rb.Download(ctx, "retry-dl.bin")
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "content" {
		t.Errorf("got %q, want %q", string(data), "content")
	}
}

func TestRetryBackend_BatchDelete(t *testing.T) {
	lb, _ := setupTestBackend(t)
	ctx := context.Background()

	lb.Upload(ctx, "bd-1.bin", bytes.NewReader([]byte("a")))
	lb.Upload(ctx, "bd-2.bin", bytes.NewReader([]byte("b")))

	rb := NewRetryBackend(lb)
	err := rb.BatchDelete(ctx, []string{"bd-1.bin", "bd-2.bin"})
	if err != nil {
		t.Fatalf("BatchDelete: %v", err)
	}

	files, _ := rb.ListQuery(ctx, "bd-")
	if len(files) != 0 {
		t.Errorf("expected 0 files after batch delete, got %d", len(files))
	}
}

func TestRetryBackend_LoginPassthrough(t *testing.T) {
	lb, _ := setupTestBackend(t)
	rb := NewRetryBackend(lb)
	if err := rb.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
}
