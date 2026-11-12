// Package input parses bytes received from a terminal into key and paste
// events. It is a reduced Go port of the WezTerm/termwiz input parser used by
// the reference implementation; mouse, Kitty encoding and Windows console
// records are intentionally out of scope for the first macOS version.
package input

import (
	"fmt"
	"unicode/utf8"
)

const (
	csi             = "\x1b["
	pasteStart      = "\x1b[200~"
	pasteEnd        = "\x1b[201~"
	maxPasteBytes   = 1 << 20
	inputChunkBytes = 32 << 10
)

// Modifiers is a bit set of held modifier keys.
type Modifiers uint8

const (
	ModNone   Modifiers = 0
	ModShift  Modifiers = 1 << 1
	ModAlt    Modifiers = 1 << 2
	ModCtrl   Modifiers = 1 << 3
	ModMeta   Modifiers = 1 << 4
	ModLeader Modifiers = 1 << 5
)

// Has reports whether all bits in other are set.
func (m Modifiers) Has(other Modifiers) bool { return m&other == other }

// KeyKind identifies a non-character key or a character key.
type KeyKind uint8

const (
	KeyChar KeyKind = iota
	KeyFunction
	KeyUpArrow
	KeyDownArrow
	KeyRightArrow
	KeyLeftArrow
	KeyApplicationUpArrow
	KeyApplicationDownArrow
	KeyApplicationRightArrow
	KeyApplicationLeftArrow
	KeyHome
	KeyEnd
	KeyTab
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyInsert
	KeyDelete
	KeyPageUp
	KeyPageDown
	KeyInternalPasteStart
	KeyInternalPasteEnd
)

// KeyCode is a decoded key. Rune is meaningful for KeyChar; Number for
// KeyFunction.
type KeyCode struct {
	Kind   KeyKind
	Rune   rune
	Number uint8
}

// Char returns a character key.
func Char(r rune) KeyCode { return KeyCode{Kind: KeyChar, Rune: r} }

// FunctionKey returns an F-key.
func FunctionKey(n uint8) KeyCode { return KeyCode{Kind: KeyFunction, Number: n} }

// KeyEvent is a key press with its modifiers.
type KeyEvent struct {
	Key       KeyCode
	Modifiers Modifiers
}

// EventKind discriminates InputEvent variants.
type EventKind uint8

const (
	EventKey EventKind = iota
	EventPaste
	EventResized
	// EventPasteTooLarge reports that a complete bracketed paste was discarded.
	EventPasteTooLarge
)

// InputEvent is a decoded terminal event.
type InputEvent struct {
	Kind  EventKind
	Key   KeyEvent
	Paste string
}

type inputState uint8

const (
	stateNormal inputState = iota
	stateEscapeMaybeAlt
	statePasting
	stateDiscardPaste
)

// InputParser incrementally decodes terminal input. Bytes may be split at
// arbitrary boundaries.
type InputParser struct {
	keys       *keyMap
	buf        readBuffer
	state      inputState
	pasteIndex int
}

// New returns a parser with the default xterm/SS3 key map.
func New() *InputParser {
	parser := &InputParser{keys: newKeyMap()}
	parser.buildBasicKeyMap()
	return parser
}

// Parse consumes bytes and returns every event that is fully determined. When
// maybeMore is true, an ambiguous prefix is buffered instead of decoded.
func (p *InputParser) Parse(data []byte, maybeMore bool) []InputEvent {
	var events []InputEvent
	for len(data) > inputChunkBytes {
		p.buf.extend(data[:inputChunkBytes])
		data = data[inputChunkBytes:]
		events = append(events, p.processBytes(true)...)
	}
	p.buf.extend(data)
	return append(events, p.processBytes(maybeMore)...)
}

// ParseAsVec consumes bytes assuming no more data is immediately available.
func (p *InputParser) ParseAsVec(data []byte) []InputEvent {
	return p.Parse(data, false)
}

