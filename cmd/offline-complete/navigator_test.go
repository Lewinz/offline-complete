package main

import (
	"bytes"
	"testing"

	"offline-complete/internal/agent"
)

func TestNavigatorForwardsEverythingWithoutAnOffer(t *testing.T) {
	var actions []agent.Navigation
	nav := &navigator{
		can: func() bool { return false },
		on: func(action agent.Navigation) bool {
			actions = append(actions, action)
			return true
		},
	}
	input := []byte("a\x1b[B\x0e\r\x1b")
	output := nav.Filter(input)
	if !bytes.Equal(output, input) {
		t.Fatalf("output = %q, want %q", output, input)
	}
	if len(actions) != 0 {
		t.Fatalf("actions = %v", actions)
	}
}

func TestNavigatorObservesEnterWithoutAnOffer(t *testing.T) {
	submits := 0
	nav := &navigator{
		can:      func() bool { return false },
		on:       func(agent.Navigation) bool { return false },
		onSubmit: func() { submits++ },
	}
	output := nav.Filter([]byte("cd foo\r"))
	if string(output) != "cd foo\r" || submits != 1 {
		t.Fatalf("output = %q submits = %d", output, submits)
	}
}

func TestNavigatorDoesNotDoubleRecordAcceptedSelection(t *testing.T) {
	submits := 0
	nav := &navigator{
		can:      func() bool { return true },
		on:       func(agent.Navigation) bool { return true },
		onSubmit: func() { submits++ },
	}
	if output := nav.Filter([]byte("\r")); len(output) != 0 || submits != 0 {
		t.Fatalf("output = %q submits = %d", output, submits)
	}
}

func TestNavigatorConsumesNavigationAndForwardsText(t *testing.T) {
	var actions []agent.Navigation
	nav := &navigator{
		can: func() bool { return true },
		on: func(action agent.Navigation) bool {
			actions = append(actions, action)
			return true
		},
	}
	input := []byte("a\x1b[Bb\x0e\t\r\x10\x1bOB\x1b")
	output := nav.Filter(input)
	if string(output) != "ab" {
		t.Fatalf("output = %q", output)
	}
	want := []agent.Navigation{
		agent.NavigationNext,
		agent.NavigationNext,
		agent.NavigationNext,
		agent.NavigationAccept,
		agent.NavigationPrevious,
		agent.NavigationNext,
		agent.NavigationDismiss,
	}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v", actions)
	}
	for index := range want {
		if actions[index] != want[index] {
			t.Fatalf("actions = %v, want %v", actions, want)
		}
	}
}

func TestNavigatorHonorsTabAcceptAndEnterExecute(t *testing.T) {
	var actions []agent.Navigation
	nav := &navigator{
		can: func() bool { return true },
		on: func(action agent.Navigation) bool {
			actions = append(actions, action)
			return true
		},
		tabAccept:    true,
		enterExecute: true,
	}
	output := nav.Filter([]byte("\t\r"))
	if string(output) != "\r" {
		t.Fatalf("output = %q, want Enter passthrough", output)
	}
	want := []agent.Navigation{agent.NavigationInsert, agent.NavigationDismiss}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v", actions)
	}
	for index := range want {
		if actions[index] != want[index] {
			t.Fatalf("actions = %v, want %v", actions, want)
		}
	}
}

func TestNavigatorForwardsKeysWhenTheActionIsRejected(t *testing.T) {
	nav := &navigator{
		can: func() bool { return true },
		on:  func(agent.Navigation) bool { return false },
	}
	input := []byte("\x1b[A\t\r\x10")
	output := nav.Filter(input)
	if !bytes.Equal(output, input) {
		t.Fatalf("output = %q, want %q", output, input)
	}
}

func TestNavigatorLeavesSplitOrModifiedSequencesAlone(t *testing.T) {
	actions := 0
	nav := &navigator{
		can: func() bool { return true },
		on: func(agent.Navigation) bool {
			actions++
			return true
		},
	}
	if output := nav.Filter([]byte("\x1b[")); string(output) != "\x1b[" {
		t.Fatalf("split prefix = %q", output)
	}
	if output := nav.Filter([]byte("A")); string(output) != "A" {
		t.Fatalf("split suffix = %q", output)
	}
	if output := nav.Filter([]byte("\x1b[1;5A")); string(output) != "\x1b[1;5A" {
		t.Fatalf("modified arrow = %q", output)
	}
	if actions != 0 {
		t.Fatalf("actions = %d", actions)
	}
}

