package session

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTerminateDoesNotEscalateWhenChildAndGraceAreReady(t *testing.T) {
	for i := 0; i < 30; i++ {
		s, err := Spawn("/bin/sleep", "30")
		if err != nil {
			t.Fatal(err)
		}
		var escalated bool
		s.signalGroup = func(_ int, sig syscall.Signal) error {
			if sig == syscall.SIGKILL {
				escalated = true
				return nil
			}
			if err := s.cmd.Process.Kill(); err != nil {
				return err
			}
			<-s.done
			return nil
		}
		grace := make(chan time.Time, 1)
		grace <- time.Now()
		_, err = s.terminateWithGrace(grace)
		closeErr := s.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("termination: %v; close: %v", err, closeErr)
		}
		if escalated {
			t.Fatal("sent SIGKILL after the direct child was already reaped")
		}
	}
}

func TestReapedChildWinsReadyKillWaitDeadline(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "exit 7")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	<-s.done
	deadline := make(chan time.Time, 1)
	deadline <- time.Now()
	for i := 0; i < 100; i++ {
		status, err := s.waitAfterKillUntil(deadline)
		if err != nil || status.Code != 7 {
			t.Fatalf("reaped child: status = %d, error = %v", status.Code, err)
		}
		// Refill only if the deadline branch consumed the notification.
		select {
		case deadline <- time.Now():
		default:
		}
	}
}

func TestTerminateReapsChildWhenGroupDisappears(t *testing.T) {
	for _, missingOn := range []syscall.Signal{syscall.SIGHUP, syscall.SIGKILL} {
		t.Run(missingOn.String(), func(t *testing.T) {
			s, err := Spawn("/bin/sh", "-c", "trap '' HUP; printf 'ready\\n'; exec /bin/sleep 30")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			s.signalGroup = func(pid int, sig syscall.Signal) error {
				if sig == missingOn {
					return syscall.ESRCH
				}
				return syscall.Kill(pid, sig)
			}
			done := make(chan error, 1)
			go func() { _, err := s.Terminate(); done <- err }()
			select {
			case err := <-done:
				if !errors.Is(err, syscall.ESRCH) {
					t.Fatalf("Terminate error = %v, want ESRCH", err)
				}
			case <-time.After(2 * time.Second):
				_ = s.cmd.Process.Kill()
				<-done
				t.Fatal("Terminate waited indefinitely for a child outside its original group")
			}
			select {
			case <-s.done:
			default:
				t.Fatal("direct child was not reaped")
			}
		})
	}
}

func TestTerminateReportsDelayedReapAfterGroupKill(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		<-s.done
		_ = s.Close()
	})
	// Model a successful signal whose child does not become waitable promptly.
	calls := 0
	s.signalGroup = func(_ int, _ syscall.Signal) error { calls++; return nil }
	result := make(chan error, 1)
	go func() { _, err := s.Terminate(); result <- err }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Terminate reported success without reaping the child")
		}
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
		<-result
		t.Fatal("Terminate waited indefinitely after group kill")
	}
	firstCalls := calls
	go func() { _, err := s.Terminate(); result <- err }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("repeated Terminate reported success without reaping")
		}
		if calls != firstCalls {
			t.Fatal("repeated Terminate signaled the process group again")
		}
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
		<-result
		t.Fatal("repeated Terminate waited indefinitely")
	}
}

func TestTerminateRetriesDirectChildWithoutResignalingFailedGroup(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		<-s.done
		_ = s.Close()
	})
	groupCalls := 0
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		groupCalls++
		return syscall.ESRCH
	}
	killCalls := 0
	s.killChild = func(p *os.Process) error {
		killCalls++
		if killCalls == 1 {
			return syscall.EPERM
		}
		return p.Kill()
	}
	if _, err := s.Terminate(); !errors.Is(err, syscall.ESRCH) || !errors.Is(err, syscall.EPERM) {
		t.Fatalf("first termination error = %v, want ESRCH and EPERM", err)
	}
	if _, err := s.Terminate(); !errors.Is(err, syscall.ESRCH) || !errors.Is(err, syscall.EPERM) {
		t.Fatalf("retry error = %v, want preserved first errors", err)
	}
	if groupCalls != 1 || killCalls != 2 {
		t.Fatalf("group calls = %d, direct kill calls = %d; want 1 and 2", groupCalls, killCalls)
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("direct child was not reaped after retry")
	}
}

