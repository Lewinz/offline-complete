//go:build !darwin

package main

import (
	"errors"
	"io"
)

func runAgent(string, []string, io.Reader, io.Writer) (int, error) {
	return 1, errors.New("completion agent mode is only supported on macOS")
}
