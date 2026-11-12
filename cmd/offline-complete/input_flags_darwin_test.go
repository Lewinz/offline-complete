//go:build darwin

package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRunPreservesNonblockingPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	raw, err := reader.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	getFlags := func() int {
		var flags int
		var inner error
		if err := raw.Control(func(fd uintptr) { flags, inner = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
			t.Fatal(err)
		}
		if inner != nil {
			t.Fatal(inner)
		}
		return flags
	}
	if getFlags()&unix.O_NONBLOCK == 0 {
		t.Fatal("test pipe is not nonblocking")
	}
	code, err := run([]string{"/bin/sh", "-c", "exit 0"}, reader, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("run: code %d, error %v", code, err)
	}
	if getFlags()&unix.O_NONBLOCK == 0 {
		t.Fatal("proxy changed caller's pipe to blocking")
	}
}

func TestInputPollingRetriesTemporaryNotReady(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	var output bytes.Buffer
	first := true
	err = copyTerminalInputWithRead(reader, &output, make(chan struct{}), func(fd int, p []byte) (int, error) {
		if first {
			first = false
			return 0, unix.EAGAIN
		}
		return unix.Read(fd, p)
	})
	if err != nil || output.String() != "hello" {
		t.Fatalf("output %q, error %v; want hello and nil", output.String(), err)
	}
}

func TestInputPollingPreservesNonblockingPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	raw, err := reader.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	flags := func() int {
		var f int
		var inner error
		if err := raw.Control(func(fd uintptr) { f, inner = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
			t.Fatal(err)
		}
		if inner != nil {
			t.Fatal(inner)
		}
		return f
	}
	if flags()&unix.O_NONBLOCK == 0 {
		t.Fatal("test pipe is not nonblocking")
	}
	stop := make(chan struct{})
	close(stop)
	if err := copyTerminalInput(reader, io.Discard, stop); err != nil {
		t.Fatal(err)
	}
	if flags()&unix.O_NONBLOCK == 0 {
		t.Fatal("input polling changed caller's pipe to blocking")
	}
}
