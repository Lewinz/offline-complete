//go:build darwin

package main

import (
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

type stalledOutput struct{ release *os.File }

func (w stalledOutput) Write(p []byte) (int, error) {
	_, _ = os.Stdout.Write([]byte("ready"))
	var b [1]byte
	_, _ = w.release.Read(b[:])
	return 0, errors.New("released output")
}

func TestSignalRestoresTerminalWithStalledOutput(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_STALLED_OUTPUT_HELPER") == "1" {
		release := os.NewFile(3, "release")
		_, err := run([]string{"/bin/sh", "-c", "printf x; exec /bin/sleep 10"}, os.Stdin, stalledOutput{release})
		if err == nil || !strings.Contains(err.Error(), "output worker") {
			_, _ = fmt.Fprintf(os.Stderr, "missing stalled output error: %v", err)
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
	readRelease, writeRelease, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readRelease.Close()
	defer writeRelease.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalRestoresTerminalWithStalledOutput$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_STALLED_OUTPUT_HELPER=1")
	cmd.ExtraFiles = []*os.File{readRelease}
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
		t.Fatalf("output marker %q: %v", marker, err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper: %v; stderr: %s", err, stderr.String())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("stalled output prevented proxy cleanup")
	}
	if got := terminalFlags(t, slave); got != original {
		t.Fatal("terminal was not restored after stalled output")
	}
}