func (p *InputParser) processBytes(maybeMore bool) []InputEvent {
	var events []InputEvent
	for p.buf.len() > 0 {
		switch p.state {
		case statePasting, stateDiscardPaste:
			end := []byte(pasteEnd)
			if index := p.buf.findSubsequence(p.pasteIndex, end); index >= 0 {
				if p.state == stateDiscardPaste || index > maxPasteBytes {
					events = append(events, InputEvent{Kind: EventPasteTooLarge})
				} else {
					events = append(events, InputEvent{Kind: EventPaste, Paste: string(p.buf.slice()[:index])})
				}
				p.buf.advance(index + len(end))
				p.state = stateNormal
				p.pasteIndex = 0
			} else {
				if p.state == statePasting && p.buf.len() > maxPasteBytes+len(end)-1 {
					p.state = stateDiscardPaste
				}
				if p.state == stateDiscardPaste && p.buf.len() > len(end)-1 {
					p.buf.advance(p.buf.len() - (len(end) - 1))
				}
				if remaining := p.buf.len() - len(end) + 1; remaining > 0 {
					p.pasteIndex = remaining
				} else {
					p.pasteIndex = 0
				}
				return events
			}
		case stateNormal, stateEscapeMaybeAlt:
			found := p.keys.lookup(p.buf.slice())
			if p.state == stateNormal && found.kind == foundExact && isEscape(found.value) && p.buf.len() > found.length {
				p.state = stateEscapeMaybeAlt
				p.buf.advance(found.length)
				continue
			}
			switch {
			case found.kind == foundExact:
				p.buf.advance(found.length)
				events = append(events, p.dispatch(found.value)...)
			case found.kind == foundAmbiguous && !maybeMore:
				p.buf.advance(found.length)
				events = append(events, p.dispatch(found.value)...)
			case (found.kind == foundAmbiguous || found.kind == foundNeedData) && maybeMore:
				return events
			default:
				r, size, ok := decodeOneChar(p.buf.slice())
				if !ok {
					return events
				}
				p.buf.advance(size)
				events = append(events, p.dispatch(InputEvent{
					Kind: EventKey,
					Key:  KeyEvent{Key: Char(r), Modifiers: ModNone},
				})...)
			}
		}
	}
	return events
}

func (p *InputParser) dispatch(event InputEvent) []InputEvent {
	switch p.state {
	case stateNormal:
		if isPasteStart(event) {
			p.state = statePasting
			p.pasteIndex = 0
			return nil
		}
		return []InputEvent{event}
	case stateEscapeMaybeAlt:
		if isPasteStart(event) {
			p.state = statePasting
			p.pasteIndex = 0
			return []InputEvent{escapeEvent()}
		}
		p.state = stateNormal
		if event.Kind == EventKey {
			event.Key.Modifiers |= ModAlt
			return []InputEvent{event}
		}
		return []InputEvent{escapeEvent(), event}
	default:
		return []InputEvent{event}
	}
}

func escapeEvent() InputEvent {
	return InputEvent{Kind: EventKey, Key: KeyEvent{Key: KeyCode{Kind: KeyEscape}}}
}

func isEscape(event InputEvent) bool {
	return event.Kind == EventKey && event.Key.Key.Kind == KeyEscape
}

func isPasteStart(event InputEvent) bool {
	return event.Kind == EventKey && event.Key.Key.Kind == KeyInternalPasteStart
}

func decodeOneChar(data []byte) (rune, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	if len(data) > 4 {
		data = data[:4]
	}
	if !utf8.FullRune(data) {
		return 0, 0, false
	}
	// DecodeRune consumes one invalid byte as RuneError, so malformed input
	// cannot permanently block the following terminal events.
	r, size := utf8.DecodeRune(data)
	return r, size, true
}

type modifierCombo struct {
	suffix string
	mods   Modifiers
}

var basicCombos = []modifierCombo{
	{"", ModNone},
	{";1", ModNone},
	{";2", ModShift},
	{";3", ModAlt},
	{";4", ModAlt | ModShift},
	{";5", ModCtrl},
	{";6", ModCtrl | ModShift},
	{";7", ModCtrl | ModAlt},
	{";8", ModCtrl | ModAlt | ModShift},
}

var metaCombos = []modifierCombo{
	{";9", ModMeta},
	{";10", ModMeta | ModShift},
	{";11", ModMeta | ModAlt},
	{";12", ModMeta | ModAlt | ModShift},
	{";13", ModMeta | ModCtrl},
	{";14", ModMeta | ModCtrl | ModShift},
	{";15", ModMeta | ModCtrl | ModAlt},
	{";16", ModMeta | ModCtrl | ModAlt | ModShift},
}

func allCombos() []modifierCombo {
	all := make([]modifierCombo, 0, len(basicCombos)+len(metaCombos))
	all = append(all, basicCombos...)
	return append(all, metaCombos...)
}

func (p *InputParser) insert(sequence string, key KeyCode, modifiers Modifiers) {
	p.keys.insert([]byte(sequence), InputEvent{
		Kind: EventKey,
		Key:  KeyEvent{Key: key, Modifiers: modifiers},
	})
}

