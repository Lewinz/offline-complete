//go:build darwin

package session

import (
	"bufio"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The poller requires a nonblocking descriptor even after a resize.
func TestPTYDescriptorNonblockingModeAfterResize(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "exec /bin/sleep 10")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.cmd.Process.Kill(); _, _ = s.Wait(); _ = s.pty.Close() }()
	check := func(stage string) {
		t.Helper()
		raw, err := s.pty.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var flags int
		var flagErr error
		if err := raw.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
			t.Fatal(err)
		}
		if flagErr != nil {
			t.Fatal(flagErr)
		}
		if flags&unix.O_NONBLOCK == 0 {
			t.Fatalf("%s: descriptor is blocking (flags %#x)", stage, flags)
		}
	}
	check("spawn")
	if err := s.Resize(30, 90); err != nil {
		t.Fatal(err)
	}
	check("resize")
}

func TestClosingPTYInterruptsPendingReadAfterResize(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "printf 'ready\\n'; exec /bin/sleep 10")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.cmd.Process.Kill(); _, _ = s.Wait(); _ = s.pty.Close() }()
	reader := bufio.NewReader(s.Reader())
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(30, 90); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := reader.ReadByte(); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("read returned before close: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := s.pty.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read returned data after close")
		}
	case <-time.After(time.Second):
		t.Fatal("PTY close did not interrupt pending read after resize")
	}
}

func TestClosingPTYInterruptsPendingRead(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "printf 'ready\\n'; exec /bin/sleep 10")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.cmd.Process.Kill(); _, _ = s.Wait(); _ = s.pty.Close() }()
	reader := bufio.NewReader(s.Reader())
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := reader.ReadByte(); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("read returned before close: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := s.pty.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("read returned data after close")
		}
	case <-time.After(time.Second):
		t.Fatal("PTY close did not interrupt pending read")
	}
}
