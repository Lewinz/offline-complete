package ipc

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestReadHandshakeTimeoutOnPartialFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() { _, _ = client.Write([]byte{0, 0}) }()
	start := time.Now()
	_, err := ReadHandshakeWithTimeout(server, "session", 50*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("partial frame error = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("partial frame blocked beyond timeout")
	}
}

func TestReadHandshakeTimeoutClearsDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		_ = WriteHandshake(client, Handshake{Version: 1, SessionID: "session"})
		time.Sleep(100 * time.Millisecond)
		_, _ = client.Write([]byte("x"))
	}()
	if _, err := ReadHandshakeWithTimeout(server, "session", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := server.Read(b[:]); err != nil || b[0] != 'x' {
		t.Fatalf("subsequent read = %q, %v", b, err)
	}
}

func TestWriteHandshakeTimeoutWhenPeerDoesNotRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	err := WriteHandshakeWithTimeout(server, Handshake{Version: 1, SessionID: "session"}, 50*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("blocked write error = %v, want deadline exceeded", err)
	}
}

func TestReadHandshakeTimeoutRejectsNonpositiveTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	if _, err := ReadHandshakeWithTimeout(server, "session", 0); err == nil {
		t.Fatal("zero timeout accepted")
	}
}
