//go:build darwin

package editstate

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/ipc"
)

func TestAuthenticatedEditingConnection(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	state := &State{}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err == nil {
			err = ConsumeAuthenticatedConn(conn, "session", state, time.Second, time.Second)
		}
		done <- err
	}()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := ipc.ConnectHandshake(client, "session", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := WriteUpdateConn(client, "session", 1, "git status", 10, time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if snapshot, ok := state.Snapshot(); ok {
			if snapshot.Line != "git status" || snapshot.Cursor != 10 || snapshot.Version != 1 {
				t.Fatalf("unexpected snapshot: %+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authenticated update not received")
		}
		time.Sleep(time.Millisecond)
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not stop consumer")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("disconnect retained snapshot")
	}
	if err := state.Update(2, "late", 4); err == nil {
		t.Fatal("disconnect allowed late update")
	}
}

func TestBadEditingSession(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	state := &State{}
	if err := state.Update(1, "old", 3); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- ConsumeAuthenticatedConn(server, strings.Repeat("a", 65536-57), state, time.Second, time.Second)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unusable session accepted")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("waited for handshake with an unusable editing session")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("rejected session retained snapshot")
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := client.Read(b[:]); err == nil {
		t.Fatal("rejected connection remained open")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("rejected connection was not closed")
	}
}

func TestEditingWrongID(t *testing.T) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	state := &State{}
	if err := state.Update(1, "old", 3); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err == nil {
			err = ConsumeAuthenticatedConn(conn, "session", state, time.Second, time.Second)
		}
		done <- err
	}()
	client, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := ipc.ConnectHandshake(client, "wrong", time.Second); err == nil {
		t.Fatal("wrong session accepted")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("server accepted wrong session")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not reject wrong session")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("failed handshake retained snapshot")
	}
	if err := state.Update(2, "late", 4); err == nil {
		t.Fatal("failed handshake allowed late update")
	}
}
