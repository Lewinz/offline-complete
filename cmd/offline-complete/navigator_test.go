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
