package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"
)

const (
	maxRetries     = 3
	baseRetryDelay = 200 * time.Millisecond
)

// RetryBackend wraps any Backend and adds exponential-backoff retries
// for transient failures on Upload, Download, Delete, and ListQuery.
type RetryBackend struct {
	Inner Backend
}

func NewRetryBackend(inner Backend) *RetryBackend {
	return &RetryBackend{Inner: inner}
}

func (r *RetryBackend) Login(ctx context.Context) error {
	return r.Inner.Login(ctx)
}

func (r *RetryBackend) Upload(ctx context.Context, filename string, data io.Reader) error {
	// data is an io.Reader that can only be consumed once. The first attempt
	// consumes it; if it fails, we cannot replay the body, so we don't retry uploads
	// whose body has already been partially read. However, in Undertow the caller
	// always passes an io.Pipe, so retrying is only safe if the pipe hasn't been read.
	// We buffer the data so retries can replay it.
	buf, err := io.ReadAll(data)
	if err != nil {
		return fmt.Errorf("retry: failed to buffer upload data: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * (1 << (attempt - 1))
			log.Printf("retry: upload %s attempt %d/%d after %v", filename, attempt+1, maxRetries+1, delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		reader := io.NopCloser(io.NewSectionReader(readerAtFromBytes(buf), 0, int64(len(buf))))
		lastErr = r.Inner.Upload(ctx, filename, reader)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("retry: upload %s failed after %d attempts: %w", filename, maxRetries+1, lastErr)
}

func (r *RetryBackend) ListQuery(ctx context.Context, prefix string) ([]string, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		result, err := r.Inner.ListQuery(ctx, prefix)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("retry: list query failed after %d attempts: %w", maxRetries+1, lastErr)
}

func (r *RetryBackend) Download(ctx context.Context, filename string) (io.ReadCloser, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		rc, err := r.Inner.Download(ctx, filename)
		if err == nil {
			return rc, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, err // Don't retry 404s — file was already cleaned up
		}
		lastErr = err
	}
	return nil, fmt.Errorf("retry: download %s failed after %d attempts: %w", filename, maxRetries+1, lastErr)
}

func (r *RetryBackend) Delete(ctx context.Context, filename string) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		lastErr = r.Inner.Delete(ctx, filename)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("retry: delete %s failed after %d attempts: %w", filename, maxRetries+1, lastErr)
}

func (r *RetryBackend) BatchDelete(ctx context.Context, filenames []string) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseRetryDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		lastErr = r.Inner.BatchDelete(ctx, filenames)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("retry: batch delete failed after %d attempts: %w", maxRetries+1, lastErr)
}

func (r *RetryBackend) CreateFolder(ctx context.Context, name string) (string, error) {
	return r.Inner.CreateFolder(ctx, name)
}

func (r *RetryBackend) FindFolder(ctx context.Context, name string) (string, error) {
	return r.Inner.FindFolder(ctx, name)
}

// bytesReaderAt wraps a byte slice to implement io.ReaderAt.
type bytesReaderAt struct {
	data []byte
}

func readerAtFromBytes(b []byte) *bytesReaderAt {
	return &bytesReaderAt{data: b}
}

func (b *bytesReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	n = copy(p, b.data[off:])
	if n < len(p) {
		err = io.EOF
	}
	return
}
