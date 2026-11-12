package main

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

type closingSession struct {
	closed chan struct{}
	err    error
}

func (s closingSession) Close() error { close(s.closed); return s.err }

func TestTerminationWorkerProcessesSignalQueuedBeforeStop(t *testing.T) {
	for i := 0; i < 1000; i++ {
		s := closingSession{closed: make(chan struct{})}
		signals := make(chan os.Signal, 1)
		interrupted := make(chan syscall.Signal, 1)
		signals <- syscall.SIGTERM
		stop := startTermination(s, signals, interrupted)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		select {
		case sig := <-interrupted:
			if sig != syscall.SIGTERM {
				t.Fatalf("signal = %v", sig)
			}
		default:
			t.Fatalf("iteration %d: queued signal discarded", i)
		}
		select {
		case <-s.closed:
		default:
			t.Fatal("signal did not close session")
		}
	}
}

func TestCleanupSignalOverridesNormalExitButNotErrors(t *testing.T) {
	failure := errors.New("output failure")
	for _, tc := range []struct {
		err  error
		want int
	}{{nil, 143}, {failure, 7}} {
		signals := make(chan syscall.Signal, 1)
		signals <- syscall.SIGTERM
		if got := exitCodeAfterCleanup(7, tc.err, signals); got != tc.want {
			t.Fatalf("error %v: code = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestTerminationWorkerStartsCloseAndReportsError(t *testing.T) {
	failure := errors.New("close failed")
	s := closingSession{closed: make(chan struct{}), err: failure}
	signals := make(chan os.Signal, 1)
	interrupted := make(chan syscall.Signal, 1)
	stop := startTermination(s, signals, interrupted)
	signals <- syscall.SIGTERM
	select {
	case <-s.closed:
	case <-time.After(time.Second):
		t.Fatal("termination did not start session close")
	}
	if sig := <-interrupted; sig != syscall.SIGTERM {
		t.Fatalf("signal = %v", sig)
	}
	if err := stop(); !errors.Is(err, failure) {
		t.Fatalf("close error = %v", err)
	}
}
