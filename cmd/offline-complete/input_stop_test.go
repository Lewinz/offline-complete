package main

import (
	"bytes"
	"io"
	"testing"
	"time"
)

type delayedInput struct {
	started chan struct{}
	release chan struct{}
}

func (r *delayedInput) Read(p []byte) (int, error) {
	close(r.started)
	<-r.release
	copy(p, "late input")
	return len("late input"), io.EOF
}

type gatedOutput struct {
	started chan struct{}
	release chan struct{}
}

func (w gatedOutput) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}

type countedInput struct{ reads int }

func (r *countedInput) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.EOF
	}
	copy(p, "x")
	return 1, nil
}

func TestWaitInputShutdownAlreadyStoppedAtDeadline(t *testing.T) {
	done := make(chan struct{})
	close(done)
	for i := 0; i < 100; i++ {
		if err := waitInputShutdown(done, 0); err != nil {
			t.Fatalf("already stopped input reported as stalled: %v", err)
		}
	}
}

func TestStoppedInputDoesNotReadAgainAfterBlockedWrite(t *testing.T) {
	r := &countedInput{}
	w := gatedOutput{started: make(chan struct{}), release: make(chan struct{})}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- copyInputUntilStopped(w, r, stop) }()
	select {
	case <-w.started:
	case <-time.After(time.Second):
		t.Fatal("output write did not start")
	}
	close(stop)
	close(w.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped copy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input copy did not stop")
	}
	if r.reads != 1 {
		t.Fatalf("reads = %d, want 1", r.reads)
	}
}

func TestStoppedInputDoesNotForwardLateRead(t *testing.T) {
	r := &delayedInput{started: make(chan struct{}), release: make(chan struct{})}
	stop := make(chan struct{})
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- copyInputUntilStopped(&output, r, stop) }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("input read did not start")
	}
	close(stop)
	close(r.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped copy: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input copy did not stop")
	}
	if output.Len() != 0 {
		t.Fatalf("forwarded input after stop: %q", output.String())
	}
}
