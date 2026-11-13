//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/ipc"
)

func TestSwiftWindowCheckReceivesOffer(t *testing.T) {
	binary, err := filepath.Abs("../../bin/oc-window")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skip("bin/oc-window not built; run make bin")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(home, ".oc-window-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	base := filepath.Join(root, "b")
	if err := ipc.EnsurePrivateBase(base); err != nil {
		t.Fatal(err)
	}
	listener, err := ipc.NewPrivateListener(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	sessionID, err := ipc.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}

	served := make(chan error, 1)
	go func() {
		conn, err := ipc.AcceptAuthenticated(listener.Listener(), sessionID, 5*time.Second)
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		message := `{"type":"offer","version":3,"offer":1,"items":[{"name":"commit","description":"Record changes"}]}` + "\n"
		if _, err := conn.Write([]byte(message)); err != nil {
			served <- err
			return
		}
		time.Sleep(500 * time.Millisecond)
		served <- nil
	}()

	cmd := exec.Command(binary, "--check", "--socket", listener.Path(), "--session", sessionID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oc-window: %v, output %q", err, output)
	}
	if !strings.Contains(string(output), "commit") {
		t.Fatalf("oc-window output = %q", output)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
