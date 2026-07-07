package netconf

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockTransport is a minimal Transport implementation for exercising the
// listen() loop without a real SSH connection.
type mockTransport struct {
	receiveCount int64
	closeCount   int64
	closed       chan struct{}
	once         sync.Once

	// recv is invoked for every Receive() call so tests can control what
	// happens (return data, return an error, mutate the session, etc).
	recv func(n int64) ([]byte, error)
}

func newMockTransport(recv func(n int64) ([]byte, error)) *mockTransport {
	return &mockTransport{closed: make(chan struct{}), recv: recv}
}

func (m *mockTransport) Send([]byte) error { return nil }

func (m *mockTransport) Receive() ([]byte, error) {
	n := atomic.AddInt64(&m.receiveCount, 1)
	return m.recv(n)
}

func (m *mockTransport) Close() error {
	atomic.AddInt64(&m.closeCount, 1)
	m.once.Do(func() { close(m.closed) })
	return nil
}

func (m *mockTransport) SetVersion(string) {}

func newTestSession(t Transport) *Session {
	s := &Session{
		Transport: t,
		logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Listener:  &Dispatcher{},
	}
	s.Listener.init()
	return s
}

// TestListenBreaksOnReceiveError is the core regression test for the
// busy-loop bug: when Transport.Receive() returns an error the listen loop
// must exit rather than spin calling Receive() forever (100% CPU).
func TestListenBreaksOnReceiveError(t *testing.T) {
	transport := newMockTransport(func(int64) ([]byte, error) {
		return nil, errors.New("connection reset by peer")
	})
	session := newTestSession(transport)

	session.listen()

	// The loop should tear the transport down promptly. If the bug were
	// present, Close() would never be called and Receive() would spin.
	select {
	case <-transport.closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("listen loop did not exit within 2s; Receive called %d times (busy loop?)",
			atomic.LoadInt64(&transport.receiveCount))
	}

	// Give the goroutine a moment to fully unwind, then assert it is not
	// still calling Receive().
	time.Sleep(50 * time.Millisecond)
	first := atomic.LoadInt64(&transport.receiveCount)
	time.Sleep(100 * time.Millisecond)
	second := atomic.LoadInt64(&transport.receiveCount)

	if second != first {
		t.Fatalf("Receive() still being called after error: %d -> %d (busy loop)", first, second)
	}
	if first != 1 {
		t.Errorf("expected exactly 1 Receive() call, got %d", first)
	}
	if got := atomic.LoadInt64(&transport.closeCount); got != 1 {
		t.Errorf("expected transport Close() to be called once, got %d", got)
	}
	if !session.IsClosed {
		t.Errorf("expected session.IsClosed to be true after receive error")
	}
}

// TestListenQuietExitOnCallerClose verifies that when the caller closes the
// session (IsClosed already true) and the pending Receive() then returns an
// error, the loop exits without trying to close the transport a second time.
func TestListenQuietExitOnCallerClose(t *testing.T) {
	var session *Session
	transport := newMockTransport(func(int64) ([]byte, error) {
		// Simulate the caller having closed the session concurrently: by
		// the time Receive returns its error, IsClosed is already set.
		session.IsClosed = true
		return nil, errors.New("use of closed connection")
	})
	session = newTestSession(transport)

	done := make(chan struct{})
	go func() {
		session.listen()
		// listen() itself only spawns the goroutine; wait for the loop to
		// actually stop touching the transport before signaling.
		close(done)
	}()

	// Poll until Receive has been called at least once and count settles.
	deadline := time.After(2 * time.Second)
	var last int64
	for {
		select {
		case <-deadline:
			t.Fatalf("listen loop kept running; Receive called %d times",
				atomic.LoadInt64(&transport.receiveCount))
		case <-time.After(30 * time.Millisecond):
		}
		cur := atomic.LoadInt64(&transport.receiveCount)
		if cur >= 1 && cur == last {
			break
		}
		last = cur
	}

	// Caller-initiated close path must NOT re-close the transport from the
	// listen loop (Close() count should stay at 0 here).
	if got := atomic.LoadInt64(&transport.closeCount); got != 0 {
		t.Errorf("expected listen loop not to close transport on caller-close path, got %d", got)
	}
	<-done
}
