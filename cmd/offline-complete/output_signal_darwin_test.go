//go:build darwin

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type heldFailingOutput struct {
	release *os.File
}

func (w heldFailingOutput) Write(p []byte) (int, error) {
	_, _ = os.Stdout.Write(p)
	var b [1]byte
	_, _ = w.release.Read(b[:])
	return 0, os.ErrClosed
}

// A signal-driven PTY close must not hide an independent caller output error.
func TestSignalDoesNotHideRealOutputWriteFailure(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_OUTPUT_SIGNAL_HELPER") == "1" {
		release := os.NewFile(3, "release")
		_, err := run([]string{"/bin/sh", "-c", "echo $$; exec /bin/sleep 10"}, os.Stdin, heldFailingOutput{release})
		if !errors.Is(err, os.ErrClosed) {
			_, _ = fmt.Fprintf(os.Stderr, "missing output error: %v", err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	readRelease, writeRelease, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readRelease.Close()
	defer writeRelease.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalDoesNotHideRealOutputWriteFailure$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_OUTPUT_SIGNAL_HELPER=1")
	cmd.ExtraFiles = []*os.File{readRelease}
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
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 1 {
		t.Fatalf("child pid = %q: %v", line, err)
	}
	needsCleanup := true
	defer func() {
		if needsCleanup {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			needsCleanup = false
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child was not reaped after signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := io.WriteString(writeRelease, "x"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper: %v; stderr: %s", err, stderr.String())
	}
}
