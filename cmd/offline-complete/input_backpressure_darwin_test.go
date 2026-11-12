//go:build darwin

package main

import (
	"bytes"
	"errors"
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

// This exercises the complete proxy with real terminal input and a child that
// stops reading its raw PTY. It does not assume a particular PTY queue size.
func TestSignalRestoresTerminalWithInputBackpressure(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_INPUT_PRESSURE_HELPER") == "1" {
		code, err := run([]string{"/bin/sh", "-c", "/bin/stty raw -echo; printf ready; exec /bin/sleep 10"}, os.Stdin, os.Stdout)
		// A write interrupted by PTY shutdown can report EIO. It must remain
		// visible rather than being silently converted into a signal exit.
		if (err != nil && !errors.Is(err, syscall.EIO)) || (err == nil && code != 128+int(syscall.SIGTERM)) {
			_, _ = fmt.Fprintf(os.Stderr, "code %d: %v", code, err)
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalRestoresTerminalWithInputBackpressure$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_INPUT_PRESSURE_HELPER=1")
	cmd.Stdin = slave
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	marker := make([]byte, 5)
	if _, err := io.ReadFull(stdout, marker); err != nil || string(marker) != "ready" {
		t.Fatalf("child readiness %q: %v", marker, err)
	}
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = master.Write(bytes.Repeat([]byte("x"), 16<<20))
	}()
	select {
	case <-writeDone:
		t.Fatal("terminal input completed before backpressure")
	case <-time.After(200 * time.Millisecond):
	}
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
		t.Fatal("input backpressure prevented proxy cleanup")
	}
	if got := terminalFlags(t, slave); got != original {
		t.Fatal("terminal was not restored after input backpressure")
	}
}
