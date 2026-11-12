//go:build darwin

package editstate

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestZLEDisableBeforeEnableIsHarmless(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	script := ZLEHooks + `
setopt errexit
_offline_complete_zle_disable
print -r -- survived
`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "survived" {
		t.Fatalf("disable before enable interrupted shell: %q, %v", out, err)
	}
}

func TestZLEDisableEndsActiveEditing(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	script := ZLEHooks + `
setopt errexit
function _offline_complete_zle_event { print -r -- "$1" }
_offline_complete_zle_enable
_offline_complete_zle_begin
_offline_complete_zle_disable
_offline_complete_zle_disable
`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil || string(out) != "begin\nend\n" {
		t.Fatalf("disable did not end active editing once: %q, %v", out, err)
	}
}

func TestZLEIgnoresEventsOutsideActiveCycle(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	script := ZLEHooks + `
function _offline_complete_zle_event { print -r -- "$1" }
_offline_complete_zle_update
_offline_complete_zle_end
_offline_complete_zle_begin
_offline_complete_zle_update
_offline_complete_zle_end
_offline_complete_zle_update
_offline_complete_zle_end
`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil || string(out) != "begin\nupdate\nend\n" {
		t.Fatalf("unexpected events outside editing cycle: %q, %v", out, err)
	}
}

func TestZLEUpdateDoesNotTraceBuffer(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	script := ZLEHooks + `
function _offline_complete_zle_event { [[ $1 != update || $2 == private_test_buffer_842 ]] || exit 7 }
_offline_complete_zle_begin
BUFFER=private_test_buffer_842
CURSOR=23
setopt xtrace
_offline_complete_zle_update
[[ -o xtrace ]] || exit 8
unsetopt xtrace
print -r -- survived
`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil || strings.Contains(string(out), "private_test_buffer_842") || !strings.Contains(string(out), "survived") {
		t.Fatalf("update leaked buffer or changed tracing: %q, %v", out, err)
	}
}

func TestZLECallbackFailureDoesNotExitShell(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	script := ZLEHooks + `
setopt errexit
function _offline_complete_zle_event { return 1 }
_offline_complete_zle_begin
_offline_complete_zle_update
_offline_complete_zle_end
print -r -- survived
`
	out, err := exec.Command(zsh, "-f", "-c", script).CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "survived" {
		t.Fatalf("callback failure interrupted shell: %q, %v", out, err)
	}
}

func TestZLEHooksCaptureAndFinish(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	script := ZLEHooks + `
function _offline_complete_zle_event { print -r -- "$1|$2|$3|$4" >&3 }
_offline_complete_zle_enable || exit 12
setopt multibyte
value=''
vared -p 'READY>' value
_offline_complete_zle_disable
print -r -- disabled >&3
vared -p 'SECOND>' value
`
	cmd := exec.Command(zsh, "-f", "-i", "-c", script)
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8", "TERM=dumb")
	cmd.ExtraFiles = []*os.File{w}
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
	output := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); output <- data }()
	events := make(chan string, 100)
	go func() {
		s := bufio.NewScanner(r)
		for s.Scan() {
			events <- s.Text()
		}
		close(events)
	}()
	wait := func(want string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		var got []string
		for {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatalf("events ended waiting for %q: %v; terminal: %q", want, got, <-output)
				}
				got = append(got, event)
				if event == want {
					return
				}
			case <-timer.C:
				t.Fatalf("waiting for %q, got %v", want, got)
			}
		}
	}
	wait("begin|||1")
	if _, err := io.WriteString(master, "aé"); err != nil {
		t.Fatal(err)
	}
	wait("update|aé|2|1")
	if _, err := io.WriteString(master, "\x01"); err != nil {
		t.Fatal(err)
	}
	wait("update|aé|0|1")
	if _, err := io.WriteString(master, "\r"); err != nil {
		t.Fatal(err)
	}
	wait("end|||1")
	wait("disabled")
	if _, err := io.WriteString(master, "\r"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("zsh failed: %v", err)
	}
	for event := range events {
		t.Fatalf("event after disable: %q", event)
	}
}
