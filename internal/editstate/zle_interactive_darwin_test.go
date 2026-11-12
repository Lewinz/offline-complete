//go:build darwin

package editstate

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestZLEInteractiveExecutionAndCancellation(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh unavailable")
	}
	eventR, eventW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer eventR.Close()
	sourceR, sourceW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer sourceR.Close()
	defer sourceW.Close()
	cmd := exec.Command(zsh, "-f", "-i")
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8", "TERM=dumb", "PROMPT=READY> ", "RPROMPT=")
	cmd.ExtraFiles = []*os.File{eventW, sourceR}
	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	eventW.Close()
	defer master.Close()
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
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
	events := make(chan string, 256)
	go func() {
		s := bufio.NewScanner(eventR)
		for s.Scan() {
			events <- s.Text()
		}
		close(events)
	}()
	wait := func(want string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		var seen []string
		for {
			select {
			case v, ok := <-events:
				if !ok {
					t.Fatalf("event stream closed waiting for %q: %v", want, seen)
				}
				if v == "must-not-run" {
					t.Fatal("cancelled command executed")
				}
				seen = append(seen, v)
				if v == want {
					return
				}
			case <-timer.C:
				outputMu.Lock()
				terminal := output.String()
				outputMu.Unlock()
				pgid, pgerr := syscall.Getpgid(cmd.Process.Pid)
				fg, fgerr := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPGRP)
				term, terr := unix.IoctlGetTermios(int(master.Fd()), unix.TIOCGETA)
				t.Fatalf("waiting for %q, got %v; terminal %q; pid=%d pgid=%d err=%v fg=%d %v term=%+v %v", want, seen, terminal, cmd.Process.Pid, pgid, pgerr, fg, fgerr, term, terr)
			}
		}
	}
	script := ZLEHooks + `
function _offline_complete_zle_event { print -r -- "$1|$2|$3|$4" >&3 }
_offline_complete_zle_enable || exit 12
print -r -- enabled >&3
`
	go func() { _, _ = io.WriteString(sourceW, script); _ = sourceW.Close() }()
	if _, err := io.WriteString(master, "source /dev/fd/4\r"); err != nil {
		t.Fatal(err)
	}
	wait("enabled")
	wait("begin|||1")
	if _, err := io.WriteString(master, "print -r -- executed >&3"); err != nil {
		t.Fatal(err)
	}
	wait("update|print -r -- executed >&3|24|1")
	if _, err := io.WriteString(master, "\r"); err != nil {
		t.Fatal(err)
	}
	wait("end|||1")
	wait("executed")
	wait("begin|||1")
	if _, err := io.WriteString(master, "print -r -- must-not-run >&3"); err != nil {
		t.Fatal(err)
	}
	wait(fmt.Sprintf("update|%s|%d|1", "print -r -- must-not-run >&3", len("print -r -- must-not-run >&3")))
	// The update callback reports before ZLE redraws. Interrupting at that
	// instant can arrive while the callback is still running; wait for the
	// terminal to display the edited line before simulating Ctrl-C.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		outputMu.Lock()
		redrawn := strings.Contains(output.String(), "print -r -- must-not-run >&3")
		outputMu.Unlock()
		if redrawn {
			break
		}
		select {
		case <-outputChanged:
		case <-deadline.C:
			t.Fatal("cancelled line was not drawn")
		}
	}
	if _, err := io.WriteString(master, "\x03"); err != nil {
		t.Fatal(err)
	}
	// On this zsh, interrupt may skip line-finish; line-init still starts the
	// next cycle. A consumer must invalidate the previous snapshot at begin.
	wait("begin|||1")
	if _, err := io.WriteString(master, "print -r -- after-cancel >&3\r"); err != nil {
		t.Fatal(err)
	}
	wait("end|||1")
	wait("after-cancel")
	if _, err := io.WriteString(master, "exit\r"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("zsh: %v", err)
	}
	for event := range events {
		if strings.Contains(event, "must-not-run") && !strings.HasPrefix(event, "update|") {
			t.Fatalf("cancelled command executed: %q", event)
		}
	}
}
