package transport

import (
	"sync"
	"time"
)

// Direction indicates if a file is req (client to server) or res (server to client)
type Direction string

const (
	DirReq Direction = "req"
	DirRes Direction = "res"
)

// Session represents an active proxy connection mapped to files.
type Session struct {
	ID           string
	mu           sync.Mutex
	txBuf        []byte
	txSeq        uint64
	rxSeq        uint64
	rxQueue      map[uint64]*Envelope
	lastActivity time.Time
	closed       bool
	rxClosed     bool // Safely tracks if RxChan was successfully closed
	TargetAddr   string
	ClientID     string

	// Backpressure: blocked when txBuf is too large
	txCond *sync.Cond

	// closeOnce ensures RxChan is closed exactly once, preventing panics
	// when multiple goroutines race to close the session.
	closeOnce sync.Once

	// App channel for receiving data downloaded from remote
	RxChan chan []byte
}

func NewSession(id string) *Session {
	s := &Session{
		ID:           id,
		rxQueue:      make(map[uint64]*Envelope),
		lastActivity: time.Now(),
		RxChan:       make(chan []byte, 1024),
	}
	s.txCond = sync.NewCond(&s.mu)
	return s
}

func (s *Session) EnqueueTx(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// BACKPRESSURE: Block if txBuf is larger than 2MB
	// This prevents memory explosion when uploading through the proxy
	for len(s.txBuf) > 2*1024*1024 && !s.closed {
		s.txCond.Wait()
	}

	s.txBuf = append(s.txBuf, data...)
	s.lastActivity = time.Now()
}

func (s *Session) ClearTx() {
	s.mu.Lock()
	s.txBuf = nil
	s.txCond.Broadcast() // Wake up any writers blocked on backpressure
	s.mu.Unlock()
}

func (s *Session) closeRxChan() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.rxClosed = true
		s.closed = true
		s.mu.Unlock()
		close(s.RxChan)
	})
}

// CloseRx closes the receive channel from the outside (e.g., when the TCP connection drops).
func (s *Session) CloseRx() {
	s.closeRxChan()
}

func (s *Session) ProcessRx(env *Envelope) {
	s.mu.Lock()
	s.lastActivity = time.Now()

	if s.rxClosed {
		s.mu.Unlock()
		return
	}

	var toDeliver [][]byte
	var shouldClose bool

	if env.Seq == s.rxSeq {
		if len(env.Payload) > 0 {
			toDeliver = append(toDeliver, env.Payload)
		}
		s.rxSeq++
		if env.Close {
			shouldClose = true
		}

		// process any queued future packets
		if !shouldClose {
			for {
				if nextEnv, ok := s.rxQueue[s.rxSeq]; ok {
					if len(nextEnv.Payload) > 0 {
						toDeliver = append(toDeliver, nextEnv.Payload)
					}
					delete(s.rxQueue, s.rxSeq)
					s.rxSeq++
					if nextEnv.Close {
						shouldClose = true
						break
					}
				} else {
					break
				}
			}
		}
	} else if env.Seq > s.rxSeq {
		s.rxQueue[env.Seq] = env
	}
	s.mu.Unlock()

	// Deliver outside the lock — channel sends can block without causing deadlock
	for _, p := range toDeliver {
		s.RxChan <- p
	}
	if shouldClose {
		s.closeRxChan()
	}
}
