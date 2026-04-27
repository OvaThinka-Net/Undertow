package main

import (
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type LogBuffer struct {
	mu    sync.Mutex
	lines []LogEntry
	max   int
	subs  map[chan LogEntry]struct{}
}

func NewLogBuffer(max int) *LogBuffer {
	return &LogBuffer{
		max:  max,
		subs: make(map[chan LogEntry]struct{}),
	}
}

// Write implements io.Writer so it can be used with log.SetOutput.
func (lb *LogBuffer) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	lb.Add(msg)
	return len(p), nil
}

func (lb *LogBuffer) Add(msg string) {
	entry := LogEntry{Time: time.Now(), Message: msg}
	lb.mu.Lock()
	lb.lines = append(lb.lines, entry)
	if len(lb.lines) > lb.max {
		lb.lines = lb.lines[len(lb.lines)-lb.max:]
	}
	subs := make([]chan LogEntry, 0, len(lb.subs))
	for ch := range lb.subs {
		subs = append(subs, ch)
	}
	lb.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (lb *LogBuffer) Lines() []LogEntry {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	out := make([]LogEntry, len(lb.lines))
	copy(out, lb.lines)
	return out
}

func (lb *LogBuffer) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 64)
	lb.mu.Lock()
	lb.subs[ch] = struct{}{}
	lb.mu.Unlock()
	return ch
}

func (lb *LogBuffer) Unsubscribe(ch chan LogEntry) {
	lb.mu.Lock()
	delete(lb.subs, ch)
	lb.mu.Unlock()
}
