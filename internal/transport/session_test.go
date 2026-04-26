package transport

import (
	"sync"
	"testing"
	"time"
)

func TestSession_ProcessRx_InOrder(t *testing.T) {
	s := NewSession("test-inorder")

	payloads := []string{"first", "second", "third"}
	for i, p := range payloads {
		s.ProcessRx(&Envelope{
			SessionID: "test-inorder",
			Seq:       uint64(i),
			Payload:   []byte(p),
		})
	}

	for i, expected := range payloads {
		select {
		case data := <-s.RxChan:
			if string(data) != expected {
				t.Errorf("packet %d: got %q, want %q", i, string(data), expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for packet %d", i)
		}
	}
}

func TestSession_ProcessRx_OutOfOrder(t *testing.T) {
	s := NewSession("test-ooo")

	// Send packets 2, 0, 1 — should deliver in order 0, 1, 2
	s.ProcessRx(&Envelope{SessionID: "test-ooo", Seq: 2, Payload: []byte("two")})
	s.ProcessRx(&Envelope{SessionID: "test-ooo", Seq: 0, Payload: []byte("zero")})

	// "zero" should now be delivered (seq=0 expected)
	select {
	case data := <-s.RxChan:
		if string(data) != "zero" {
			t.Errorf("expected 'zero', got %q", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for 'zero'")
	}

	// Now send seq=1 which fills the gap
	s.ProcessRx(&Envelope{SessionID: "test-ooo", Seq: 1, Payload: []byte("one")})

	// "one" then "two" should be delivered
	expected := []string{"one", "two"}
	for _, want := range expected {
		select {
		case data := <-s.RxChan:
			if string(data) != want {
				t.Errorf("expected %q, got %q", want, string(data))
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for %q", want)
		}
	}
}

func TestSession_ProcessRx_CloseDelivered(t *testing.T) {
	s := NewSession("test-close")

	s.ProcessRx(&Envelope{
		SessionID: "test-close",
		Seq:       0,
		Payload:   []byte("final"),
		Close:     true,
	})

	// Read the payload
	select {
	case data := <-s.RxChan:
		if string(data) != "final" {
			t.Errorf("expected 'final', got %q", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	// Channel should now be closed
	select {
	case _, ok := <-s.RxChan:
		if ok {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestSession_ProcessRx_DuplicateIgnored(t *testing.T) {
	s := NewSession("test-dup")

	s.ProcessRx(&Envelope{SessionID: "test-dup", Seq: 0, Payload: []byte("hello")})
	// Duplicate of seq=0 — should be silently ignored (seq < rxSeq)
	s.ProcessRx(&Envelope{SessionID: "test-dup", Seq: 0, Payload: []byte("duplicate")})

	select {
	case data := <-s.RxChan:
		if string(data) != "hello" {
			t.Errorf("expected 'hello', got %q", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	// Channel should have nothing else
	select {
	case data := <-s.RxChan:
		t.Errorf("unexpected extra data: %q", string(data))
	case <-time.After(100 * time.Millisecond):
		// Good — no extra data
	}
}

func TestSession_Backpressure(t *testing.T) {
	s := NewSession("test-bp")

	// Fill the TX buffer beyond 2MB to trigger backpressure.
	// The condition is len(txBuf) > 2*1024*1024, so we need > 2MB.
	bigChunk := make([]byte, 1024*1024) // 1MB
	s.EnqueueTx(bigChunk)
	s.EnqueueTx(bigChunk)
	s.EnqueueTx([]byte{0x01}) // Now at 2MB+1, exceeding the 2MB threshold

	// Next enqueue should block because txBuf > 2MB
	done := make(chan struct{})
	go func() {
		s.EnqueueTx(bigChunk) // This should block
		close(done)
	}()

	// Verify it's actually blocked
	select {
	case <-done:
		t.Fatal("EnqueueTx should have blocked on backpressure")
	case <-time.After(200 * time.Millisecond):
		// Good — it's blocked
	}

	// ClearTx should unblock
	s.ClearTx()

	select {
	case <-done:
		// Good — unblocked
	case <-time.After(time.Second):
		t.Fatal("EnqueueTx still blocked after ClearTx")
	}
}

func TestSession_Backpressure_ClosedUnblocks(t *testing.T) {
	s := NewSession("test-bp-close")

	bigChunk := make([]byte, 3*1024*1024) // 3MB — immediately triggers backpressure
	s.EnqueueTx(bigChunk)

	done := make(chan struct{})
	go func() {
		s.EnqueueTx([]byte("more")) // Should block
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("should be blocked")
	case <-time.After(200 * time.Millisecond):
	}

	// Close the session — should unblock the writer
	s.mu.Lock()
	s.closed = true
	s.txCond.Broadcast()
	s.mu.Unlock()

	select {
	case <-done:
		// Good
	case <-time.After(time.Second):
		t.Fatal("still blocked after close")
	}
}

func TestSession_CloseOnce_NoPanic(t *testing.T) {
	s := NewSession("test-once")

	// Simulate multiple concurrent close attempts — must not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			s.ProcessRx(&Envelope{
				SessionID: "test-once",
				Seq:       seq,
				Close:     true,
			})
		}(uint64(i))
	}

	wg.Wait()

	// Verify channel is closed
	_, ok := <-s.RxChan
	if ok {
		t.Error("expected RxChan to be closed")
	}
}

func TestSession_CloseOnce_MultipleDirect(t *testing.T) {
	s := NewSession("test-direct-close")

	// Call closeRxChan many times concurrently — must not panic
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.closeRxChan()
		}()
	}
	wg.Wait()

	if !s.rxClosed {
		t.Error("expected rxClosed to be true")
	}
	if !s.closed {
		t.Error("expected closed to be true")
	}
}
