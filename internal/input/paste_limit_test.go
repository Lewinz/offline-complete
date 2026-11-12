package input

import (
	"strings"
	"testing"
)

func TestOversizedPasteIsDiscardedUntilEnd(t *testing.T) {
	p := New()
	var events []InputEvent
	events = append(events, p.Parse([]byte(pasteStart+strings.Repeat("x", 1024*1024+1)), true)...)
	if len(events) != 0 {
		t.Fatalf("unexpected events before paste end: %v", events)
	}
	events = append(events, p.Parse([]byte("\x1b[20"), true)...)
	events = append(events, p.Parse([]byte("1~z"), false)...)
	assertEvents(t, []InputEvent{{Kind: EventPasteTooLarge}, charEvent('z', ModNone)}, events)
}

func TestUnterminatedOversizedPasteHasBoundedBuffer(t *testing.T) {
	p := New()
	for i := 0; i < 16; i++ {
		data := strings.Repeat("x", 1024*1024)
		if i == 0 {
			data = pasteStart + data
		}
		if events := p.Parse([]byte(data), true); len(events) != 0 {
			t.Fatalf("unexpected events while discarding paste: %v", events)
		}
	}
	if len(p.buf.storage) > len(pasteEnd)-1 || cap(p.buf.storage) > 2*1024*1024 {
		t.Fatalf("unbounded paste buffer: len=%d cap=%d", len(p.buf.storage), cap(p.buf.storage))
	}
	assertEvents(t, []InputEvent{{Kind: EventPasteTooLarge}, charEvent('z', ModNone)}, p.Parse([]byte(pasteEnd+"z"), false))
}

func TestOversizedPasteEndSplitAtEveryByte(t *testing.T) {
	for split := 0; split <= len(pasteEnd); split++ {
		p := New()
		data := pasteStart + strings.Repeat("x", 1024*1024+32) + pasteEnd[:split]
		var got []InputEvent
		got = append(got, p.Parse([]byte(data), true)...)
		got = append(got, p.Parse([]byte(pasteEnd[split:]+"z"), false)...)
		assertEvents(t, []InputEvent{{Kind: EventPasteTooLarge}, charEvent('z', ModNone)}, got)
	}
}

func TestPasteAtLimitIsAccepted(t *testing.T) {
	p := New()
	payload := strings.Repeat("x", 1024*1024)
	got := p.Parse([]byte(pasteStart+payload+pasteEnd), false)
	assertEvents(t, []InputEvent{{Kind: EventPaste, Paste: payload}}, got)
}
