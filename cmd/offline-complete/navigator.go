package main

import (
	"bytes"
	"io"
	"unicode"
	"unicode/utf8"

	"offline-complete/internal/agent"
)

// navigator consumes keyboard navigation while a candidate offer is active.
// Everything else is forwarded to the shell unchanged. When no offer is
// active, the chunk is returned untouched so the shell keeps its normal key
// bindings.
type navigator struct {
	can            func() bool
	on             func(agent.Navigation) bool
	onSubmit       func()
	tabAccept      bool
	enterExecute   bool
	reloadSettings func() (tabAccept bool, enterExecute bool)
}

func (n *navigator) keySettings() (bool, bool) {
	if n.reloadSettings != nil {
		return n.reloadSettings()
	}
	return n.tabAccept, n.enterExecute
}

func (n *navigator) Filter(chunk []byte) []byte {
	if n == nil || n.can == nil || n.on == nil {
		return chunk
	}
	if !n.can() {
		if n.onSubmit != nil {
			for _, b := range chunk {
				if b == 0x0d {
					n.onSubmit()
				}
			}
		}
		return chunk
	}
	tabAccept, enterExecute := n.keySettings()
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
			action := agent.NavigationNext
			if tabAccept {
				action = agent.NavigationInsert
			}
			if n.on(action) {
				index++
				continue
			}
		case 0x0d: // Enter
			if enterExecute {
				// Clear the popup, but let the original Enter reach the shell.
				n.on(agent.NavigationDismiss)
				if n.onSubmit != nil {
					n.onSubmit()
				}
				filtered = append(filtered, b)
				index++
				continue
			}
			if n.on(agent.NavigationAccept) {
				index++
				continue
			}
			if n.onSubmit != nil {
				n.onSubmit()
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
	pastes    pasteNormalizer
}

func (w *filterWriter) Write(data []byte) (int, error) {
	written := len(data)
	dismissed := false
	for _, segment := range w.pastes.Normalize(data) {
		if segment.literal {
			// A multi-line or control-character paste is left framed for the
			// shell's safe-paste handling, so completion may never see the
			// resulting buffer. Drop the candidate panel instead of leaving a
			// stale list on screen.
			if !dismissed && w.navigator != nil && w.navigator.on != nil {
				w.navigator.on(agent.NavigationDismiss)
				dismissed = true
			}
			if _, err := w.upstream.Write(segment.data); err != nil {
				return 0, err
			}
			continue
		}
		filtered := w.navigator.Filter(segment.data)
		if len(filtered) == 0 {
			continue
		}
		if _, err := w.upstream.Write(filtered); err != nil {
			return 0, err
		}
	}
	return written, nil
}

func (w *filterWriter) Close() error { return w.upstream.Close() }

const (
	pasteStart        = "\x1b[200~"
	pasteEnd          = "\x1b[201~"
	maxUnwrappedPaste = 64 * 1024
	maxBufferedPaste  = 1 << 20
)

type inputSegment struct {
	data    []byte
	literal bool
}

// pasteNormalizer removes bracketed-paste framing for a single-line printable
// paste. Some zsh/PTY combinations insert a framed paste without firing
// line-pre-redraw, leaving completion looking at the previous buffer. Plain
// text always redraws; multi-line or control-character pastes keep the frame
// and bypass key navigation so zsh can apply its own safe-paste handling.
type pasteNormalizer struct {
	pending []byte
	inPaste bool
}

func (p *pasteNormalizer) Normalize(data []byte) []inputSegment {
	p.pending = append(p.pending, data...)
	var out []inputSegment
	add := func(value []byte, literal bool) {
		if len(value) == 0 {
			return
		}
		out = append(out, inputSegment{data: append([]byte(nil), value...), literal: literal})
	}
	for {
		if !p.inPaste {
			index := bytes.Index(p.pending, []byte(pasteStart))
			if index < 0 {
				keep := partialMarkerSuffix(p.pending, pasteStart)
				add(p.pending[:len(p.pending)-keep], false)
				p.pending = append(p.pending[:0], p.pending[len(p.pending)-keep:]...)
				return out
			}
			add(p.pending[:index], false)
			p.pending = p.pending[index+len(pasteStart):]
			p.inPaste = true
			continue
		}
		index := bytes.Index(p.pending, []byte(pasteEnd))
		if index < 0 {
			if len(p.pending) > maxBufferedPaste {
				// Do not hold an unbounded paste. Forward what we have and let
				// the shell process the remainder as normal input.
				add(append([]byte(pasteStart), p.pending...), true)
				p.pending = p.pending[:0]
				p.inPaste = false
			}
			return out
		}
		payload := p.pending[:index]
		p.pending = p.pending[index+len(pasteEnd):]
		p.inPaste = false
		if safePastePayload(payload) {
			add(payload, false)
			continue
		}
		framed := make([]byte, 0, len(pasteStart)+len(payload)+len(pasteEnd))
		framed = append(framed, pasteStart...)
		framed = append(framed, payload...)
		framed = append(framed, pasteEnd...)
		add(framed, true)
	}
}

// partialMarkerSuffix keeps a split paste-start marker together. A lone ESC is
// deliberately not buffered: it is the dismiss key when no other bytes follow.
func partialMarkerSuffix(data []byte, marker string) int {
	limit := len(marker) - 1
	if len(data) < limit {
		limit = len(data)
	}
	for length := limit; length >= 2; length-- {
		if bytes.Equal(data[len(data)-length:], []byte(marker[:length])) {
			return length
		}
	}
	return 0
}

func safePastePayload(payload []byte) bool {
	if len(payload) == 0 || len(payload) > maxUnwrappedPaste || !utf8.Valid(payload) {
		return false
	}
	for _, value := range string(payload) {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}
