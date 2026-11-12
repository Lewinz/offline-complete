//go:build darwin

package session

import (
	"bufio"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestCloseWithFullRawPTYInputQueue uses an unread raw slave so that EAGAIN,
// rather than a large write or an elapsed delay, establishes backpressure.
func TestCloseWithFullRawPTYInputQueue(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "stty raw -echo; printf 'ready\\n'; exec /bin/sleep 30")
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
		data := make([]byte, 4096)
		for i := range data {
			data[i] = 'x'
		}
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
	select {
	case err := <-written:
		t.Fatalf("write returned before close despite full queue: %v", err)
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
		t.Fatal("close did not return with a full raw PTY input queue")
	}
	select {
	case <-written:
		// A completed write is not proof that the child consumed its bytes.
	case <-time.After(time.Second):
		t.Fatal("write did not return after close")
	}
}
