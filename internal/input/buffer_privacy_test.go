package input

import (
	"bytes"
	"testing"
)

func TestConsumedInputIsNotRetainedInParserBuffer(t *testing.T) {
	p := New()
	events := p.Parse([]byte("secret\x1b["), true)
	if len(events) != len("secret") {
		t.Fatalf("got %d events, want six characters", len(events))
	}
	if got := string(p.buf.slice()); got != "\x1b[" {
		t.Fatalf("pending input = %q", got)
	}
	if bytes.Contains(p.buf.storage[:cap(p.buf.storage)], []byte("secret")) {
		t.Fatal("consumed input retained in parser buffer")
	}
	for _, b := range p.buf.storage[len(p.buf.storage):cap(p.buf.storage)] {
		if b != 0 {
			t.Fatal("consumed bytes retained beyond pending input")
		}
	}
}
