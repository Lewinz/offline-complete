//go:build darwin

package editstate

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"offline-complete/internal/ipc"
)

func TestAuthenticatedReceiverService(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReceiver("edit-session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.ServeAuthenticated(ctx, listener, time.Second, 4) }()
	defer cancel()
	client, err := ipc.DialAuthenticated(listener.Addr().String(), "edit-session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := WriteUpdateConn(client, "edit-session", 1, "git status", 10, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if s, ok := r.Snapshot(); ok && s.Line == "git status" && s.Cursor == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authenticated update was not published")
		}
		time.Sleep(time.Millisecond)
	}
	if bad, err := ipc.DialAuthenticated(listener.Addr().String(), "other-session", time.Second); err == nil {
		bad.Close()
		t.Fatal("wrong session accepted")
	}
	if s, ok := r.Snapshot(); !ok || s.Line != "git status" {
		t.Fatal("bad handshake changed active snapshot")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("cancellation retained snapshot")
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil {
		t.Fatal("connection remained open")
	}
}
