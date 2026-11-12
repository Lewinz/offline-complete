//go:build darwin

package agent_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"offline-complete/internal/agent"
	"offline-complete/internal/editstate"
)

func TestWriteLineReplacementUpdatesRealZshBuffer(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}

	eventR, eventW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer eventR.Close()

	cmd := exec.Command(zsh, "-f", "-i")
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8", "TERM=dumb", "PROMPT=READY> ", "RPROMPT=")
	cmd.ExtraFiles = []*os.File{eventW}
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	eventW.Close()
	defer master.Close()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	state := &editstate.State{}
	consumeResult := make(chan error, 1)
	go func() { consumeResult <- editstate.ConsumeNUL(eventR, state) }()

	var output bytes.Buffer
	var outputMu sync.Mutex
	outputChanged := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			outputMu.Lock()
			output.Write(buf[:n])
			outputMu.Unlock()
			select {
			case outputChanged <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()

	scriptPath := filepath.Join(t.TempDir(), "hooks.zsh")
	if err := os.WriteFile(scriptPath, []byte(editstate.ZLETransportScript()+"\nprint -r -- hooks-ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write := func(value string) {
		t.Helper()
		if _, err := io.WriteString(master, value); err != nil {
			t.Fatal(err)
		}
	}
	waitOutput := func(want string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			outputMu.Lock()
			seen := strings.Contains(output.String(), want)
			outputMu.Unlock()
			if seen {
				return
			}
			select {
			case <-outputChanged:
			case <-timer.C:
				t.Fatalf("waiting for %q", want)
			}
		}
	}

	write("source " + scriptPath + "\r")
	waitOutput("hooks-ready")
	waitOutput("READY> ")

	write("git co")
	snapshot := waitForSnapshot(t, state, "git co", 6)

	completer := testCompleter()
	offer, ok := completer.Complete(snapshot)
	if !ok || len(offer.Candidates) != 1 || offer.Candidates[0].Name != "commit" {
		t.Fatalf("offer = %+v ok=%v", offer, ok)
	}
	edited, err := agent.Apply(snapshot, offer.Candidates[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.WriteLineReplacement(master, edited.Line, edited.Cursor); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, state, "git commit", 10)

	write(" --all")
	waitForSnapshot(t, state, "git commit --all", 16)
	if err := agent.WriteLineReplacement(master, "git commit --all", 10); err != nil {
		t.Fatal(err)
	}
	waitForSnapshot(t, state, "git commit --all", 10)

	write("\x03")
	write("exit\r")
	select {
	case err := <-consumeResult:
		if err != nil && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "closed pipe") {
			t.Fatalf("ConsumeNUL: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consume did not finish after shell exit")
	}
}

func waitForSnapshot(t *testing.T, state *editstate.State, line string, cursor int) editstate.Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, present := state.Snapshot()
		if present && snapshot.Line == line && snapshot.Cursor == cursor {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, present := state.Snapshot()
	t.Fatalf("snapshot = %+v present=%v, want %q cursor %d", snapshot, present, line, cursor)
	return editstate.Snapshot{}
}
