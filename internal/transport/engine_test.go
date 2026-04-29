package transport

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/OvaThinka-Net/Undertow/internal/storage"
)

// mockBackend is a test double for storage.Backend that records calls and
// allows injecting uploaded data for pollLoop to download.
type mockBackend struct {
	mu        sync.Mutex
	files     map[string][]byte
	uploads   []string // filenames uploaded
	deletions []string // filenames deleted
}

func newMockBackend() *mockBackend {
	return &mockBackend{files: make(map[string][]byte)}
}

func (m *mockBackend) Login(ctx context.Context) error { return nil }

func (m *mockBackend) Upload(ctx context.Context, filename string, data io.Reader) error {
	buf, _ := io.ReadAll(data)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[filename] = buf
	m.uploads = append(m.uploads, filename)
	return nil
}

func (m *mockBackend) ListQuery(ctx context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []string
	for name := range m.files {
		if len(prefix) == 0 || len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			result = append(result, name)
		}
	}
	return result, nil
}

func (m *mockBackend) Download(ctx context.Context, filename string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[filename]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockBackend) Delete(ctx context.Context, filename string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, filename)
	m.deletions = append(m.deletions, filename)
	return nil
}

func (m *mockBackend) BatchDelete(ctx context.Context, filenames []string) error {
	for _, f := range filenames {
		m.Delete(ctx, f)
	}
	return nil
}

func (m *mockBackend) CreateFolder(ctx context.Context, name string) (string, error) {
	return name, nil
}

func (m *mockBackend) FindFolder(ctx context.Context, name string) (string, error) {
	return "", nil
}

func (m *mockBackend) getUploads() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.uploads))
	copy(cp, m.uploads)
	return cp
}

func (m *mockBackend) getDeletions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.deletions))
	copy(cp, m.deletions)
	return cp
}

func (m *mockBackend) fileCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.files)
}

func TestEngine_NewEngine_Client(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "client-1")

	if e.myDir != DirReq {
		t.Errorf("client myDir: got %q, want %q", e.myDir, DirReq)
	}
	if e.peerDir != DirRes {
		t.Errorf("client peerDir: got %q, want %q", e.peerDir, DirRes)
	}
	if e.id != "client-1" {
		t.Errorf("id: got %q, want %q", e.id, "client-1")
	}
}

func TestEngine_NewEngine_Server(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	if e.myDir != DirRes {
		t.Errorf("server myDir: got %q, want %q", e.myDir, DirRes)
	}
	if e.peerDir != DirReq {
		t.Errorf("server peerDir: got %q, want %q", e.peerDir, DirReq)
	}
}

func TestEngine_AddGetRemoveSession(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	s := NewSession("sess-1")
	e.AddSession(s)

	got := e.GetSession("sess-1")
	if got == nil {
		t.Fatal("expected to find session")
	}
	if got.ID != "sess-1" {
		t.Errorf("session ID: got %q, want %q", got.ID, "sess-1")
	}

	e.RemoveSession("sess-1")
	if e.GetSession("sess-1") != nil {
		t.Error("session should be removed")
	}
}

func TestEngine_RemoveSession_Tombstone(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	s := NewSession("tomb-1")
	e.AddSession(s)
	e.RemoveSession("tomb-1")

	e.closedSessionsMu.Lock()
	_, exists := e.closedSessions["tomb-1"]
	e.closedSessionsMu.Unlock()

	if !exists {
		t.Error("removed session should be in tombstone map")
	}
}

func TestEngine_GetSession_NotFound(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	if e.GetSession("nonexistent") != nil {
		t.Error("expected nil for nonexistent session")
	}
}

func TestEngine_SetRates(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")

	e.SetPollRate(100)
	if e.pollTicker != 100*time.Millisecond {
		t.Errorf("pollTicker: got %v, want 100ms", e.pollTicker)
	}

	e.SetFlushRate(50)
	if e.flushTicker != 50*time.Millisecond {
		t.Errorf("flushTicker: got %v, want 50ms", e.flushTicker)
	}

	// SetPollRate with 0 should not change
	e.SetPollRate(0)
	if e.pollTicker != 100*time.Millisecond {
		t.Error("SetPollRate(0) should not change interval")
	}
}

func TestEngine_SetRefreshRate_Legacy(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")

	// At default flushTicker (150ms), SetRefreshRate should update both
	e.SetRefreshRate(200)
	if e.pollTicker != 200*time.Millisecond {
		t.Errorf("pollTicker: got %v, want 200ms", e.pollTicker)
	}
	if e.flushTicker != 200*time.Millisecond {
		t.Errorf("flushTicker should also be updated: got %v", e.flushTicker)
	}

	// Once flushTicker is non-default, SetRefreshRate should only update pollTicker
	e.SetRefreshRate(300)
	if e.pollTicker != 300*time.Millisecond {
		t.Errorf("pollTicker: got %v, want 300ms", e.pollTicker)
	}
	if e.flushTicker != 200*time.Millisecond {
		t.Errorf("flushTicker should not change: got %v", e.flushTicker)
	}
}

