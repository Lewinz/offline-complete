//go:build darwin

package ipc

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func socketPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.AcceptUnix()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestConnHandshake(t *testing.T) {
	client, server := socketPair(t)
	result := make(chan error, 1)
	go func() { result <- AcceptHandshake(server, "session", time.Second) }()
	if err := ConnectHandshake(client, "session", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := server.Read(buf); err != nil || string(buf) != "x" {
		t.Fatalf("connection unusable: %q, %v", buf, err)
	}
}

func TestConnRejectsSessionMismatch(t *testing.T) {
	client, server := socketPair(t)
	result := make(chan error, 1)
	go func() { result <- AcceptHandshake(server, "expected", time.Second) }()
	if err := ConnectHandshake(client, "wrong", time.Second); err == nil {
		t.Fatal("client accepted wrong session")
	}
	if err := <-result; err == nil {
		t.Fatal("server accepted wrong session")
	}
	if _, err := server.Write([]byte("x")); err == nil {
		t.Fatal("rejected connection remains open")
	}
}

func TestConnTimeoutClosesConnection(t *testing.T) {
	client, server := socketPair(t)
	start := time.Now()
	if err := AcceptHandshake(server, "session", 50*time.Millisecond); err == nil {
		t.Fatal("accepted silent peer")
	}
	if time.Since(start) > time.Second {
		t.Fatal("handshake did not time out")
	}
	if _, err := server.Write([]byte("x")); err == nil {
		t.Fatal("timed out connection remains open")
	}
	client.Close()
}
