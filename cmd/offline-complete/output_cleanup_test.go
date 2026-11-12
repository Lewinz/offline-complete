package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

type blockedOutput struct {
	started chan struct{}
	release chan struct{}
}

func (w blockedOutput) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return len(p), nil
}

type delayedInputFailure struct {
	release chan struct{}
	err     error
}

func (r delayedInputFailure) Read([]byte) (int, error) {
	<-r.release
	return 0, r.err
}

func TestExitedChildInterruptsStalledOutput(t *testing.T) {
	output := blockedOutput{make(chan struct{}), make(chan struct{})}
	defer close(output.release)
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "printf x"}, strings.NewReader(""), output)
		done <- err
	}()
	select {
	case <-output.started:
	case <-time.After(3 * time.Second):
		t.Fatal("output did not start")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "output worker") {
			t.Fatalf("missing stalled output error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exited child did not interrupt stalled output")
	}
}

func TestOutputDrainWaitsWhileProgressContinues(t *testing.T) {
	progress := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 4 {
			time.Sleep(80 * time.Millisecond)
			progress <- struct{}{}
		}
	}()
	if err := waitOutputDrain(done, progress, 150*time.Millisecond); err != nil {
		t.Fatalf("progressing output timed out: %v", err)
	}
}

func TestOutputDrainStopsExtendingAfterTermination(t *testing.T) {
	progress := make(chan struct{}, 1)
	abort := make(chan struct{})
	done := make(chan struct{})
	finished := make(chan error, 1)
	go func() { finished <- waitOutputDrain(done, progress, 150*time.Millisecond, abort) }()
	close(abort)
	for i := 0; i < 8; i++ {
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("termination returned without reporting stalled output")
			}
			return
		case <-time.After(60 * time.Millisecond):
			select {
			case progress <- struct{}{}:
			default:
			}
		}
	}
	t.Fatal("continued progress prevented termination")
}

func TestOutputDrainReportsInputFailureDespiteProgress(t *testing.T) {
	progress := make(chan struct{}, 1)
	inputErr := make(chan error, 1)
	failure := errors.New("input failed during output drain")
	done := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- waitOutputDrainWithFailures(done, progress, inputErr, nil, 150*time.Millisecond, nil)
	}()
	inputErr <- failure
	for i := 0; i < 10; i++ {
		select {
		case err := <-finished:
			if !errors.Is(err, failure) {
				t.Fatalf("missing input failure: %v", err)
			}
			return
		case <-time.After(60 * time.Millisecond):
			select {
			case progress <- struct{}{}:
			default:
			}
		}
	}
	close(done)
	t.Fatal("input failure did not interrupt progressing output")
}

func TestOutputDrainReportsResizeFailureWhenOutputCompletes(t *testing.T) {
	failure := errors.New("resize failed during output drain")
	resizeErr := make(chan error, 1)
	resizeErr <- failure
	done := make(chan struct{})
	close(done)
	if err := waitOutputDrainWithFailures(done, nil, nil, resizeErr, time.Second, nil); !errors.Is(err, failure) {
		t.Fatalf("missing resize failure: %v", err)
	}
}

type delayedOutputFailure struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (w delayedOutputFailure) Write([]byte) (int, error) {
	close(w.started)
	<-w.release
	return 0, w.err
}

func TestOutputDrainPreservesConcurrentInputAndOutputErrors(t *testing.T) {
	inputFailure := errors.New("late input failure")
	outputFailure := errors.New("output write failure")
	input := delayedInputFailure{make(chan struct{}), inputFailure}
	output := delayedOutputFailure{make(chan struct{}), make(chan struct{}), outputFailure}
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "printf x"}, input, output)
		done <- err
	}()
	select {
	case <-output.started:
	case <-time.After(3 * time.Second):
		close(input.release)
		close(output.release)
		t.Fatal("output did not start")
	}
	// Let the direct child exit and the proxy enter output draining before
	// delivering the input error, then release the failing output writer.
	time.Sleep(300 * time.Millisecond)
	close(input.release)
	time.Sleep(100 * time.Millisecond)
	close(output.release)
	select {
	case err := <-done:
		if !errors.Is(err, inputFailure) || !errors.Is(err, outputFailure) {
			t.Fatalf("missing input or output failure: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not return")
	}
}

