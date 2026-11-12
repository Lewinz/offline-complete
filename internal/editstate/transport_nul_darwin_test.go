//go:build darwin

package editstate

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
)

func TestZLETransportCapturesClearsAndCancels(t *testing.T) {
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

	state := &State{}
	consumeResult := make(chan error, 1)
	go func() { consumeResult <- ConsumeNUL(eventR, state) }()

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
				outputMu.Lock()
				terminal := output.String()
				outputMu.Unlock()
				t.Fatalf("waiting for %q, terminal %q", want, terminal)
			}
		}
	}
	write := func(value string) {
		t.Helper()
		if _, err := io.WriteString(master, value); err != nil {
			t.Fatal(err)
		}
	}

	scriptPath := filepath.Join(t.TempDir(), "hooks.zsh")
	script := ZLETransportScript() + "\nprint -r -- hooks-ready\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	write("source " + scriptPath + "\r")
	waitOutput("hooks-ready")
	waitOutput("READY> ")

	write("git co")
	waitEditingSnapshot(t, state, "git co", 6)

	write("\x1b[D\x1b[D")
	waitEditingSnapshot(t, state, "git co", 4)

	write("\r")
	waitEditingCleared(t, state)

	cancelled := filepath.Join(t.TempDir(), "must-not-run")
	cancelLine := "touch " + cancelled
	write(cancelLine)
	waitEditingSnapshot(t, state, cancelLine, len(cancelLine))
	write("\x03")
	waitEditingCleared(t, state)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(cancelled); !os.IsNotExist(err) {
		t.Fatalf("cancelled command executed: %v", err)
	}

	executed := filepath.Join(t.TempDir(), "after-cancel")
	executeLine := "touch " + executed
	write(executeLine + "\r")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(executed); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("executed command did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitEditingCleared(t, state)

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
