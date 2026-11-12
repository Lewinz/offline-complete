//go:build darwin

package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSignalAfterChildClosesTerminal(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_CLOSED_TERMINAL_HELPER") == "1" {
		code, err := run([]string{"/bin/sh", "-c", "echo $$; exec 0<&- 1>&- 2>&-; exec /bin/sleep 10"}, os.Stdin, os.Stdout)
		if err != nil {
			os.Exit(1)
		}
		os.Exit(code)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalAfterChildClosesTerminal$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_CLOSED_TERMINAL_HELPER=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 1 {
		t.Fatalf("invalid child PID %q: %v", line, err)
	}
	needsCleanup := true
	defer func() {
		if needsCleanup {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	// Allow the child to close all three PTY descriptors before signaling.
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 143 {
			t.Fatalf("exit = %v, want 143", err)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("child still exists: %v", err)
		}
		needsCleanup = false
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not exit")
	}
}
