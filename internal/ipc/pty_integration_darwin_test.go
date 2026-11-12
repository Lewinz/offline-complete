//go:build darwin

package ipc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/session"
)

// A separate process is killed without running listener cleanup. A restarted
// listener must work without removing the abandoned socket.
func TestCrashRestartPreservesSocketAndAcceptsConnections(t *testing.T) {
	if base := os.Getenv("OFFLINE_COMPLETE_CRASH_TEST_BASE"); base != "" {
		listener, err := NewPrivateListener(base)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, listener.Path())
		// The parent kills this process; no cleanup handler runs.
		time.Sleep(time.Minute)
		os.Exit(2)
	}

	base := privateBase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashRestartPreservesSocketAndAcceptsConnections$")
	cmd.Env = append(os.Environ(), "OFFLINE_COMPLETE_CRASH_TEST_BASE="+base)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	pathCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		pathCh <- strings.TrimSpace(line)
	}()
	var oldPath string
	select {
	case oldPath = <-pathCh:
		if oldPath == "" {
			t.Fatal("child did not report a socket path")
		}
	case <-ctx.Done():
		t.Fatal("child did not create listener before timeout")
	}
	before, err := os.Lstat(oldPath)
	if err != nil || before.Mode()&os.ModeSocket == 0 {
		t.Fatalf("child socket unavailable: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child exited successfully")
	}
	fresh, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	}()
	serverCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(serverCtx, fresh.Listener(), "restart", time.Second, 1, func(_ context.Context, c *net.UnixConn) {
			_, _ = c.Write([]byte("ready"))
		})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	conn, err := DialAuthenticated(fresh.Path(), "restart", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var reply [5]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil || string(reply[:]) != "ready" {
		t.Fatalf("new listener response = %q, error = %v", reply, err)
	}
	after, err := os.Lstat(oldPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("abandoned socket changed or disappeared: %v", err)
	}
}

func TestStaleSocketIsPreservedOnRestart(t *testing.T) {
	base := privateBase(t)
	dir := filepath.Join(base, "s-stale")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "socket")
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("stale socket changed or disappeared: %v", err)
	}
}

// IPC is not yet wired into the production proxy. This checks the planned
// failure isolation when the existing components run together.
func TestIPCDisconnectDoesNotInterruptPTY(t *testing.T) {
	child, err := session.Spawn("/bin/sh", "-c", `stty -echo; echo ready; while IFS= read -r line; do printf 'received:%s\n' "$line"; done`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := child.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}()
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(child.Reader())
		for scanner.Scan() {
			lines <- strings.TrimSpace(scanner.Text())
		}
		close(lines)
	}()
	readLine := func(want string) {
		t.Helper()
		select {
		case got := <-lines:
			if got != want {
				t.Fatalf("PTY output = %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for PTY output %q", want)
		}
	}
	readLine("ready")

	listener, err := NewPrivateListener(privateBase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- ServeAuthenticated(ctx, listener.Listener(), "session", time.Second, 4, func(_ context.Context, c *net.UnixConn) {
			_, _ = io.Copy(io.Discard, c)
		})
	}()
	stopped := false
	defer func() {
		cancel()
		if !stopped {
			if err := <-done; err != nil {
				t.Errorf("serve: %v", err)
			}
		}
	}()
	conn, err := DialAuthenticated(listener.Path(), "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(child.Writer(), "after-disconnect"); err != nil {
		t.Fatal(err)
	}
	readLine("received:after-disconnect")

	if bad, err := DialAuthenticated(listener.Path(), "wrong-session", time.Second); err == nil {
		bad.Close()
		t.Fatal("wrong session accepted")
	}
	if _, err := fmt.Fprintln(child.Writer(), "after-rejection"); err != nil {
		t.Fatal(err)
	}
	readLine("received:after-rejection")

	conn, err = DialAuthenticated(listener.Path(), "session", time.Second)
	if err != nil {
		t.Fatalf("reconnect after disconnect: %v", err)
	}
	conn.Close()
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IPC server did not stop")
	}
	if _, err := fmt.Fprintln(child.Writer(), "after-shutdown"); err != nil {
		t.Fatal(err)
	}
	readLine("received:after-shutdown")
}
