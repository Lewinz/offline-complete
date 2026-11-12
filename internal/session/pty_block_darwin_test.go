//go:build darwin

package session

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestBlockedPTYWriteTerminationCharacterization observes whether draining the
// master affects termination after a large input write. It does not treat a
// process-group signal failure as successful termination.
func TestBlockedPTYWriteTerminationCharacterization(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "stty raw -echo; trap '' HUP; printf 'ready\\n'; while :; do :; done")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = s.pty.Close()
		_ = s.cmd.Process.Kill()
	}()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	type writeResult struct {
		n   int
		err error
	}
	written := make(chan writeResult, 1)
	go func() {
		n, err := io.WriteString(s.Writer(), strings.Repeat("x", 16<<20))
		written <- writeResult{n, err}
	}()
	select {
	case result := <-written:
		t.Fatalf("write unexpectedly completed: %d bytes, %v", result.n, result.err)
	case <-time.After(200 * time.Millisecond):
	}
	terminated := make(chan error, 1)
	go func() { _, err := s.Terminate(); terminated <- err }()
	select {
	case err := <-terminated:
		t.Logf("termination before drain: %v", err)
	case <-time.After(1200 * time.Millisecond):
		select {
		case result := <-written:
			t.Logf("write completed before drain: %d bytes, %v", result.n, result.err)
		default:
			t.Log("write still pending before drain")
		}
		t.Log("termination still waiting; draining PTY")
		go io.Copy(io.Discard, s.Reader())
		select {
		case err := <-terminated:
			t.Logf("termination after drain: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("termination did not finish after drain")
		}
	}
}

// TestPTYOutputFlushTerminationCharacterization compares a terminal output
// flush with draining. The outcome is observational, not a boundedness claim.
func TestPTYOutputFlushTerminationCharacterization(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_PTY_DIAGNOSTIC") != "1" {
		t.Skip("set OFFLINE_COMPLETE_PTY_DIAGNOSTIC=1 to run the PTY load diagnostic")
	}
	s, err := Spawn("/bin/sh", "-c", "stty raw -echo; trap '' HUP; printf 'ready\\n'; while :; do :; done")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.pty.Close(); _ = s.cmd.Process.Kill() }()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { _, err := io.WriteString(s.Writer(), strings.Repeat("x", 16<<20)); written <- err }()
	select {
	case err := <-written:
		t.Fatalf("write unexpectedly completed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	terminated := make(chan error, 1)
	go func() { _, err := s.Terminate(); terminated <- err }()
	select {
	case err := <-terminated:
		t.Logf("termination before flush: %v", err)
		return
	case <-time.After(1200 * time.Millisecond):
	}
	// Darwin TIOCFLUSH takes a pointer to a bitmask; FWRITE is 2.
	flushErr := unix.IoctlSetPointerInt(int(s.pty.Fd()), unix.TIOCFLUSH, 2)
	t.Logf("master output flush: %v", flushErr)
	select {
	case err := <-terminated:
		t.Logf("termination after flush: %v", err)
	case <-time.After(time.Second):
		t.Log("termination still waiting after flush; draining PTY")
		go io.Copy(io.Discard, s.Reader())
		select {
		case err := <-terminated:
			t.Logf("termination after drain: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("termination did not finish after drain")
		}
	}
}

func TestCloseReturnsWhenPTYInputIsBlocked(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "stty raw -echo; trap '' HUP; printf 'ready\\n'; while :; do :; done")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.pty.Close(); _ = s.cmd.Process.Kill() }()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { _, err := io.WriteString(s.Writer(), strings.Repeat("x", 16<<20)); written <- err }()
	select {
	case err := <-written:
		t.Fatalf("write unexpectedly completed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	s.signalGroup = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			return syscall.EPERM
		}
		return syscall.Kill(pid, sig)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, syscall.EPERM) {
			t.Fatalf("Close must report the group signal error: %v", err)
		}
	case <-time.After(2 * time.Second):
		go io.Copy(io.Discard, s.Reader())
		select {
		case err := <-closed:
			t.Fatalf("Close blocked until output was drained: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("Close remained blocked after drain")
		}
	}
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		t.Fatal("write remained blocked after Close")
	}
}
