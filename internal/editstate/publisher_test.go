package editstate

import (
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"
)

func awaitSnapshot(t *testing.T, s *State, version uint64, present bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snap, ok := s.Snapshot()
		if ok == present && (!present || snap.Version == version) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("snapshot did not reach version %d, present=%v", version, present)
}

func TestPublisherOrderedStream(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	p, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	if err := p.Update("café", 5); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 1, true)
	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 0, false)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- p.Update("ok", 2) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	awaitSnapshot(t, &state, 12, true)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not exit")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("close left a snapshot")
	}
	if err := p.Clear(); err == nil {
		t.Fatal("closed publisher accepted clear")
	}
}

func TestPublisherInvalidUpdateClosesStream(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	p, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	if err := p.Update("old", 3); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 1, true)
	if err := p.Update("é", 1); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalid update left connection open")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("stale snapshot survived")
	}
	if err := p.Update("new", 3); err == nil {
		t.Fatal("failed publisher was reused")
	}
}

func TestPublisherWriteTimeoutAndOverflowClose(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		client, server := net.Pipe()
		p, err := NewPublisher(client, "session", 20*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if overflow {
			p.sequence = math.MaxUint64
		}
		if err := p.Clear(); err == nil {
			t.Fatal("expected failure")
		}
		server.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := server.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("connection not closed: %v", err)
		}
		if err := p.Clear(); err == nil {
			t.Fatal("failed publisher was reused")
		}
		server.Close()
	}
}

func TestPublisherRejectsBadParametersWithoutClosingConnection(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if _, err := NewPublisher(client, "", time.Second); err == nil {
		t.Fatal("empty session accepted")
	}
	if _, err := NewPublisher(client, "session", 0); err == nil {
		t.Fatal("zero timeout accepted")
	}
	go func() { _, _ = client.Write([]byte("x")) }()
	server.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if _, err := server.Read(b); err != nil || string(b) != "x" {
		t.Fatalf("connection taken: %q, %v", b, err)
	}
}

type publisherCloseFailure struct {
	net.Conn

	err error
}

func (c *publisherCloseFailure) Close() error {

	_ = c.Conn.Close()

	return c.err

}

func TestPublisherReportsCloseFailureAfterWriteFailure(t *testing.T) {

	client, server := net.Pipe()

	failure := errors.New("close failure")

	p, err := NewPublisher(&publisherCloseFailure{Conn: client, err: failure}, "session", time.Second)

	if err != nil {
		t.Fatal(err)
	}

	server.Close()

	if err := p.Clear(); !errors.Is(err, failure) {
		t.Fatalf("close error lost: %v", err)
	}

	if err := p.Close(); !errors.Is(err, failure) {
		t.Fatalf("close result not preserved: %v", err)
	}

	if err := p.Update("late", 4); err == nil {
		t.Fatal("write after failure succeeded")
	}

}

type notifyingPublisherConn struct {
	net.Conn
	writing chan struct{}
}

func (c *notifyingPublisherConn) Write(b []byte) (int, error) {
	close(c.writing)
	return c.Conn.Write(b)
}

func TestPublisherCloseInterruptsStalledWrite(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	conn := &notifyingPublisherConn{Conn: client, writing: make(chan struct{})}
	p, err := NewPublisher(conn, "session", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	writing := make(chan error, 1)
	go func() { writing <- p.Clear() }()
	select {
	case <-conn.writing:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close waited for stalled write deadline")
	}
	select {
	case err := <-writing:
		if err == nil {
			t.Fatal("interrupted write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("write was not interrupted")
	}
	if err := p.Clear(); err == nil {
		t.Fatal("closed publisher accepted another message")
	}
}

func TestPublisherCloseDuringStalledWrite(t *testing.T) {

	client, server := net.Pipe()

	defer server.Close()

	p, err := NewPublisher(client, "session", 50*time.Millisecond)

	if err != nil {
		t.Fatal(err)
	}

	writing := make(chan error, 1)

	go func() { writing <- p.Clear() }()

	time.Sleep(10 * time.Millisecond)

	closed := make(chan error, 1)

	go func() { closed <- p.Close() }()

	select {

	case err := <-writing:
		if err == nil {
			t.Fatal("stalled write succeeded")
		}

	case <-time.After(time.Second):
		t.Fatal("write did not time out")

	}

	select {

	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}

	case <-time.After(time.Second):
		t.Fatal("close did not return")

	}

}