func TestFilterWriterReportsConsumedLength(t *testing.T) {
	var upstream bytes.Buffer
	nav := &navigator{
		can: func() bool { return true },
		on:  func(agent.Navigation) bool { return true },
	}
	writer := &filterWriter{upstream: nopWriteCloser{Buffer: &upstream}, navigator: nav}
	written, err := writer.Write([]byte("a\x0eb"))
	if err != nil {
		t.Fatal(err)
	}
	if written != 3 {
		t.Fatalf("written = %d, want 3", written)
	}
	if upstream.String() != "ab" {
		t.Fatalf("upstream = %q", upstream.String())
	}
}

type nopWriteCloser struct {
	*bytes.Buffer
}

func (w nopWriteCloser) Close() error { return nil }

func newTestFilterWriter(nav *navigator) (*filterWriter, *bytes.Buffer) {
	var upstream bytes.Buffer
	return &filterWriter{upstream: nopWriteCloser{Buffer: &upstream}, navigator: nav}, &upstream
}

func TestFilterWriterUnwrapsSingleLineBracketedPaste(t *testing.T) {
	actions := 0
	nav := &navigator{
		can: func() bool { return true },
		on: func(agent.Navigation) bool {
			actions++
			return true
		},
	}
	writer, upstream := newTestFilterWriter(nav)
	input := []byte(pasteStart + "billamountbackfill" + pasteEnd)
	written, err := writer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(input) {
		t.Fatalf("written = %d, want %d", written, len(input))
	}
	if upstream.String() != "billamountbackfill" {
		t.Fatalf("upstream = %q", upstream.String())
	}
	if actions != 0 {
		t.Fatalf("actions = %d", actions)
	}
}

func TestFilterWriterReassemblesSplitBracketedPaste(t *testing.T) {
	nav := &navigator{
		can: func() bool { return true },
		on:  func(agent.Navigation) bool { return true },
	}
	writer, upstream := newTestFilterWriter(nav)
	input := []byte(pasteStart + "billamountbackfill" + pasteEnd)
	for _, split := range []int{2, 4, 6, 12, len(input) - 3} {
		if _, err := writer.Write(input[:split]); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(input[split:]); err != nil {
			t.Fatal(err)
		}
		if upstream.String() != "billamountbackfill" {
			t.Fatalf("split %d: upstream = %q", split, upstream.String())
		}
		upstream.Reset()
	}
}

func TestFilterWriterKeepsUnsafePastesFramed(t *testing.T) {
	for _, payload := range []string{"one\ntwo", "one\r", "one\x03two", "one\ttwo", "one\x1btwo"} {
		nav := &navigator{
			can: func() bool { return true },
			on:  func(agent.Navigation) bool { return true },
		}
		writer, upstream := newTestFilterWriter(nav)
		input := []byte(pasteStart + payload + pasteEnd)
		if _, err := writer.Write(input); err != nil {
			t.Fatal(err)
		}
		if upstream.String() != string(input) {
			t.Fatalf("payload %q: upstream = %q", payload, upstream.String())
		}
	}
}

func TestFilterWriterHidesOfferForMultilinePaste(t *testing.T) {
	var actions []agent.Navigation
	nav := &navigator{
		can: func() bool { return true },
		on: func(action agent.Navigation) bool {
			actions = append(actions, action)
			return true
		},
	}
	writer, upstream := newTestFilterWriter(nav)
	input := []byte(pasteStart + "one\ntwo" + pasteEnd)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if upstream.String() != string(input) {
		t.Fatalf("upstream = %q, want framed paste", upstream.String())
	}
	if len(actions) != 1 || actions[0] != agent.NavigationDismiss {
		t.Fatalf("actions = %v, want one dismiss", actions)
	}
}

func TestFilterWriterKeepsLoneEscapeForDismissal(t *testing.T) {
	dismissed := false
	nav := &navigator{
		can: func() bool { return true },
		on: func(action agent.Navigation) bool {
			if action == agent.NavigationDismiss {
				dismissed = true
			}
			return true
		},
	}
	writer, upstream := newTestFilterWriter(nav)
	if _, err := writer.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	if !dismissed {
		t.Fatal("lone escape was buffered instead of dismissing")
	}
	if upstream.Len() != 0 {
		t.Fatalf("upstream = %q", upstream.String())
	}
}