func TestEngine_FlushAll_UploadsData(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "client-1")

	s := NewSession("flush-sess")
	s.TargetAddr = "example.com:443"
	e.AddSession(s)

	s.EnqueueTx([]byte("test payload"))

	e.flushAll(context.Background())

	// Wait for async upload goroutine
	time.Sleep(200 * time.Millisecond)

	uploads := mb.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("expected 1 upload, got %d", len(uploads))
	}

	// Verify the uploaded file contains our envelope
	mb.mu.Lock()
	data := mb.files[uploads[0]]
	mb.mu.Unlock()

	var env Envelope
	err := env.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Decode uploaded data: %v", err)
	}
	if env.SessionID != "flush-sess" {
		t.Errorf("SessionID: got %q, want %q", env.SessionID, "flush-sess")
	}
	if string(env.Payload) != "test payload" {
		t.Errorf("Payload: got %q, want %q", env.Payload, "test payload")
	}
	if env.TargetAddr != "example.com:443" {
		t.Errorf("TargetAddr: got %q, want %q", env.TargetAddr, "example.com:443")
	}
}

func TestEngine_FlushAll_ClosedSessionRemoved(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")

	s := NewSession("close-me")
	e.AddSession(s)

	// Mark session as closed
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	e.flushAll(context.Background())

	// Wait for async upload
	time.Sleep(200 * time.Millisecond)

	if e.GetSession("close-me") != nil {
		t.Error("closed session should be removed after flush")
	}
}

func TestEngine_FlushAll_IdleTimeout(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")

	s := NewSession("idle-sess")
	e.AddSession(s)

	// Fake last activity to be 2 minutes ago
	s.mu.Lock()
	s.lastActivity = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()

	e.flushAll(context.Background())

	// Wait for async upload
	time.Sleep(200 * time.Millisecond)

	// Session should be removed due to idle timeout
	if e.GetSession("idle-sess") != nil {
		t.Error("idle session should be removed after flush")
	}
}

func TestEngine_FlushAll_MultipleSessionsMuxed(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")

	s1 := NewSession("mux-1")
	s1.ClientID = "c1"
	s2 := NewSession("mux-2")
	s2.ClientID = "c1"

	e.AddSession(s1)
	e.AddSession(s2)

	s1.EnqueueTx([]byte("data-1"))
	s2.EnqueueTx([]byte("data-2"))

	e.flushAll(context.Background())

	time.Sleep(200 * time.Millisecond)

	// Both sessions share client "c1", so they should be in the same mux file
	uploads := mb.getUploads()
	if len(uploads) != 1 {
		t.Fatalf("expected 1 mux upload for same client, got %d", len(uploads))
	}

	// Decode both envelopes from the mux file
	mb.mu.Lock()
	data := mb.files[uploads[0]]
	mb.mu.Unlock()

	ids := make(map[string]bool)
	reader := bytes.NewReader(data)
	for {
		var env Envelope
		if err := env.Decode(reader); err != nil {
			break
		}
		ids[env.SessionID] = true
	}

	if !ids["mux-1"] || !ids["mux-2"] {
		t.Errorf("expected both sessions in mux, got: %v", ids)
	}
}

func TestEngine_FlushAll_NoDataNoUpload(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "") // server — won't auto-send on seq=0

	s := NewSession("empty-sess")
	s.mu.Lock()
	s.txSeq = 1 // Not the first seq, so no initial trigger
	s.mu.Unlock()
	e.AddSession(s)

	e.flushAll(context.Background())
	time.Sleep(100 * time.Millisecond)

	if len(mb.getUploads()) != 0 {
		t.Error("should not upload when there's no data to send")
	}
}

func TestEngine_ConcurrentAddGetSession(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		id := "sess-" + string(rune('A'+i%26))
		go func(id string) {
			defer wg.Done()
			s := NewSession(id)
			e.AddSession(s)
		}(id)
		go func(id string) {
			defer wg.Done()
			e.GetSession(id)
		}(id)
	}
	wg.Wait()
}

