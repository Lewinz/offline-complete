package main

import (
	"errors"
	"syscall"
	"testing"
	"time"

	"offline-complete/internal/session"
)

type controlledWait struct {
	entered chan struct{}
	release chan struct{}
	status  session.ExitStatus
	err     error
}

func (w controlledWait) Wait() (session.ExitStatus, error) {
	close(w.entered)
	<-w.release
	return w.status, w.err
}

func TestSignalDuringChildWaitUsesSignalExitCode(t *testing.T) {
	w := controlledWait{entered: make(chan struct{}), release: make(chan struct{}), status: session.ExitStatus{Code: 129}}
	signals := make(chan syscall.Signal, 1)
	done := make(chan int, 1)
	go func() { code, _ := waitForChild(w, signals, nil); done <- code }()
	<-w.entered
	signals <- syscall.SIGTERM
	close(w.release)
	if code := <-done; code != 143 {
		t.Fatalf("exit code = %d, want 143", code)
	}
}

func TestSignalDuringStalledChildWaitReturnsError(t *testing.T) {
	w := controlledWait{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	signals := make(chan syscall.Signal, 1)
	done := make(chan error, 1)
	go func() { _, err := waitForChild(w, signals, nil); done <- err }()
	<-w.entered
	signals <- syscall.SIGTERM
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled child wait reported success")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("child wait did not return after signal")
	}
}

func TestInputFailureDuringChildWaitReturnsError(t *testing.T) {
	w := controlledWait{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	inputErr := make(chan error, 1)
	want := errors.New("input failed")
	done := make(chan error, 1)
	go func() { _, err := waitForChild(w, nil, inputErr); done <- err }()
	<-w.entered
	inputErr <- want
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("wait error = %v, want input failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input failure did not interrupt child wait")
	}
}

func TestResizeFailureDuringChildWaitReturnsError(t *testing.T) {
	w := controlledWait{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(w.release)
	resizeErr := make(chan error, 1)
	want := errors.New("resize failed")
	done := make(chan error, 1)
	go func() { _, err := waitForChild(w, nil, nil, resizeErr); done <- err }()
	<-w.entered
	resizeErr <- want
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("wait error = %v, want resize failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resize failure did not interrupt child wait")
	}
}

func TestReadyChildWaitErrorSurvivesConcurrentInputFailure(t *testing.T) {
	waitErr := errors.New("wait failed")
	inputFailure := errors.New("input failed")
	for i := 0; i < 100; i++ {
		done := make(chan childWaitResult, 1)
		done <- childWaitResult{err: waitErr}
		input := make(chan error, 1)
		input <- inputFailure
		_, err := awaitChild(done, nil, input, nil)
		if !errors.Is(err, waitErr) || !errors.Is(err, inputFailure) {
			t.Fatalf("iteration %d: error = %v, want both wait and input failures", i, err)
		}
	}
}

func TestChildWaitErrorIsNotHiddenBySignal(t *testing.T) {
	want := errors.New("wait failed")
	w := controlledWait{entered: make(chan struct{}), release: make(chan struct{}), err: want}
	signals := make(chan syscall.Signal, 1)
	signals <- syscall.SIGTERM
	close(w.release)
	_, err := waitForChild(w, signals, nil)
	if !errors.Is(err, want) {
		t.Fatalf("wait error = %v, want %v", err, want)
	}
}