func TestInputFailureInterruptsStalledOutput(t *testing.T) {
	failure := errors.New("injected input failure")
	input := delayedInputFailure{make(chan struct{}), failure}
	output := blockedOutput{make(chan struct{}), make(chan struct{})}
	defer close(output.release)
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "printf x; exec /bin/sleep 10"}, input, output)
		done <- err
	}()
	select {
	case <-output.started:
	case <-time.After(3 * time.Second):
		close(input.release)
		t.Fatal("output did not start")
	}
	close(input.release)
	select {
	case err := <-done:
		if !errors.Is(err, failure) || !strings.Contains(err.Error(), "output worker") {
			t.Fatalf("missing input or output error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled output prevented input failure cleanup")
	}
}

func TestResizeFailureInterruptsStalledOutput(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	terminalFD := int(slave.Fd())
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	nullFD := int(null.Fd())
	output := blockedOutput{make(chan struct{}), make(chan struct{})}
	defer close(output.release)
	done := make(chan error, 1)
	go func() {
		_, err := run([]string{"/bin/sh", "-c", "printf x; exec /bin/sleep 10"}, slave, output)
		done <- err
	}()
	select {
	case <-output.started:
	case <-time.After(3 * time.Second):
		t.Fatal("output did not start")
	}
	// Replace the descriptor atomically without racing os.File.Close against Fd.
	// The original terminal is no longer available for restoration in this test.
	if err := syscall.Dup2(nullFD, terminalFD); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "read terminal size") || !strings.Contains(err.Error(), "output worker") || !strings.Contains(err.Error(), "restore terminal") {
			t.Fatalf("missing resize, stalled output, or restoration error: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("stalled output prevented resize failure cleanup")
	}
}

type twoChunkOutput struct {
	reads int
}

func (r *twoChunkOutput) Read(p []byte) (int, error) {
	r.reads++
	if r.reads > 2 {
		return 0, io.EOF
	}
	p[0] = byte('0' + r.reads)
	return 1, nil
}

type firstWriteBlocked struct {
	started chan struct{}
	release chan struct{}
	writes  int
}

func (w *firstWriteBlocked) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		close(w.started)
		<-w.release
	}
	return len(p), nil
}

func TestStoppedOutputDoesNotReadAfterBlockedWrite(t *testing.T) {
	src := &twoChunkOutput{}
	dst := &firstWriteBlocked{started: make(chan struct{}), release: make(chan struct{})}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := forwardOutput(dst, src, make(chan syscall.Signal), stop)
		done <- err
	}()
	select {
	case <-dst.started:
	case <-time.After(time.Second):
		t.Fatal("output did not start")
	}
	close(stop)
	close(dst.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("output worker did not stop")
	}
	if src.reads != 1 {
		t.Fatalf("read %d chunks, want only the first", src.reads)
	}
}

type closedOutput struct{}

func (closedOutput) Write([]byte) (int, error) { return 0, os.ErrClosed }

type outputReadError struct{ err error }

func (r outputReadError) Read([]byte) (int, error) { return 0, r.err }

func TestForwardOutputSignalCloseKeepsExitCode(t *testing.T) {
	signals := make(chan syscall.Signal, 1)
	signals <- syscall.SIGTERM
	code, err := forwardOutput(io.Discard, outputReadError{os.ErrClosed}, signals)
	if code != 143 || err != nil {
		t.Fatalf("code = %d, error = %v; want 143 and nil", code, err)
	}
}

func TestForwardOutputClosedReadWithoutSignalFails(t *testing.T) {
	_, err := forwardOutput(io.Discard, outputReadError{os.ErrClosed}, make(chan syscall.Signal))
	if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("error = %v, want closed read error", err)
	}
}

func TestForwardOutputDoesNotHideIndependentErrors(t *testing.T) {
	readFailure := errors.New("read failure")
	for _, tc := range []struct {
		name string
		dst  io.Writer
		src  io.Reader
		want error
	}{
		{"write closed", closedOutput{}, bytes.NewBufferString("data"), os.ErrClosed},
		{"read failure", io.Discard, outputReadError{readFailure}, readFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signals := make(chan syscall.Signal, 1)
			signals <- syscall.SIGTERM
			_, err := forwardOutput(tc.dst, tc.src, signals)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
