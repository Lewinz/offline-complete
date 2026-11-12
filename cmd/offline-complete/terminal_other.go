//go:build !darwin

package main

import (
	"errors"
	"io"
	"os"

	"github.com/creack/pty"
)

func terminalSize(file *os.File) (*pty.Winsize, error) { return pty.GetsizeFull(file) }

func canPollInput(_ *os.File, terminal bool) bool { return terminal }

func copyTerminalInput(*os.File, io.Writer, <-chan struct{}) error {
	return errors.New("interactive terminal mode is only supported on macOS")
}

func makeRaw(*os.File) (func() error, error) {
	return nil, errors.New("interactive terminal mode is only supported on macOS")
}
