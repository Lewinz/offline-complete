package editstate

import (
	"net"
	"testing"
	"time"
)

func TestPublisherZshCursor(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	p, err := NewPublisher(client, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var state State
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(server, "session", &state) }()
	if err := p.UpdateZsh("aé界", 2, true); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 1, true)
	snap, ok := state.Snapshot()
	if !ok || snap.Line != "aé界" || snap.Cursor != 3 {
		t.Fatalf("snapshot = %+v, present=%v", snap, ok)
	}
	if err := p.Clear(); err != nil {
		t.Fatal(err)
	}
	awaitSnapshot(t, &state, 0, false)
}

func TestPublisherZshInvalidation(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		cursor     int
		multibyte  bool
	}{
		{"mode", "é", 1, false},
		{"cursor", "é", 2, true},
		{"utf8", string([]byte{0xff}), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer server.Close()
			p, err := NewPublisher(client, "session", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var state State
			done := make(chan error, 1)
			go func() { done <- ConsumeStream(server, "session", &state) }()
			if err := p.Update("old", 3); err != nil {
				t.Fatal(err)
			}
			awaitSnapshot(t, &state, 1, true)
			if err := p.UpdateZsh(tc.line, tc.cursor, tc.multibyte); err == nil {
				t.Fatal("invalid zsh update accepted")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("consumer remained connected")
			}
			if _, ok := state.Snapshot(); ok {
				t.Fatal("stale snapshot survived")
			}
			if err := p.Update("new", 3); err == nil {
				t.Fatal("publisher reused after failure")
			}
		})
	}
}
