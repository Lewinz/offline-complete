package editstate

import (
	"math"
	"net"
	"testing"
	"time"
)

func TestEditingCycleCloseInterruptsStalledSend(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	publisher, err := NewPublisher(client, "session", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cycles := NewCyclePublisher(publisher)
	sent := make(chan error, 1)
	go func() { _, err := cycles.Begin(); sent <- err }()
	// Read the first byte to establish that the frame write is in progress,
	// then leave the rest unread so the pipe remains blocked.
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- cycles.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cycle close waited for stalled send")
	}
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("stalled send succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled send did not return")
	}
	if _, err := cycles.Begin(); err == nil {
		t.Fatal("closed cycle restarted")
	}
	if err := cycles.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEditingCycleCloseInvalidatesSnapshotAndRejectsEvents(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	publisher, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	cycles := NewCyclePublisher(publisher)
	cycle, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := cycles.Update(cycle, "private", 7, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 2, true)
	if err := cycles.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not observe close")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("close retained snapshot")
	}
	if err := cycles.Update(cycle, "late", 4, true); err == nil {
		t.Fatal("late update accepted")
	}
	if err := cycles.End(cycle); err == nil {
		t.Fatal("late end accepted")
	}
}

func TestEditingCyclesInvalidateAndRejectDelayedEvents(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	publisher, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	var state State
	go ConsumeStream(server, "session", &state)
	cycles := NewCyclePublisher(publisher)
	first, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := cycles.Update(first, "aé界", 2, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 2, true)
	if snap, ok := state.Snapshot(); !ok || snap.Line != "aé界" || snap.Cursor != 3 {
		t.Fatalf("snapshot = %+v, present=%v", snap, ok)
	}
	if err := cycles.End(first); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 0, false)
	second, err := cycles.Begin()
	if err != nil || second == first {
		t.Fatalf("second cycle = %d, %v", second, err)
	}
	if err := cycles.Update(second, "new", 3, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 5, true)
	if err := cycles.Update(first, "old", 3, true); err == nil {
		t.Fatal("accepted delayed update")
	}
	if err := cycles.End(first); err == nil {
		t.Fatal("accepted delayed end")
	}
	if snap, ok := state.Snapshot(); !ok || snap.Line != "new" {
		t.Fatalf("delayed event changed new cycle: %+v, %v", snap, ok)
	}
	if err := cycles.End(second); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 0, false)
}

func TestEditingCycleExhaustionInvalidatesState(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	publisher, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	cycles := NewCyclePublisher(publisher)
	cycle, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := cycles.Update(cycle, "old", 3, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 2, true)
	cycles.last = math.MaxUint64
	if _, err := cycles.Begin(); err == nil {
		t.Fatal("cycle counter wrapped")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("exhausted cycle remained connected")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("exhaustion retained snapshot")
	}
}

func TestEditingCycleSendFailurePermanentlyCloses(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	publisher, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	cycles := NewCyclePublisher(publisher)
	cycle, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := cycles.Update(cycle, "old", 3, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 2, true)
	if err := cycles.Update(cycle, "é", 1, false); err == nil {
		t.Fatal("unknown zsh mode accepted")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed publisher remained connected")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("failed update retained snapshot")
	}
	if _, err := cycles.Begin(); err == nil {
		t.Fatal("failed publisher reused")
	}
}
