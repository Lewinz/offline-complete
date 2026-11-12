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

func TestReceiverServer(t *testing.T) {
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
	handled := make(chan error, 4)
	go func() {
		done <- ipc.ServeAuthenticated(ctx, listener, "session", time.Second, 4, func(ctx context.Context, conn *net.UnixConn) {
			handled <- r.Consume(ctx, conn)
		})
	}()
	defer func() { cancel(); <-done }()
	dial := func(session string) (*net.UnixConn, error) {
		conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
		if err != nil {
			return nil, err
		}
		if err := ipc.ConnectHandshake(conn, session, time.Second); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	client, err := dial("session")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := WriteUpdateConn(client, "session", 1, "git", 3, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if s, ok := r.Snapshot(); ok && s.Line == "git" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no authenticated snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	if wrong, err := dial("wrong"); err == nil {
		wrong.Close()
		t.Fatal("wrong session accepted")
	}
	if s, ok := r.Snapshot(); !ok || s.Line != "git" {
		t.Fatal("bad handshake affected snapshot")
	}
	client.Close()
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("disconnect not processed")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("disconnect retained snapshot")
	}
	client2, err := dial("session")
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()
	if err := WriteUpdateConn(client2, "session", 1, "new", 3, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for {
		if s, ok := r.Snapshot(); ok && s.Line == "new" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reconnect did not publish snapshot")
		}
		time.Sleep(time.Millisecond)
	}
}
