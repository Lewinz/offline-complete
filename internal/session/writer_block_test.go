package session

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestWriterCloseReleasesQueuedWrite(t *testing.T) {
	read, file, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w := &writer{file: file}
	firstDone := make(chan struct{})
	go func() {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, 16<<20))
		close(firstDone)
	}()
	t.Cleanup(func() {
		_ = read.Close()
		<-firstDone
		_ = file.Close()
	})
	select {
	case <-firstDone:
		t.Fatal("first write unexpectedly completed")
	case <-time.After(150 * time.Millisecond):
	}
	queued := make(chan error, 1)
	go func() { _, err := w.Write([]byte("queued")); queued <- err }()
	select {
	case err := <-queued:
		t.Fatalf("queued write unexpectedly returned: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-queued:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("queued write: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued write remained blocked after Close")
	}
}

func TestWriterCloseDoesNotWaitForBlockedWrite(t *testing.T) {
	read, file, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w := &writer{file: file}
	written := make(chan struct{})
	go func() {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, 16<<20))
		close(written)
	}()
	t.Cleanup(func() {
		_ = read.Close()
		<-written
		_ = file.Close()
	})
	select {
	case <-written:
		t.Fatal("write unexpectedly completed without a reader")
	case <-time.After(150 * time.Millisecond):
	}
	closed := make(chan error, 1)
	go func() { closed <- w.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer Close blocked behind a write")
	}
	if _, err := w.Write([]byte("later")); err != os.ErrClosed {
		t.Fatalf("write after Close: %v", err)
	}
}
