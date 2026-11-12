//go:build darwin

package editstate

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestCommandStatusInteractive(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	srcR, srcW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer srcR.Close()
	defer srcW.Close()
	cmd := exec.Command(zsh, "-f", "-i")
	cmd.Env = append(os.Environ(), "TERM=dumb", "PROMPT=READY> ", "RPROMPT=")
	cmd.ExtraFiles = []*os.File{w, srcR}
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	defer master.Close()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	go io.Copy(io.Discard, master)
	events := make(chan string, 20)
	go func() {
		s := bufio.NewScanner(r)
		for s.Scan() {
			events <- s.Text()
		}
		close(events)
	}()
	wait := func(want string) {
		t.Helper()
		select {
		case got, ok := <-events:
			if !ok || got != want {
				t.Fatalf("event = %q (open=%v), want %q", got, ok, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for %q", want)
		}
	}
	script := CommandStatusHooks + `
function _offline_complete_command_status { print -r -- "status:$1:$#" >&3 }
autoload -Uz add-zsh-hook
function existing_precmd { true }
add-zsh-hook precmd existing_precmd
_offline_complete_status_enable || exit 12
print -r -- enabled >&3
`
	go func() { _, _ = io.WriteString(srcW, script); _ = srcW.Close() }()
	if _, err := io.WriteString(master, "source /dev/fd/4\r"); err != nil {
		t.Fatal(err)
	}
	wait("enabled")
	if _, err := io.WriteString(master, "false\r"); err != nil {
		t.Fatal(err)
	}
	wait("status:1:1")
	if _, err := io.WriteString(master, "true\r"); err != nil {
		t.Fatal(err)
	}
	wait("status:0:1")
	if _, err := io.WriteString(master, "_offline_complete_status_disable\r"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(master, "exit\r"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	for event := range events {
		t.Fatalf("unexpected status after disable: %q", event)
	}
}
