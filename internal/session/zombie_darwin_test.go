//go:build darwin

package session

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestExitedGroupSignalCharacterization records the Darwin result for a session
// leader that has exited but has not yet been reaped, without any PTY involved.
func TestExitedGroupSignalCharacterization(t *testing.T) {
	cmd := exec.Command("/usr/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := syscall.Getpgid(cmd.Process.Pid)
		if err == syscall.ESRCH {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	t.Logf("signal to exited, unreaped process group: %v", err)
}
