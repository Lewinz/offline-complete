//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

type stalledInputWriter struct {
	io.WriteCloser
	entered chan struct{}
	release chan struct{}
}

func (w *stalledInputWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return 0, os.ErrClosed
}

// The child and caller terminals are real; the PTY write is deliberately
// stalled so the test can observe the input worker entering it before SIGTERM.
func TestSignalRestoresTerminalWithStalledInputWorker(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_INPUT_STALL_HELPER") == "1" {
		entered := make(chan struct{})
		release := make(chan struct{})
		go func() { <-entered; _, _ = io.WriteString(os.Stdout, "blocked") }()
		_, err := runWithInputWriter([]string{"/bin/sh", "-c", "printf ready; exec /bin/sleep 10"}, os.Stdin, os.Stdout, func(w io.WriteCloser) io.WriteCloser {
			return &stalledInputWriter{WriteCloser: w, entered: entered, release: release}
		})
		close(release)
		if err == nil || !strings.Contains(err.Error(), "input worker did not stop") {
			_, _ = fmt.Fprintf(os.Stderr, "missing stalled input error: %v", err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	original := terminalFlags(t, slave)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalRestoresTerminalWithStalledInputWorker$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_INPUT_STALL_HELPER=1")
	cmd.Stdin = slave
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	read := func(want string) {
		t.Helper()
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(out, buf); err != nil || string(buf) != want {
			t.Fatalf("marker %q: %q, %v", want, buf, err)
		}
	}
	read("ready")
	if _, err := master.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	read("blocked")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy: %v; stderr: %s", err, stderr.String())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("stalled input worker prevented terminal restoration")
	}
	if got := terminalFlags(t, slave); got != original {
		t.Fatal("terminal was not restored")
	}
}