func TestEngine_Start_ContextCancel(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, true, "c1")
	e.SetPollRate(50)
	e.SetFlushRate(50)

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	// Cancel should stop all loops without panic
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestEngine_PollLoop_DownloadsAndProcesses(t *testing.T) {
	mb := newMockBackend()

	// Create a server engine that polls for "req-" files
	e := NewEngine(mb, false, "")

	var newSessionCalled bool
	var newSessionMu sync.Mutex
	e.OnNewSession = func(sessionID, targetAddr string, s *Session) {
		newSessionMu.Lock()
		newSessionCalled = true
		newSessionMu.Unlock()
	}

	// Pre-upload a mux file that the server should discover
	var buf bytes.Buffer
	env := Envelope{
		SessionID:  "discovered-sess",
		Seq:        0,
		TargetAddr: "target.com:443",
		Payload:    []byte("hello from client"),
	}
	env.Encode(&buf)

	ts := time.Now().UnixNano()
	filename := "req-clientA-mux-" + intToStr(ts) + ".bin"
	mb.Upload(context.Background(), filename, bytes.NewReader(buf.Bytes()))

	// Start engine with fast polling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.SetPollRate(50)
	e.SetFlushRate(5000) // slow flush — we don't need it
	e.Start(ctx)

	// Wait for poll to pick it up
	time.Sleep(500 * time.Millisecond)

	newSessionMu.Lock()
	called := newSessionCalled
	newSessionMu.Unlock()

	if !called {
		t.Error("OnNewSession should have been called for new session")
	}

	// Session should exist
	s := e.GetSession("discovered-sess")
	if s == nil {
		t.Fatal("session should exist after poll")
	}

	// Read the data from RxChan
	select {
	case data := <-s.RxChan:
		if string(data) != "hello from client" {
			t.Errorf("got %q, want %q", data, "hello from client")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rx data")
	}

	// File should be deleted after processing
	time.Sleep(200 * time.Millisecond)
	if mb.fileCount() != 0 {
		t.Error("processed file should be deleted")
	}
}

func TestEngine_PollLoop_SkipsStaleFiles(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	// Create a file with a timestamp older than 5 minutes
	oldTs := time.Now().Add(-10 * time.Minute).UnixNano()
	filename := "req-old-mux-" + intToStr(oldTs) + ".bin"

	var buf bytes.Buffer
	env := Envelope{SessionID: "stale", Seq: 0, Payload: []byte("old data")}
	env.Encode(&buf)
	mb.Upload(context.Background(), filename, bytes.NewReader(buf.Bytes()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.SetPollRate(50)
	e.SetFlushRate(5000)
	e.Start(ctx)

	time.Sleep(500 * time.Millisecond)

	// Stale session should NOT be created
	if e.GetSession("stale") != nil {
		t.Error("stale file should be skipped, not processed")
	}

	// But the stale file should be deleted
	if mb.fileCount() != 0 {
		t.Error("stale file should be deleted")
	}
}

func TestEngine_PollLoop_TombstonedSessionIgnored(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	sessionCalls := 0
	e.OnNewSession = func(sessionID, targetAddr string, s *Session) {
		sessionCalls++
	}

	// Pre-tombstone a session
	e.closedSessionsMu.Lock()
	e.closedSessions["dead-sess"] = time.Now()
	e.closedSessionsMu.Unlock()

	// Upload a file for the tombstoned session
	var buf bytes.Buffer
	env := Envelope{SessionID: "dead-sess", Seq: 0, Payload: []byte("ghost")}
	env.Encode(&buf)
	ts := time.Now().UnixNano()
	mb.Upload(context.Background(), "req-cl-mux-"+intToStr(ts)+".bin", bytes.NewReader(buf.Bytes()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.SetPollRate(50)
	e.SetFlushRate(5000)
	e.Start(ctx)

	time.Sleep(500 * time.Millisecond)

	// OnNewSession should still be called (the envelope is processed),
	// but the tombstone check inside pollLoop skips delivery
	if e.GetSession("dead-sess") != nil {
		t.Error("tombstoned session should not be re-created")
	}
}

func TestEngine_PollLoop_DeduplicatesFiles(t *testing.T) {
	mb := newMockBackend()
	e := NewEngine(mb, false, "")

	callCount := 0
	var callMu sync.Mutex
	e.OnNewSession = func(sessionID, targetAddr string, s *Session) {
		callMu.Lock()
		callCount++
		callMu.Unlock()
	}

	var buf bytes.Buffer
	env := Envelope{SessionID: "dedup-sess", Seq: 0, Payload: []byte("once")}
	env.Encode(&buf)

	// We need a file that persists across polls (mock doesn't auto-delete from ListQuery)
	// But our mock does delete on BatchDelete. The processed map should prevent re-download
	// even if the file reappears (e.g., batch delete hasn't completed yet).
	ts := time.Now().UnixNano()
	fname := "req-c1-mux-" + intToStr(ts) + ".bin"

	// Add to processed map to simulate already-seen
	e.processedMu.Lock()
	e.processed[fname] = time.Now()
	e.processedMu.Unlock()

	mb.Upload(context.Background(), fname, bytes.NewReader(buf.Bytes()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.SetPollRate(50)
	e.SetFlushRate(5000)
	e.Start(ctx)

	time.Sleep(300 * time.Millisecond)

	callMu.Lock()
	c := callCount
	callMu.Unlock()

	if c != 0 {
		t.Errorf("already-processed file should not trigger OnNewSession, got %d calls", c)
	}
}

// intToStr converts int64 to string without importing strconv in tests
func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := make([]byte, 0, 20)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if negative {
		digits = append(digits, '-')
	}
	// reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
