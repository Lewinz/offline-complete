//go:build darwin

package session

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The helper runs separately so the parent can bound a stuck Close or write.
func TestIsolatedCloseWithBlockedRawPTYWrite(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_RAW_WRITE_HELPER") == "1" {
		testBlockedRawPTYWrite(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIsolatedCloseWithBlockedRawPTYWrite$", "-test.v")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_RAW_WRITE_HELPER=1")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("isolated PTY test timed out: %s", output)
	}
	if err != nil {
		t.Fatalf("isolated PTY test: %v\n%s", err, output)
	}
}

func testBlockedRawPTYWrite(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "stty raw -echo; printf 'ready\\n'; exec /bin/sleep 5")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.cmd.Process.Kill(); _ = s.pty.Close() }()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	raw, err := s.pty.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fillErr error
	if err := raw.Control(func(fd uintptr) {
		flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if err != nil {
			fillErr = err
			return
		}
		if err := unix.SetNonblock(int(fd), true); err != nil {
			fillErr = err
			return
		}
		defer func() {
			_, err := unix.FcntlInt(fd, unix.F_SETFL, flags)
			fillErr = errors.Join(fillErr, err)
		}()
		data := []byte(strings.Repeat("x", 4096))
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_, err := unix.Write(int(fd), data)
			if errors.Is(err, unix.EAGAIN) {
				return
			}
			if err != nil {
				fillErr = err
				return
			}
		}
		fillErr = errors.New("raw PTY input did not reach EAGAIN")
	}); err != nil {
		t.Fatal(err)
	}
	if fillErr != nil {
		t.Fatal(fillErr)
	}
	written := make(chan error, 1)
	go func() { _, err := s.Writer().Write([]byte("x")); written <- err }()
	deadline := time.Now().Add(time.Second)
	active := false
	for time.Now().Before(deadline) {
		s.writer.mu.Lock()
		active = s.writer.writing
		s.writer.mu.Unlock()
		if active {
			break
		}
		select {
		case err := <-written:
			t.Fatalf("write completed before becoming active: %v", err)
		default:
		}
		runtime.Gosched()
	}
	if !active {
		t.Fatal("did not observe an active write")
	}
	select {
	case err := <-written:
		t.Fatalf("write completed while the input queue was full: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not return with a blocked PTY write")
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("write did not return after close")
	}
}
