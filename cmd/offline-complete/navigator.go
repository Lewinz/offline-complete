package main

import (
	"io"

	"offline-complete/internal/agent"
)

// navigator consumes keyboard navigation while a candidate offer is active.
// Everything else is forwarded to the shell unchanged. When no offer is
// active, the chunk is returned untouched so the shell keeps its normal key
// bindings.
type navigator struct {
	can func() bool
	on  func(agent.Navigation) bool
}

func (n *navigator) Filter(chunk []byte) []byte {
	if n == nil || n.can == nil || n.on == nil || !n.can() {
		return chunk
	}
	filtered := make([]byte, 0, len(chunk))
	for index := 0; index < len(chunk); {
		b := chunk[index]
		if b == 0x1b {
			if index+2 < len(chunk) && (chunk[index+1] == '[' || chunk[index+1] == 'O') &&
				(chunk[index+2] == 'A' || chunk[index+2] == 'B') {
				action := agent.NavigationPrevious
				if chunk[index+2] == 'B' {
					action = agent.NavigationNext
				}
				if n.on(action) {
					index += 3
					continue
				}
			} else if index+1 == len(chunk) {
				if n.on(agent.NavigationDismiss) {
					index++
					continue
				}
			}
			filtered = append(filtered, b)
			index++
			continue
		}
		switch b {
		case 0x10: // Ctrl-P
			if n.on(agent.NavigationPrevious) {
				index++
				continue
			}
		case 0x0e: // Ctrl-N
			if n.on(agent.NavigationNext) {
				index++
				continue
			}
		case 0x09: // Tab
			if n.on(agent.NavigationNext) {
				index++
				continue
			}
		case 0x0d: // Enter
			if n.on(agent.NavigationAccept) {
				index++
				continue
			}
		}
		filtered = append(filtered, b)
		index++
	}
	return filtered
}

// filterWriter applies navigation filtering before bytes reach the shell.
type filterWriter struct {
	upstream  io.WriteCloser
	navigator *navigator
}

func (w *filterWriter) Write(data []byte) (int, error) {
	filtered := w.navigator.Filter(data)
	if len(filtered) == 0 {
		return len(data), nil
	}
	if _, err := w.upstream.Write(filtered); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *filterWriter) Close() error { return w.upstream.Close() }
