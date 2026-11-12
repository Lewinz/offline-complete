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
	"unsafe"

	"github.com/creack/pty"
)

func terminalFlags(t *testing.T, f *os.File) syscall.Termios {
	t.Helper()
	var state syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&state)))
	if errno != 0 {
		t.Fatal(errno)
	}
	return state
}

func TestRestoreDoesNotOverwriteLaterTerminalChanges(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()

	restore, err := makeRaw(slave)
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	later := terminalFlags(t, slave)
	later.Lflag ^= syscall.ECHO
	if err := terminalState(slave, syscall.TIOCSETA, &later); err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if got := terminalFlags(t, slave); got != later {
		t.Fatal("repeated restore overwrote later terminal settings")
	}
}

func TestRestoreContinuesReportingFailure(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	restore, err := makeRaw(slave)
	if err != nil {
		slave.Close()
		t.Fatal(err)
	}
	if err := slave.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := restore(); err == nil {
			t.Fatalf("restore attempt %d unexpectedly succeeded", i+1)
		}
	}
}

func TestExitedProxyDoesNotConsumeLaterTerminalInput(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	var output strings.Builder
	if code, err := run([]string{"/bin/sh", "-c", "sleep 0.2"}, slave, &output); err != nil || code != 0 {
		t.Fatalf("run: %d %v", code, err)
	}
	if _, err := master.Write([]byte("after-exit\n")); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(slave).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "after-exit\n" {
			t.Fatalf("input = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy consumed input after it returned")
	}
}

func TestExitedProxyDoesNotConsumeLaterPipeInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	var output strings.Builder
	if code, err := run([]string{"/bin/sh", "-c", "sleep 0.2"}, reader, &output); err != nil || code != 0 {
		t.Fatalf("run: %d %v", code, err)
	}
	if _, err := writer.Write([]byte("after-exit\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "after-exit\n" {
			t.Fatalf("input = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy consumed pipe input after it returned")
	}
}

func TestExitedProxyDoesNotConsumeLaterSocketInput(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(fds[0]), "socket-reader")
	writer := os.NewFile(uintptr(fds[1]), "socket-writer")
	defer reader.Close()
	defer writer.Close()
	var output strings.Builder
	if code, err := run([]string{"/bin/sh", "-c", "sleep 0.2"}, reader, &output); err != nil || code != 0 {
		t.Fatalf("run: %d %v", code, err)
	}
	if _, err := writer.Write([]byte("after-exit\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "after-exit\n" {
			t.Fatalf("input = %q", line)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy consumed socket input after it returned")
	}
}

func TestSocketInputIsForwarded(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(fds[0]), "socket-reader")
	writer := os.NewFile(uintptr(fds[1]), "socket-writer")
	defer reader.Close()
	defer writer.Close()
	if _, err := writer.Write([]byte("socket-data\n")); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	code, err := run([]string{"/bin/sh", "-c", "IFS= read -r line; printf 'received:%s' \"$line\""}, reader, &output)
	if err != nil || code != 0 || !strings.Contains(output.String(), "received:socket-data") {
		t.Fatalf("run: code=%d err=%v output=%q", code, err, output.String())
	}
}

func TestPipeInputIsForwarded(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Write([]byte("pipe-data\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	code, err := run([]string{"/bin/sh", "-c", "IFS= read -r line; printf 'received:%s' \"$line\""}, reader, &output)
	if err != nil || code != 0 || !strings.Contains(output.String(), "received:pipe-data") {
		t.Fatalf("run: code=%d err=%v output=%q", code, err, output.String())
	}
}

func TestSignalWithPipeInputStopsChild(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_PIPE_SIGNAL_HELPER") == "1" {
		code, _ := run([]string{"/bin/sh", "-c", "trap '' HUP; echo $$; exec /bin/sleep 10"}, os.Stdin, os.Stdout)
		os.Exit(code)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalWithPipeInputStopsChild$")
			cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_PIPE_SIGNAL_HELPER=1")
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
			line, err := bufio.NewReader(out).ReadString('\n')
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
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 128+int(sig) {
				t.Errorf("exit = %v, want %d", err, 128+int(sig))
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Errorf("child %d still exists: %v", pid, err)
			} else {
				needsCleanup = false
			}
		})
	}
}

func TestSignalRestoresCallerTerminal(t *testing.T) {
	if os.Getenv("OFFLINE_COMPLETE_SIGNAL_HELPER") == "1" {
		code, _ := run([]string{"/bin/sh", "-c", "echo ready; exec sleep 10"}, os.Stdin, os.Stdout)
		os.Exit(code)
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			before := terminalFlags(t, slave)
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalRestoresCallerTerminal$")
			cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_SIGNAL_HELPER=1")
			cmd.Stdin = slave
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
			line, err := bufio.NewReader(out).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "ready" {
				t.Fatalf("ready = %q, %v", line, err)
			}
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 128+int(sig) {
				t.Errorf("exit = %v, want %d", err, 128+int(sig))
			}
			if after := terminalFlags(t, slave); after != before {
				t.Errorf("terminal not restored after %s", sig)
			}
		})
	}
}

func TestTerminalForwardsUnicodeArrowsAndPaste(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	output, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer writer.Close()
	_ = output.SetReadDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "stty raw -echo; printf 'ready\\n'; dd bs=1 count=21 2>/dev/null | od -An -tx1"}, slave, writer)
		done <- err
	}()
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("ready: %q %v", line, err)
	}
	if _, err := master.Write([]byte("雪\x1b[A\x1b[200~abc\x1b[201~")); err != nil {
		t.Fatal(err)
	}
	var hex strings.Builder
	for hex.Len() < 42 {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatalf("output %q: %v", hex.String(), err)
		}
		hex.WriteString(strings.Join(strings.Fields(line), ""))
	}
	if got := hex.String(); got != "e99baa1b5b411b5b3230307e6162631b5b3230317e" {
		t.Errorf("forwarded bytes = %s", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTerminalForwardsCtrlCToChild(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	output, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer writer.Close()
	_ = output.SetReadDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "trap 'echo interrupted; exit 0' INT; echo ready; while :; do sleep 0.1; done"}, slave, writer)
		done <- err
	}()
	reader := bufio.NewReader(output)
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("ready: %q %v", line, err)
	}
	if _, err := master.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatalf("interrupted: %q %v", line, err)
		}
		if strings.Contains(line, "interrupted") {
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCallerTerminalRestoredOnOutputFailure(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	before := terminalFlags(t, slave)
	_, err = run([]string{"/bin/sh", "-c", "printf ready; exec sleep 5"}, slave, failingOutput{})
	if err == nil || !strings.Contains(err.Error(), "output failed") {
		t.Fatalf("error = %v", err)
	}
	if after := terminalFlags(t, slave); after != before {
		t.Errorf("terminal not restored: before %+v after %+v", before, after)
	}
}

func TestCallerTerminalRawModeAndRestore(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	before := terminalFlags(t, slave)
	size, err := pty.GetsizeFull(slave)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("initial PTY size: %+v", size)
	output, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	defer writer.Close()
	_ = output.SetReadDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() { _, err := run([]string{"/bin/sh", "-c", "echo ready; sleep 1"}, slave, writer); done <- err }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("ready %q: %v", line, err)
	}
	during := terminalFlags(t, slave)
	if during.Lflag&(syscall.ICANON|syscall.ECHO|syscall.ISIG) != 0 {
		t.Errorf("caller terminal not raw: lflag %#x", during.Lflag)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after := terminalFlags(t, slave)
	if after != before {
		t.Errorf("terminal state not restored: before %+v after %+v", before, after)
	}
}
