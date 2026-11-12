//go:build darwin

package session

import (
	"errors"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// TestPTYInputBackpressureCharacterization probes whether a raw, unread slave
// can provide a deterministic full-queue precondition for future close tests.
func TestPTYInputBackpressureCharacterization(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	state, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	state.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
	state.Iflag &^= unix.IXON | unix.ICRNL
	state.Cc[unix.VMIN] = 1
	state.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TIOCSETA, state); err != nil {
		t.Fatal(err)
	}
	fd := int(master.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4096)
	for i := range data {
		data[i] = 'x'
	}
	deadline := time.Now().Add(2 * time.Second)
	var total int
	full := false
	for total < 16<<20 && time.Now().Before(deadline) {
		n, err := unix.Write(fd, data)
		if n > 0 {
			total += n
		}
		if errors.Is(err, unix.EAGAIN) {
			t.Logf("unread raw slave applied backpressure after %d bytes", total)
			full = true
			break
		}
		if err != nil {
			t.Fatalf("write after %d bytes: %v", total, err)
		}
		if n == 0 {
			t.Fatal("write made no progress without EAGAIN")
		}
	}
	if !full {
		t.Fatalf("no input backpressure observed after %d bytes", total)
	}
	slaveFD := int(slave.Fd())
	if err := unix.SetNonblock(slaveFD, true); err != nil {
		t.Fatal(err)
	}
	received := 0
	for received < total && time.Now().Before(deadline) {
		n, err := unix.Read(slaveFD, data)
		if errors.Is(err, unix.EAGAIN) {
			time.Sleep(time.Millisecond)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range data[:n] {
			if b != 'x' {
				t.Fatalf("unexpected byte %#x after %d bytes", b, received)
			}
		}
		received += n
	}
	if received != total {
		t.Fatalf("slave received %d of %d accepted bytes", received, total)
	}
}
