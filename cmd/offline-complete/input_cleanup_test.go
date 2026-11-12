package main

import (
	"errors"
	"io"
	"testing"
	"time"
)

type inputWriter struct{}

func (inputWriter) Write(p []byte) (int, error) { return len(p), nil }
func (inputWriter) Close() error                { return nil }

type failingCloseInputWriter struct{ err error }

func (w failingCloseInputWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w failingCloseInputWriter) Close() error                { return w.err }

type failedInput struct{ err error }

func (r failedInput) Read([]byte) (int, error) { return 0, r.err }

type inputCleanupSession struct {
	closed chan struct{}
	err    error
}

func (s *inputCleanupSession) Close() error { close(s.closed); return s.err }

func TestStalledInputCleanupReturnsError(t *testing.T) {
	done := make(chan struct{})
	start := time.Now()
	err := waitInputShutdown(done, 30*time.Millisecond)
	if err == nil {
		t.Fatal("stalled input cleanup returned success")
	}
	if time.Since(start) > time.Second {
		t.Fatal("stalled input cleanup waited too long")
	}
	close(done)
	if err := waitInputShutdown(done, time.Second); err != nil {
		t.Fatalf("completed input cleanup: %v", err)
	}
}

func TestInputWriterCloseFailureAfterEOFStartsSessionCleanup(t *testing.T) {
	writerFailure := errors.New("writer close failure")
	s := &inputCleanupSession{closed: make(chan struct{})}
	errs := make(chan error, 1)
	if err := forwardInput(s, failingCloseInputWriter{writerFailure}, func(io.Writer) error {
		return nil
	}, errs); err != nil {
		t.Fatalf("session close: %v", err)
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("writer close failure did not start session cleanup")
	}
	select {
	case err := <-errs:
		if !errors.Is(err, writerFailure) {
			t.Fatalf("writer close error = %v", err)
		}
	default:
		t.Fatal("writer close error not reported")
	}
}

func TestInputWriterCloseFailureStartsSessionCleanup(t *testing.T) {
	inputFailure := errors.New("input failure")
	writerFailure := errors.New("writer close failure")
	s := &inputCleanupSession{closed: make(chan struct{})}
	errs := make(chan error, 1)
	if err := forwardInput(s, failingCloseInputWriter{writerFailure}, func(io.Writer) error {
		return inputFailure
	}, errs); err != nil {
		t.Fatalf("session close: %v", err)
	}
	select {
	case <-s.closed:
	default:
		t.Fatal("writer close failure did not start session cleanup")
	}
	select {
	case err := <-errs:
		if !errors.Is(err, inputFailure) || !errors.Is(err, writerFailure) {
			t.Fatalf("input and writer close errors not preserved: %v", err)
		}
	default:
		t.Fatal("writer close error not reported")
	}
}

func TestInputFailureStartsCloseAndReportsBothErrors(t *testing.T) {
	inputFailure := errors.New("input failure")
	closeFailure := errors.New("close failure")
	s := &inputCleanupSession{closed: make(chan struct{}), err: closeFailure}
	errs := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- forwardInput(s, inputWriter{}, func(w io.Writer) error {
			_, err := io.Copy(w, failedInput{inputFailure})
			return err
		}, errs)
	}()
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("input failure did not start session close")
	}
	select {
	case err := <-done:
		if !errors.Is(err, closeFailure) {
			t.Fatalf("close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input cleanup did not finish")
	}
	if err := <-errs; !errors.Is(err, inputFailure) {
		t.Fatalf("input error = %v", err)
	}
}
