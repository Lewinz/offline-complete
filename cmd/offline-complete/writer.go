package main

import (
	"io"
	"sync"
)

// lockedWriter serializes PTY writes from terminal input and candidate
// insertion so a replacement sequence cannot interleave with a keystroke.
type lockedWriter struct {
	mu sync.Mutex
	w  io.WriteCloser
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(data)
}

func (w *lockedWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Close()
}
