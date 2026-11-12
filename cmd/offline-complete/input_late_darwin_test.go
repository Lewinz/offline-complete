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

type releasedInputWriter struct {
	io.WriteCloser
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (w *releasedInputWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(p), nil
}

func (w *releasedInputWriter) Close() error {
	err := w.WriteCloser.Close()
	close(w.closed)
	return err
}

// Removing both stop checks before the read lets the released worker consume
// the later line before the caller can read it.
func TestReleasedInputWorkerPreservesLaterTerminalInput(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_LATE_INPUT_HELPER") == "1" {
		entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() { <-entered; _, _ = io.WriteString(os.Stdout, "blocked") }()
		_, err := runWithInputWriter([]string{"/bin/sh", "-c", "printf ready; exec /bin/sleep 10"}, os.Stdin, os.Stdout, func(w io.WriteCloser) io.WriteCloser {
			return &releasedInputWriter{WriteCloser: w, entered: entered, release: release, closed: closed}
		})
		if err == nil || !strings.Contains(err.Error(), "input worker did not stop") {
			_, _ = fmt.Fprintf(os.Stderr, "missing stalled input error: %v", err)
			os.Exit(2)
		}
		_, _ = io.WriteString(os.Stdout, "returned")
		control := os.NewFile(3, "control")
		var b [1]byte
		if _, err := io.ReadFull(control, b[:]); err != nil {
			os.Exit(3)
		}
		close(release)
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			_, _ = io.WriteString(os.Stderr, "input worker did not finish copy")
			os.Exit(4)
		}
		var line [6]byte
		if _, err := io.ReadFull(os.Stdin, line[:]); err != nil || string(line[:]) != "later\n" {
			_, _ = fmt.Fprintf(os.Stderr, "later input: %q, %v", line, err)
			os.Exit(5)
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
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlRead.Close()
	defer controlWrite.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReleasedInputWorkerPreservesLaterTerminalInput$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_LATE_INPUT_HELPER=1")
	cmd.Stdin = slave
	cmd.ExtraFiles = []*os.File{controlRead}
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
	read("returned")
	if got := terminalFlags(t, slave); got != original {
		t.Fatal("terminal was not restored")
	}
	if _, err := master.Write([]byte("later\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := controlWrite.Write([]byte("r")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("proxy: %v; stderr: %s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("released input worker consumed or blocked later terminal input")
	}
}
