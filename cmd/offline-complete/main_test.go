package main

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"offline-complete/internal/session"
)

func TestChildSignalExitCodeIsForwarded(t *testing.T) {
	code, err := run([]string{"/bin/sh", "-c", "kill -TERM $$"}, bytes.NewReader(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if code != 143 {
		t.Fatalf("exit code = %d, want 143", code)
	}
}

func TestResizeReadFailureStopsChildAndIsReported(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	s, err := session.Spawn("/bin/sleep", "5")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	signals := make(chan os.Signal, 1)
	failures := make(chan error, 1)
	stop := startResize(s, slave, signals, failures)
	if err := slave.Close(); err != nil {
		t.Fatal(err)
	}
	signals <- syscall.SIGWINCH
	select {
	case err := <-failures:
		if err == nil || !strings.Contains(err.Error(), "terminal size") {
			t.Fatalf("resize notification = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resize failure was not notified")
	}
	done := make(chan error, 1)
	go func() { _, err := s.Wait(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resize failure left child running")
	}
	if err := stop(); err != nil && strings.Contains(err.Error(), "read terminal size") {
		t.Fatalf("resize failure reported twice: %v", err)
	}
}

func TestResizeFailureRemainsAvailableAfterWorkerStops(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	s, err := session.Spawn("/bin/sleep", "5")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	signals := make(chan os.Signal, 1)
	failures := make(chan error, 1)
	stop := startResize(s, slave, signals, failures)
	if err := slave.Close(); err != nil {
		t.Fatal(err)
	}
	signals <- syscall.SIGWINCH
	// Wait for the worker to publish before stopping it, without consuming
	// the notification that run's cleanup must collect.
	deadline := time.Now().Add(2 * time.Second)
	for len(failures) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(failures) == 0 {
		t.Fatal("resize failure was not published")
	}
	_ = stop()
	select {
	case err := <-failures:
		if err == nil || !strings.Contains(err.Error(), "read terminal size") {
			t.Fatalf("resize failure = %v", err)
		}
	default:
		t.Fatal("resize failure lost during cleanup")
	}
}

func TestResizeWriteFailureStopsChildAndIsReported(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	s, err := session.Spawn("/bin/sh", "-c", "trap '' HUP; echo ready; exec /bin/sleep 5")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if line, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child readiness: %q %v", line, err)
	}
	if err := s.Reader().(*os.File).Close(); err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	stop := startResize(s, slave, signals)
	signals <- syscall.SIGWINCH
	done := make(chan error, 1)
	go func() { _, err := s.Wait(); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resize failure left child running")
	}
	if err := stop(); err == nil || !strings.Contains(err.Error(), "resize terminal") {
		t.Fatalf("resize error = %v", err)
	}
}

func TestWindowResizeIsForwarded(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	output, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer writer.Close()
	_ = output.SetReadDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", `trap 'stty size; exit' WINCH; echo ready; while :; do sleep 0.1; done`}, slave, writer)
		done <- err
	}()
	r := bufio.NewReader(output)
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("ready: %q %v", line, err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Rows: 41, Cols: 121}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	line, err = r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "41 121" {
		t.Fatalf("resized: %q %v", line, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResizeBetweenInitialReadAndSpawnIsNotLost(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	observed := make(chan os.Signal, 1)
	signal.Notify(observed, syscall.SIGWINCH)
	defer signal.Stop(observed)
	original := initialTerminalSize
	defer func() { initialTerminalSize = original }()
	initialTerminalSize = func(file *os.File) (*pty.Winsize, error) {
		size, err := pty.GetsizeFull(file)
		if err != nil {
			return nil, err
		}
		if err := pty.Setsize(master, &pty.Winsize{Rows: 41, Cols: 121}); err != nil {
			return nil, err
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
			return nil, err
		}
		select {
		case <-observed:
		case <-time.After(time.Second):
			return nil, errors.New("SIGWINCH was not delivered")
		}
		return size, nil
	}
	var output bytes.Buffer
	code, err := run([]string{"/bin/sh", "-c", `i=0; while [ "$i" -lt 30 ]; do s=$(stty size); if [ "$s" = "41 121" ]; then echo "$s"; exit 0; fi; i=$((i+1)); sleep 0.05; done; echo "$s"; exit 1`}, slave, &output)
	if err != nil || code != 0 || strings.TrimSpace(output.String()) != "41 121" {
		t.Fatalf("queued resize: code %d, output %q, error %v", code, output.String(), err)
	}
}

func TestInitialTerminalSizeIsInherited(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 37, Cols: 113}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code, err := run([]string{"/bin/stty", "size"}, slave, &output)
	if err != nil || code != 0 {
		t.Fatalf("run: code %d, error %v", code, err)
	}
	if strings.TrimSpace(output.String()) != "37 113" {
		t.Fatalf("initial size = %q, want 37 113", output.String())
	}
}

func TestExplicitlyRunsProgramAndReturnsItsExitCode(t *testing.T) {
	var output bytes.Buffer
	code, err := run([]string{"/bin/sh", "-c", "printf 'cli-output'; exit 7"}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	if !strings.Contains(output.String(), "cli-output") {
		t.Fatalf("output = %q", output.String())
	}
}

var simultaneousInputError = errors.New("simultaneous input error")
var simultaneousOutputError = errors.New("simultaneous output error")

type coordinatedInput struct{ ready <-chan struct{} }

func (r coordinatedInput) Read([]byte) (int, error) {
	<-r.ready
	return 0, simultaneousInputError
}

type coordinatedOutput struct{ ready chan struct{} }

func (w coordinatedOutput) Write([]byte) (int, error) {
	close(w.ready)
	time.Sleep(200 * time.Millisecond)
	return 0, simultaneousOutputError
}

func TestConcurrentInputAndOutputFailuresAreReported(t *testing.T) {
	ready := make(chan struct{})
	_, err := run([]string{"/bin/sh", "-c", "printf ready; exec /bin/sleep 5"}, coordinatedInput{ready}, coordinatedOutput{ready})
	if !errors.Is(err, simultaneousInputError) || !errors.Is(err, simultaneousOutputError) {
		t.Fatalf("run error = %v, want both input and output failures", err)
	}
}

type failingInput struct{}

func (failingInput) Read([]byte) (int, error) { return 0, errors.New("input failed") }

func TestInputFailureStopsChildAndReturnsError(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sleep", "5"}, failingInput{}, &bytes.Buffer{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "input failed") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input failure left proxy waiting for child")
	}
}

type failingOutput struct{}

func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestOutputFailureStopsChildAndReturnsError(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "printf ready; exec /bin/sleep 5"}, strings.NewReader(""), failingOutput{})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "output failed") {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("output failure left proxy waiting for child")
	}
}

func TestReportsMissingProgramAndStartupFailure(t *testing.T) {
	code, err := run(nil, strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Fatal("run without arguments succeeded")
	}
	if code == 0 {
		t.Fatal("missing arguments reported success")
	}

	code, err = run([]string{"/this/path/does/not/exist/offline-complete"}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil {
		t.Fatal("run with a missing program succeeded")
	}
	if code == 0 {
		t.Fatal("missing program reported success")
	}
	if !strings.Contains(err.Error(), "failed to start program") {
		t.Fatalf("error = %v", err)
	}
}
