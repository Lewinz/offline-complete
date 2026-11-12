package input

import (
	"reflect"
	"strings"
	"testing"
)

func keyEvent(key KeyCode, modifiers Modifiers) InputEvent {
	return InputEvent{Kind: EventKey, Key: KeyEvent{Key: key, Modifiers: modifiers}}
}

func charEvent(r rune, modifiers Modifiers) InputEvent {
	return keyEvent(Char(r), modifiers)
}

func assertEvents(t *testing.T, want, got []InputEvent) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestSimple(t *testing.T) {
	parser := New()
	got := parser.ParseAsVec([]byte("hello"))
	want := []InputEvent{
		charEvent('h', ModNone),
		charEvent('e', ModNone),
		charEvent('l', ModNone),
		charEvent('l', ModNone),
		charEvent('o', ModNone),
	}
	assertEvents(t, want, got)
}

func TestInvalidUTF8DoesNotBlockLaterInput(t *testing.T) {
	parser := New()
	got := parser.Parse([]byte{0xff, 'a'}, true)
	want := []InputEvent{charEvent('�', ModNone), charEvent('a', ModNone)}
	assertEvents(t, want, got)
	assertEvents(t, []InputEvent{charEvent('b', ModNone)}, parser.ParseAsVec([]byte("b")))
}

func TestSplitUTF8IsBuffered(t *testing.T) {
	parser := New()
	if got := parser.Parse([]byte{0xe2, 0x82}, true); len(got) != 0 {
		t.Fatalf("incomplete UTF-8 produced events: %#v", got)
	}
	assertEvents(t, []InputEvent{charEvent('€', ModNone)}, parser.ParseAsVec([]byte{0xac}))
}

func TestControlCharacters(t *testing.T) {
	parser := New()
	got := parser.ParseAsVec([]byte("\x03\x1bJ\x7f"))
	want := []InputEvent{
		charEvent('C', ModCtrl),
		charEvent('J', ModAlt),
		keyEvent(KeyCode{Kind: KeyBackspace}, ModNone),
	}
	assertEvents(t, want, got)
}

func TestArrowKeys(t *testing.T) {
	parser := New()
	got := parser.ParseAsVec([]byte("\x1bOA\x1bOB\x1bOC\x1bOD"))
	want := []InputEvent{
		keyEvent(KeyCode{Kind: KeyApplicationUpArrow}, ModNone),
		keyEvent(KeyCode{Kind: KeyApplicationDownArrow}, ModNone),
		keyEvent(KeyCode{Kind: KeyApplicationRightArrow}, ModNone),
		keyEvent(KeyCode{Kind: KeyApplicationLeftArrow}, ModNone),
	}
	assertEvents(t, want, got)
}

func TestPartial(t *testing.T) {
	parser := New()
	var got []InputEvent
	parser.Parse([]byte("\x1b[11"), true)
	got = append(got, parser.Parse([]byte("~"), true)...)
	assertEvents(t, []InputEvent{keyEvent(FunctionKey(1), ModNone)}, got)
}

func TestPartialAmbiguous(t *testing.T) {
	parser := New()
	assertEvents(t, []InputEvent{keyEvent(KeyCode{Kind: KeyEscape}, ModNone)}, parser.ParseAsVec([]byte("\x1b")))

	var got []InputEvent
	got = append(got, parser.Parse([]byte("\x1b[11"), true)...)
	got = append(got, parser.Parse(nil, false)...)
	want := []InputEvent{
		keyEvent(KeyCode{Kind: KeyEscape}, ModNone),
		charEvent('[', ModNone),
		charEvent('1', ModNone),
		charEvent('1', ModNone),
	}
	assertEvents(t, want, got)
}

func TestPartialBracketedPaste(t *testing.T) {
	parser := New()
	var got []InputEvent
	got = append(got, parser.Parse([]byte("\x1b[200~1234"), false)...)
	got = append(got, parser.Parse([]byte("5678\x1b[201~"), false)...)
	assertEvents(t, []InputEvent{{Kind: EventPaste, Paste: "12345678"}}, got)
}

func TestLargePaste(t *testing.T) {
	parser := New()
	payload := strings.Repeat("abcdefg", 4000)
	input := pasteStart + payload + pasteEnd
	got := parser.Parse([]byte(input), false)
	assertEvents(t, []InputEvent{{Kind: EventPaste, Paste: payload}}, got)
}

