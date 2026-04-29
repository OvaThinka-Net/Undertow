package transport

import (
	"io"
	"testing"
	"time"
)

func TestVirtualConn_ReadWrite(t *testing.T) {
	s := NewSession("vc-rw")
	vc := NewVirtualConn(s, nil)

	// Write goes to session txBuf
	n, err := vc.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned %d, want 5", n)
	}

	s.mu.Lock()
	if string(s.txBuf) != "hello" {
		t.Errorf("txBuf: got %q, want %q", s.txBuf, "hello")
	}
	s.mu.Unlock()
}

func TestVirtualConn_ReadFromRxChan(t *testing.T) {
	s := NewSession("vc-rx")
	vc := NewVirtualConn(s, nil)

	// Send data via RxChan
	go func() {
		s.RxChan <- []byte("world")
	}()

	buf := make([]byte, 10)
	n, err := vc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "world" {
		t.Errorf("Read: got %q, want %q", buf[:n], "world")
	}
}

func TestVirtualConn_ReadPartialBuffer(t *testing.T) {
	s := NewSession("vc-partial")
	vc := NewVirtualConn(s, nil)

	go func() {
		s.RxChan <- []byte("abcdef")
	}()

	// Read only 3 bytes
	buf := make([]byte, 3)
	n, err := vc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 3 || string(buf[:n]) != "abc" {
		t.Errorf("first read: got %q (n=%d), want 'abc'", buf[:n], n)
	}

	// Read remaining 3 bytes from internal buffer
	n, err = vc.Read(buf)
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if n != 3 || string(buf[:n]) != "def" {
		t.Errorf("second read: got %q (n=%d), want 'def'", buf[:n], n)
	}
}

func TestVirtualConn_ReadEOFOnClosedChannel(t *testing.T) {
	s := NewSession("vc-eof")
	vc := NewVirtualConn(s, nil)

	close(s.RxChan)

	buf := make([]byte, 10)
	_, err := vc.Read(buf)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got: %v", err)
	}
}

func TestVirtualConn_Close(t *testing.T) {
	s := NewSession("vc-close")
	vc := NewVirtualConn(s, nil)

	if err := vc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s.mu.Lock()
	if !s.closed {
		t.Error("session should be marked closed after VirtualConn.Close()")
	}
	s.mu.Unlock()
}

func TestVirtualConn_WriteEmpty(t *testing.T) {
	s := NewSession("vc-empty")
	vc := NewVirtualConn(s, nil)

	n, err := vc.Write([]byte{})
	if err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}

	s.mu.Lock()
	if len(s.txBuf) != 0 {
		t.Error("txBuf should be empty after writing empty bytes")
	}
	s.mu.Unlock()
}

func TestVirtualConn_Addresses(t *testing.T) {
	s := NewSession("vc-addr")
	vc := NewVirtualConn(s, nil)

	if vc.LocalAddr() == nil {
		t.Error("LocalAddr should not be nil")
	}
	if vc.RemoteAddr() == nil {
		t.Error("RemoteAddr should not be nil")
	}
}

func TestVirtualConn_Deadlines(t *testing.T) {
	s := NewSession("vc-dl")
	vc := NewVirtualConn(s, nil)

	if err := vc.SetDeadline(time.Now()); err != nil {
		t.Errorf("SetDeadline: %v", err)
	}
	if err := vc.SetReadDeadline(time.Now()); err != nil {
		t.Errorf("SetReadDeadline: %v", err)
	}
	if err := vc.SetWriteDeadline(time.Now()); err != nil {
		t.Errorf("SetWriteDeadline: %v", err)
	}
}

func TestVirtualConn_ImplementsNetConn(t *testing.T) {
	s := NewSession("vc-iface")
	vc := NewVirtualConn(s, nil)

	// Compile-time check that VirtualConn implements net.Conn
	var _ io.ReadWriteCloser = vc
}

func TestVirtualConn_CloseUnblocksBackpressure(t *testing.T) {
	s := NewSession("vc-bp-close")
	vc := NewVirtualConn(s, nil)

	// Fill txBuf past backpressure threshold
	big := make([]byte, 3*1024*1024)
	s.EnqueueTx(big)

	done := make(chan struct{})
	go func() {
		vc.Write([]byte("blocked"))
		close(done)
	}()

	// Verify it blocks
	select {
	case <-done:
		t.Fatal("Write should be blocked by backpressure")
	case <-time.After(200 * time.Millisecond):
	}

	// Close should unblock
	vc.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Write still blocked after Close")
	}
}
