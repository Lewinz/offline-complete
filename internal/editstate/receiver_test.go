package editstate

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type closeErrorConn struct {
	net.Conn
	err error
}

func (c closeErrorConn) Close() error {
	_ = c.Conn.Close()
	return c.err
}

func TestReceiverReportsCloseErrorOnDisconnect(t *testing.T) {
	r, err := NewReceiver("s", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	closeErr := errors.New("connection close failed")
	done := make(chan error, 1)
	go func() { done <- r.Consume(context.Background(), closeErrorConn{Conn: a, err: closeErr}) }()
	if err := WriteUpdate(b, "s", 1, "git", 3); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	select {
	case err := <-done:
		if !errors.Is(err, closeErr) || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("expected both read deadline and close errors, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver did not return")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("disconnect retained snapshot")
	}
}

func TestReceiverRejectsInvalidSessionAtConstruction(t *testing.T) {
	for _, session := range []string{"", "\xff", string(make([]byte, 64*1024))} {
		if _, err := NewReceiver(session, time.Second); err == nil {
			t.Fatal("invalid session accepted")
		}
	}
}

func TestReceiverRejectsSessionThatCannotCarryAnUpdate(t *testing.T) {
	// A clear frame fits, but even an empty update exceeds the frame limit.
	session := strings.Repeat("a", 65536-57)
	if err := WriteClear(io.Discard, session, 1); err != nil {
		t.Fatalf("clear should fit: %v", err)
	}
	if err := WriteUpdate(io.Discard, session, 1, "", 0); err == nil {
		t.Fatal("update unexpectedly fits")
	}
	if _, err := NewReceiver(session, time.Second); err == nil {
		t.Fatal("receiver accepted a session that cannot carry an update")
	}
}

func TestReceiverRejectsConcurrentConnectionAndAllowsReconnect(t *testing.T) {
	r, err := NewReceiver("s", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- r.Consume(context.Background(), a) }()
	if err := WriteUpdate(b, "s", 1, "git", 3); err != nil {
		t.Fatal(err)
	}
	waitSnapshot := func(want string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if s, ok := r.Snapshot(); ok && s.Line == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("snapshot %q not observed", want)
	}
	waitSnapshot("git")
	c, d := net.Pipe()
	if err := r.Consume(context.Background(), c); err == nil {
		t.Fatal("concurrent connection accepted")
	}
	_ = d.Close()
	if s, ok := r.Snapshot(); !ok || s.Line != "git" {
		t.Fatal("rejected connection changed active snapshot")
	}
	_ = b.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not stop consumer")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("disconnect retained snapshot")
	}
	e, f := net.Pipe()
	defer f.Close()
	go func() { done <- r.Consume(context.Background(), e) }()
	if err := WriteUpdate(f, "s", 1, "new", 3); err != nil {
		t.Fatal(err)
	}
	waitSnapshot("new")
	_ = f.Close()
	<-done
}

type delayedAfterContext struct {
	context.Context
	release <-chan struct{}
}

func (c delayedAfterContext) AfterFunc(f func()) func() bool {
	stopped := make(chan struct{})
	go func() {
		select {
		case <-c.Done():
			select {
			case <-c.release:
				f()
			case <-stopped:
			}
		case <-stopped:
		}
	}()
	return func() bool { close(stopped); return true }
}

func TestReceiverCancellationHidesSnapshotBeforeCallbackRuns(t *testing.T) {
	r, err := NewReceiver("s", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- r.Consume(delayedAfterContext{Context: ctx, release: release}, a) }()
	defer func() {
		close(release)
		_ = b.Close()
		<-done
	}()
	if err := WriteUpdate(b, "s", 1, "secret", 6); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := r.Snapshot(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if _, ok := r.Snapshot(); ok {
		t.Fatal("cancelled context still exposes snapshot before callback")
	}
}

type slowCloseConn struct {
	net.Conn
	started chan struct{}
	release chan struct{}
}

func (c *slowCloseConn) Close() error {
	select {
	case <-c.started:
	default:
		close(c.started)
	}
	<-c.release
	return c.Conn.Close()
}

func TestReceiverRejectsReconnectUntilOldConnectionCloses(t *testing.T) {
	r, err := NewReceiver("s", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	conn := &slowCloseConn{Conn: a, started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- r.Consume(context.Background(), conn) }()
	defer func() {
		_ = b.Close()
		close(conn.release)
		<-done
	}()
	if err := WriteUpdate(b, "s", 1, "git", 3); err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("old connection did not start closing")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("old snapshot remained visible during close")
	}
	c, d := net.Pipe()
	defer d.Close()
	result := make(chan error, 1)
	go func() { result <- r.Consume(context.Background(), c) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("reconnect accepted before old connection closed")
		}
	case <-time.After(time.Second):
		_ = d.Close()
		<-result
		t.Fatal("reconnect accepted before old connection closed")
	}
}

type firstCloseBlocksConn struct {
	net.Conn
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (c *firstCloseBlocksConn) Close() error {
	err := c.Conn.Close()
	if c.calls.Add(1) == 1 {
		close(c.started)
		<-c.release
	}
	return err
}

func TestReceiverCancellationWaitsForInFlightCloseBeforeReconnect(t *testing.T) {
	r, err := NewReceiver("s", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	conn := &firstCloseBlocksConn{Conn: a, started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Consume(ctx, conn) }()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(conn.release) }) }
	defer func() { cancel(); release() }()
	if err := WriteUpdate(b, "s", 1, "git", 3); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not start closing connection")
	}
	select {
	case err := <-done:
		t.Fatalf("consumer returned before first close finished: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	c, d := net.Pipe()
	defer d.Close()
	if err := r.Consume(context.Background(), c); err == nil {
		t.Fatal("reconnect accepted while first close was blocked")
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not return after close completed")
	}
	if got := conn.calls.Load(); got != 1 {
		t.Fatalf("connection closed %d times, want once", got)
	}
	e, f := net.Pipe()
	defer f.Close()
	go func() { done <- r.Consume(context.Background(), e) }()
	if err := WriteUpdate(f, "s", 1, "new", 3); err != nil {
		t.Fatalf("reconnect after close: %v", err)
	}
	_ = f.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconnected consumer did not exit")
	}
}

func TestReceiverCancellationClearsSnapshotBeforeBlockedClose(t *testing.T) {
	r, err := NewReceiver("s", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	conn := &slowCloseConn{Conn: a, started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Consume(ctx, conn) }()
	defer func() {
		cancel()
		close(conn.release)
		<-done
	}()
	if err := WriteUpdate(b, "s", 1, "secret", 6); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := r.Snapshot(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not attempt to close connection")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("cancel retained snapshot while connection close blocked")
	}
}

func TestReceiverCancellationClosesConnectionAndClearsSnapshot(t *testing.T) {
	r, err := NewReceiver("s", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Consume(ctx, a) }()
	if err := WriteUpdate(b, "s", 1, "secret", 6); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := r.Snapshot(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop consumer")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("cancel retained snapshot")
	}
	if _, err := b.Write([]byte("x")); err == nil {
		t.Fatal("connection remains open")
	}
}