func TestLargePartialBracketedPaste(t *testing.T) {
	parser := New()
	first := strings.Repeat("abcdefg", 4000)
	second := strings.Repeat("hijklmno", 4000)
	var got []InputEvent
	got = append(got, parser.Parse([]byte(pasteStart+first), false)...)
	got = append(got, parser.Parse([]byte(second+pasteEnd), false)...)
	assertEvents(t, []InputEvent{{Kind: EventPaste, Paste: first + second}}, got)
}

func TestParseGeneral(t *testing.T) {
	parser := New()
	cases := []struct {
		input string
		want  []InputEvent
	}{
		{"\x1b", []InputEvent{keyEvent(KeyCode{Kind: KeyEscape}, ModNone)}},
		{"a", []InputEvent{charEvent('a', ModNone)}},
		{"\x1b[A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModNone)}},
		{"\x1b[1;2A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModShift)}},
		{"\x1b[1;3A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModAlt)}},
		{"\x1b[1;5A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModCtrl)}},
		{"\x1b[1;9A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModMeta)}},
		{"\x1b[1;13A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModMeta|ModCtrl)}},
		{"\x1b[1;16A", []InputEvent{keyEvent(KeyCode{Kind: KeyUpArrow}, ModMeta|ModCtrl|ModShift|ModAlt)}},
		{"\x1bOA", []InputEvent{keyEvent(KeyCode{Kind: KeyApplicationUpArrow}, ModNone)}},
		{"\x1bOI", []InputEvent{keyEvent(KeyCode{Kind: KeyTab}, ModNone)}},
		{"\x1bOSP", []InputEvent{charEvent(' ', ModNone)}},
	}
	for _, test := range cases {
		assertEvents(t, test.want, parser.ParseAsVec([]byte(test.input)))
	}
}

func TestKeyMapLookup(t *testing.T) {
	km := newKeyMap()
	if got := km.lookup([]byte("boo")); got.kind != foundNone {
		t.Fatalf("empty map lookup = %v", got.kind)
	}

	km.insert([]byte("boa"), charEvent('a', ModNone))
	km.insert([]byte("boo"), charEvent('o', ModNone))
	km.insert([]byte("boom"), charEvent('m', ModNone))

	cases := []struct {
		key    string
		kind   foundKind
		length int
	}{
		{"b", foundNeedData, 0},
		{"bo", foundNeedData, 0},
		{"boa", foundExact, 3},
		{"boo", foundAmbiguous, 3},
		{"boom", foundExact, 4},
		{"boom!", foundExact, 4},
	}
	for _, test := range cases {
		got := km.lookup([]byte(test.key))
		if got.kind != test.kind || (test.kind != foundNeedData && got.length != test.length) {
			t.Errorf("lookup(%q) = {%v %d}, want {%v %d}", test.key, got.kind, got.length, test.kind, test.length)
		}
	}
}

func TestKeyMapSequence(t *testing.T) {
	km := newKeyMap()
	km.insert([]byte("\x03"), charEvent('c', ModCtrl))
	km.insert([]byte("\x27"), charEvent('\'', ModNone))
	km.insert([]byte("\x03XYZ"), charEvent('x', ModCtrl))

	if got := km.lookup([]byte("\x03")); got.kind != foundAmbiguous || got.length != 1 {
		t.Errorf("lookup control = {%v %d}", got.kind, got.length)
	}
	if got := km.lookup([]byte("\x03foo")); got.kind != foundExact || got.length != 1 {
		t.Errorf("lookup control+text = {%v %d}", got.kind, got.length)
	}
	if got := km.lookup([]byte("\x03X")); got.kind != foundAmbiguous || got.length != 1 {
		t.Errorf("lookup control+prefix = {%v %d}", got.kind, got.length)
	}
}

func TestReadBufferSubsequence(t *testing.T) {
	var buf readBuffer
	buf.extend([]byte("abcdef"))
	if got := buf.findSubsequence(0, []byte("cd")); got != 2 {
		t.Fatalf("index = %d, want 2", got)
	}
	if got := buf.findSubsequence(3, []byte("cd")); got != -1 {
		t.Fatalf("index after offset = %d, want -1", got)
	}
	buf.advance(2)
	if string(buf.slice()) != "cdef" {
		t.Fatalf("remaining = %q", buf.slice())
	}
}
