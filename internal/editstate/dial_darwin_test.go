//go:build darwin

package editstate

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestDialPublisher(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReceiver("session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.ServeAuthenticated(ctx, listener, time.Second, 4) }()
	defer func() { cancel(); <-done }()
	p, err := DialPublisher(listener.Addr().String(), "session", time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Update("café", 5); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if s, ok := r.Snapshot(); ok && s.Line == "café" && s.Cursor == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update not received")
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		if _, ok := r.Snapshot(); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clear not received")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := DialPublisher(listener.Addr().String(), "wrong", time.Second, time.Second); err == nil {
		t.Fatal("wrong session accepted")
	}
}

func TestDialPublisherBadTimeout(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := DialPublisher(listener.Addr().String(), "session", time.Second, 0); err == nil {
		t.Fatal("zero write timeout accepted")
	}
	if err := listener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.AcceptUnix()
	if err == nil {
		conn.Close()
		t.Fatal("invalid parameters initiated a connection")
	}
	if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
		t.Fatalf("accept: %v", err)
	}
}