func TestCloseAfterDelayedDirectKillDoesNotResignalGroup(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.cmd.Process.Kill()
		<-s.done
		_ = s.Close()
	})
	groupCalls := 0
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		groupCalls++
		return syscall.EPERM
	}
	// Simulate a successful direct kill whose process is not yet waitable.
	s.killChild = func(_ *os.Process) error { return nil }
	result := make(chan error, 1)
	go func() { _, err := s.Terminate(); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, syscall.EPERM) || !strings.Contains(err.Error(), "reap direct child timed out") {
			t.Fatalf("Terminate error = %v, want group error and reap timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Terminate did not bound delayed direct-child reap")
	}
	go func() { result <- s.Close() }()
	select {
	case err := <-result:
		if !errors.Is(err, syscall.EPERM) || !strings.Contains(err.Error(), "reap direct child timed out") {
			t.Fatalf("Close error = %v, want preserved group error and reap timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after delayed direct-child reap")
	}
	if groupCalls != 1 {
		t.Fatalf("group signaled %d times, want 1", groupCalls)
	}
}

func TestCloseReportsStalledTermination(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		close(entered)
		<-release
		return syscall.EPERM
	}
	result := make(chan error, 1)
	go func() { result <- s.Close() }()
	<-entered
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "termination timed out") {
			t.Errorf("Close error = %v, want termination timeout", err)
		}
	case <-time.After(4 * time.Second):
		t.Error("Close waited indefinitely for a stalled termination")
	}
	close(release)
	_ = s.cmd.Process.Kill()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Error("child was not reaped after releasing signal")
	}
}

func TestCloseTimeoutDoesNotStartDirectKillAfterStalledSignal(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	called := make(chan struct{}, 1)
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		close(entered)
		<-release
		return syscall.EPERM
	}
	s.killChild = func(_ *os.Process) error {
		called <- struct{}{}
		return syscall.EPERM
	}
	result := make(chan error, 1)
	go func() { result <- s.Close() }()
	<-entered
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "termination timed out") {
			t.Errorf("Close error = %v, want termination timeout", err)
		}
	case <-time.After(4 * time.Second):
		t.Error("Close did not return after stalled signal")
	}
	close(release)
	// Wait until the original Terminate has left its serialized section.
	s.terminateMu.Lock()
	s.terminateMu.Unlock()
	select {
	case <-called:
		t.Error("started a direct-child kill after Close returned")
	default:
	}
	_ = s.cmd.Process.Kill()
	<-s.done
}

func TestQueuedTerminateReturnsAfterCloseAbandonsStalledSignal(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.signalGroup = func(_ int, _ syscall.Signal) error {
		close(entered)
		<-release
		return syscall.EPERM
	}
	first := make(chan error, 1)
	go func() { _, err := s.Terminate(); first <- err }()
	<-entered
	queued := make(chan error, 1)
	go func() { _, err := s.Terminate(); queued <- err }()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err == nil {
			t.Error("Close succeeded despite stalled termination")
		}
	case <-time.After(4 * time.Second):
		t.Error("Close did not return")
	}
	select {
	case err := <-queued:
		if err == nil || !strings.Contains(err.Error(), "termination abandoned") {
			t.Errorf("queued Terminate error = %v, want abandonment", err)
		}
	case <-time.After(time.Second):
		t.Error("queued Terminate remained blocked after Close abandoned termination")
	}
	close(release)
	<-first
	_ = s.cmd.Process.Kill()
	<-s.done
}

func TestCloseReportsEarlierTerminationFailure(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	s.signalGroup = func(_ int, _ syscall.Signal) error { return syscall.EPERM }
	if _, err := s.Terminate(); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Terminate error = %v, want EPERM", err)
	}
	if err := s.Close(); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Close error = %v, want earlier EPERM", err)
	}
}

func TestTerminateReportsGroupSignalFailureAndReapsChild(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.signalGroup = func(_ int, _ syscall.Signal) error { return syscall.EPERM }
	done := make(chan error, 1)
	go func() { _, err := s.Terminate(); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EPERM) {
			t.Fatalf("Terminate error = %v, want EPERM", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Terminate blocked after group signal failure")
	}
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("direct child was not reaped")
	}
}

func TestTerminateReportsEscalationFailureAndReapsChild(t *testing.T) {
	s, err := Spawn("/bin/sh", "-c", "trap '' HUP; printf 'ready\\n'; exec /bin/sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := bufio.NewReader(s.Reader()).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	s.signalGroup = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			return syscall.EPERM
		}
		return syscall.Kill(pid, sig)
	}
	_, err = s.Terminate()
	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Terminate error = %v, want EPERM", err)
	}
	select {
	case <-s.done:
	default:
		t.Fatal("direct child was not reaped")
	}
}

func TestCloseReportsStalledPTYClose(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{})
	s.closePTY = func() error {
		close(entered)
		<-release
		err := s.pty.Close()
		close(closed)
		return err
	}
	result := make(chan error, 1)
	go func() { result <- s.Close() }()
	<-entered
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "PTY close timed out") {
			t.Errorf("Close error = %v, want PTY close timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("Close waited indefinitely for stalled PTY close")
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Error("PTY close did not finish")
	}
	_ = s.cmd.Process.Kill()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
		t.Error("child was not reaped")
	}
}
