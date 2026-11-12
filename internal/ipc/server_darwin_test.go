//go:build darwin

package ipc

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerBadID(t *testing.T) {
	for _, id := range []string{string([]byte{0xff}), strings.Repeat("x", MaxFrame+1), strings.Repeat("\\", MaxFrame/2)} {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "s"), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- ServeAuthenticated(ctx, listener, id, time.Second, 1, func(context.Context, *net.UnixConn) {})
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Error("unencodable session accepted")
			}
		case <-time.After(200 * time.Millisecond):
			t.Error("unencodable session started serving instead of returning an error")
			cancel()
			<-done
		}
		cancel()
		_ = listener.Close()
	}
}

func TestServerReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(ctx, listener, "session", time.Second, 3, func(_ context.Context, c *net.UnixConn) { _, _ = c.Write([]byte("ok")) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	stalled, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	for i := 0; i < 2; i++ {
		c, err := DialAuthenticated(path, "session", time.Second)
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		buf := make([]byte, 2)
		_, err = io.ReadFull(c, buf)
		c.Close()
		if err != nil || string(buf) != "ok" {
			t.Fatalf("connection %d: %q, %v", i, buf, err)
		}
	}
}

func TestServerConcurrentSessionsAreIsolated(t *testing.T) {
	base := privateBase(t)
	first, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPrivateListener(base)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	for _, s := range []struct {
		listener *PrivateListener
		id       string
	}{{first, "first"}, {second, "second"}} {
		go func() {
			done <- ServeAuthenticated(ctx, s.listener.Listener(), s.id, time.Second, 4, func(_ context.Context, c *net.UnixConn) { _, _ = c.Write([]byte(s.id)) })
		}()
	}
	defer func() {
		cancel()
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
		for _, l := range []*PrivateListener{first, second} {
			if err := l.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, tc := range []struct {
		listener *PrivateListener
		id       string
	}{{first, "first"}, {second, "second"}} {
		c, err := DialAuthenticated(tc.listener.Path(), tc.id, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(tc.id))
		_, err = io.ReadFull(c, buf)
		c.Close()
		if err != nil || string(buf) != tc.id {
			t.Fatalf("session %q: got %q, %v", tc.id, buf, err)
		}
	}
	if c, err := DialAuthenticated(first.Path(), "second", time.Second); err == nil {
		c.Close()
		t.Fatal("first listener accepted second session")
	}
	c, err := DialAuthenticated(first.Path(), "first", time.Second)
	if err != nil {
		t.Fatalf("valid session failed after rejected handshake: %v", err)
	}
	c.Close()
}

func TestServerLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		done <- ServeAuthenticated(ctx, listener, "session", time.Second, 1, func(_ context.Context, c *net.UnixConn) {
			close(entered)
			_, _ = io.Copy(io.Discard, c)
		})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	first, err := DialAuthenticated(path, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	second, err := DialAuthenticated(path, "session", time.Second)
	if err == nil {
		second.Close()
		t.Fatal("connection over limit accepted")
	}
}

func TestServerRateLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(ctx, listener, "session", time.Second, 128, func(_ context.Context, c *net.UnixConn) { _, _ = c.Write([]byte("x")) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	denied := false
	for i := 0; i < 80; i++ {
		c, err := DialAuthenticated(path, "session", time.Second)
		if err != nil {
			denied = true
			break
		}
		var b [1]byte
		_, err = io.ReadFull(c, b[:])
		c.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if !denied {
		t.Fatal("unbounded connection rate")
	}
	time.Sleep(300 * time.Millisecond)
	c, err := DialAuthenticated(path, "session", time.Second)
	if err != nil {
		t.Fatalf("server did not recover after rate limit: %v", err)
	}
	c.Close()
}

func TestServerCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(ctx, listener, "session", time.Second, 2, func(_ context.Context, c *net.UnixConn) { close(entered); _, _ = io.Copy(io.Discard, c) })
	}()
	c, err := DialAuthenticated(path, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := c.Read(b[:]); err == nil {
		t.Fatal("connection remained open")
	}
}
