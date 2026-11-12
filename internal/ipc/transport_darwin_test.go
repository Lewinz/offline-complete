//go:build darwin

package ipc

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestTransportBadID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	badID := string([]byte{0xff})
	_ = listener.SetDeadline(time.Now().Add(150 * time.Millisecond))
	start := time.Now()
	conn, err := AcceptAuthenticated(listener, badID, time.Second)
	if conn != nil {
		conn.Close()
	}
	if err == nil || time.Since(start) >= 100*time.Millisecond {
		t.Error("invalid session did not fail before accepting")
	}
	_ = listener.SetDeadline(time.Time{})
	conn, err = DialAuthenticated(path, badID, time.Second)
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Error("invalid session accepted by dial")
	}
	_ = listener.SetDeadline(time.Now().Add(100 * time.Millisecond))
	accepted, err := listener.AcceptUnix()
	if err == nil {
		accepted.Close()
		t.Error("invalid session dialed the socket")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Errorf("accept: %v", err)
	}
}

func TestTransportReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	first := make(chan error, 1)
	go func() { _, err := AcceptAuthenticated(listener, "right", time.Second); first <- err }()
	if conn, err := DialAuthenticated(path, "wrong", time.Second); err == nil {
		conn.Close()
		t.Fatal("wrong session accepted")
	}
	if err := <-first; err == nil {
		t.Fatal("listener accepted wrong session")
	}

	second := make(chan error, 1)
	go func() {
		conn, err := AcceptAuthenticated(listener, "right", time.Second)
		if err == nil {
			defer conn.Close()
			_, err = conn.Write([]byte("x"))
		}
		second <- err
	}()
	conn, err := DialAuthenticated(path, "right", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil || string(buf) != "x" {
		t.Fatalf("read after reconnect: %q, %v", buf, err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
