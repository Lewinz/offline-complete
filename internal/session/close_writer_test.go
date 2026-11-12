package session

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestTerminateRejectsNewWritesBeforeSignallingChild(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.signalGroup = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGHUP {
			close(entered)
			<-release
		}
		return syscall.Kill(pid, sig)
	}
	finished := make(chan error, 1)
	go func() { _, err := s.Terminate(); finished <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		<-finished
		_ = s.Close()
		t.Fatal("Terminate did not begin signalling")
	}
	_, writeErr := s.Writer().Write([]byte("unsubmitted input\n"))
	close(release)
	terminateErr := <-finished
	closeErr := s.Close()
	if !errors.Is(writeErr, os.ErrClosed) {
		t.Errorf("write during Terminate: %v, want os.ErrClosed", writeErr)
	}
	if terminateErr != nil || closeErr != nil {
		t.Logf("termination error: %v; close error: %v", terminateErr, closeErr)
	}
}

func TestCloseRejectsNewWritesBeforeTerminatingChild(t *testing.T) {
	s, err := Spawn("/bin/sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	s.signalGroup = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGHUP {
			close(entered)
			<-release
		}
		return syscall.Kill(pid, sig)
	}
	finished := make(chan error, 1)
	go func() { finished <- s.Close() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		<-finished
		t.Fatal("Close did not begin termination")
	}
	_, writeErr := s.Writer().Write([]byte("unsubmitted input\n"))
	close(release)
	closeErr := <-finished
	if !errors.Is(writeErr, os.ErrClosed) {
		t.Errorf("write during Close: %v, want os.ErrClosed", writeErr)
	}
	if closeErr != nil {
		t.Logf("termination reported: %v", closeErr)
	}
}