func (p *InputParser) buildBasicKeyMap() {
	// Ctrl-[A..Z] are sent as 1..26; Alt-A..Z is often ESC followed by the letter.
	for alpha := byte('A'); alpha <= 'Z'; alpha++ {
		p.insert(string([]byte{alpha & 0x1f}), Char(rune(alpha)), ModCtrl)
		p.insert(string([]byte{0x1b, alpha}), Char(rune(alpha)), ModAlt)
	}

	// CSI u encodings for the ASCII range.
	for c := 0; c <= 0x7f; c++ {
		for _, combo := range allCombos() {
			p.insert(fmt.Sprintf("\x1b[%d%su", c, combo.suffix), Char(rune(c)), combo.mods)
		}
	}

	arrows := []struct {
		kind KeyKind
		code byte
	}{
		{KeyUpArrow, 'A'}, {KeyDownArrow, 'B'}, {KeyRightArrow, 'C'}, {KeyLeftArrow, 'D'},
		{KeyHome, 'H'}, {KeyEnd, 'F'},
	}
	for _, arrow := range arrows {
		p.insert(string([]byte{0x1b, '[', arrow.code}), KeyCode{Kind: arrow.kind}, ModNone)
		for _, combo := range allCombos() {
			p.insert(fmt.Sprintf("\x1b[1%s%c", combo.suffix, arrow.code), KeyCode{Kind: arrow.kind}, combo.mods)
		}
	}

	rxvt := []struct {
		kind KeyKind
		code byte
	}{
		{KeyUpArrow, 'a'}, {KeyDownArrow, 'b'}, {KeyRightArrow, 'c'}, {KeyLeftArrow, 'd'},
	}
	for _, arrow := range rxvt {
		p.insert(string([]byte{0x1b, '[', arrow.code}), KeyCode{Kind: arrow.kind}, ModShift)
		p.insert(string([]byte{0x1b, 'O', arrow.code}), KeyCode{Kind: arrow.kind}, ModCtrl)
	}

	applicationArrows := []struct {
		kind KeyKind
		code byte
	}{
		{KeyApplicationUpArrow, 'A'}, {KeyApplicationDownArrow, 'B'},
		{KeyApplicationRightArrow, 'C'}, {KeyApplicationLeftArrow, 'D'},
	}
	for _, arrow := range applicationArrows {
		p.insert(string([]byte{0x1b, 'O', arrow.code}), KeyCode{Kind: arrow.kind}, ModNone)
		for _, combo := range basicCombos {
			p.insert(fmt.Sprintf("\x1bO1%s%c", combo.suffix, arrow.code), KeyCode{Kind: arrow.kind}, combo.mods)
		}
	}

	// SS3 function keys without modifiers.
	ss3 := []struct {
		key  KeyCode
		code byte
	}{
		{KeyCode{Kind: KeyEnd}, 'F'},
		{KeyCode{Kind: KeyHome}, 'H'},
		{KeyCode{Kind: KeyTab}, 'I'},
		{KeyCode{Kind: KeyEnter}, 'M'},
		{FunctionKey(1), 'P'},
		{FunctionKey(2), 'Q'},
		{FunctionKey(3), 'R'},
		{FunctionKey(4), 'S'},
		{Char('='), 'X'},
	}
	for _, entry := range ss3 {
		p.insert(string([]byte{0x1b, 'O', entry.code}), entry.key, ModNone)
	}
	p.insert("\x1bOSP", Char(' '), ModNone)

	// F1-F4 with modifiers.
	for _, entry := range []struct {
		number uint8
		code   byte
	}{{1, 'P'}, {2, 'Q'}, {3, 'R'}, {4, 'S'}} {
		for _, combo := range allCombos() {
			p.insert(fmt.Sprintf("\x1b[1%s%c", combo.suffix, entry.code), FunctionKey(entry.number), combo.mods)
		}
	}

	// F5-F20 with modifiers, encoded as CSI number ~.
	for _, group := range []struct {
		first, last int
		offset      int
	}{
		{1, 5, 10}, {6, 10, 11}, {11, 14, 12}, {15, 16, 13}, {17, 20, 14},
	} {
		for n := group.first; n <= group.last; n++ {
			for _, combo := range allCombos() {
				p.insert(fmt.Sprintf("\x1b[%d%s~", n+group.offset, combo.suffix), FunctionKey(uint8(n)), combo.mods)
			}
		}
	}

	for _, entry := range []struct {
		key  KeyKind
		code byte
	}{
		{KeyInsert, '2'}, {KeyDelete, '3'}, {KeyHome, '1'}, {KeyEnd, '4'},
		{KeyPageUp, '5'}, {KeyPageDown, '6'}, {KeyHome, '7'}, {KeyEnd, '8'},
	} {
		for _, suffix := range []struct {
			code byte
			mods Modifiers
		}{
			{'~', ModNone}, {'$', ModShift}, {'^', ModCtrl}, {'@', ModShift | ModCtrl},
		} {
			p.insert(string([]byte{0x1b, '[', entry.code, suffix.code}), KeyCode{Kind: entry.key}, suffix.mods)
		}
	}

	p.insert("\x7f", KeyCode{Kind: KeyBackspace}, ModNone)
	p.insert("\x08", KeyCode{Kind: KeyBackspace}, ModNone)
	p.insert("\x1b", KeyCode{Kind: KeyEscape}, ModNone)
	p.insert("\t", KeyCode{Kind: KeyTab}, ModNone)
	p.insert("\x1b[Z", KeyCode{Kind: KeyTab}, ModShift)
	p.insert("\r", KeyCode{Kind: KeyEnter}, ModNone)
	p.insert("\n", KeyCode{Kind: KeyEnter}, ModNone)
	p.insert(pasteStart, KeyCode{Kind: KeyInternalPasteStart}, ModNone)
	p.insert(pasteEnd, KeyCode{Kind: KeyInternalPasteEnd}, ModNone)
}
