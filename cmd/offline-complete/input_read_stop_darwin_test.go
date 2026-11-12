//go:build darwin

package main

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestTerminalInputReadFinishingAfterStopIsDiscarded(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	restore, err := makeRaw(slave)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()

	stop := make(chan struct{})
	readDone := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- copyTerminalInputWithRead(slave, &output, stop, func(fd int, p []byte) (int, error) {
			n, err := unix.Read(fd, p)
			close(readDone)
			<-release
			return n, err
		})
	}()
	if _, err := master.Write([]byte("sensitive input")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal read did not complete")
	}
	close(stop)
	// Release the read only after the stop notification is visible.
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copy terminal input: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal input did not stop")
	}
	if output.Len() != 0 {
		t.Fatalf("forwarded %d bytes after stop", output.Len())
	}
}
