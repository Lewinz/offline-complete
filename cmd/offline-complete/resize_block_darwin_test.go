//go:build darwin

package main

import (
	"bufio"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"offline-complete/internal/session"
)

func TestResizeFailureClosesBlockedPTY(t *testing.T) {
	master, terminal, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	if err := terminal.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := session.Spawn("/bin/sh", "-c", "stty raw -echo; trap '' HUP; printf 'ready\\n'; while :; do :; done")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	go func() { _, _ = io.WriteString(s.Writer(), strings.Repeat("x", 16<<20)); close(written) }()
	select {
	case <-written:
		t.Fatal("PTY write did not block")
	case <-time.After(200 * time.Millisecond):
	}
	signals := make(chan os.Signal)
	stop := startResize(s, terminal, signals)
	signals <- syscall.SIGWINCH
	done := make(chan error, 1)
	go func() { done <- stop() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "read terminal size") {
			t.Fatalf("resize error = %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = s.Close()
		<-done
		t.Fatal("resize worker waited for termination before PTY cleanup")
	}
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		t.Fatal("PTY write remained blocked")
	}
}
