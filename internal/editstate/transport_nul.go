package editstate

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"offline-complete/internal/shellparse"
)

const maxNULField = 64 * 1024

// ZLETransportScript returns the opt-in zsh fragment plus a NUL-delimited
// callback that writes editing events to file descriptor 3. The caller must
// pass a writable descriptor as fd 3, drain it continuously, and close it when
// the session ends. The callback is synchronous: a blocked descriptor blocks
// the shell, and the buffer is not persisted or logged by this fragment.
//
// Record layout (all six fields NUL-terminated):
//
//	event, sequence, cursor, multibyte, line, cwd
//
// begin and end carry an empty line. A shell buffer cannot contain NUL.
func ZLETransportScript() string {
	return ZLEHooks + zleNULTransport + "\n_offline_complete_zle_enable || true\n"
}

const zleNULTransport = `
function _offline_complete_zle_event {
  typeset -g _offline_complete_sequence=$(( ${_offline_complete_sequence:-0} + 1 ))
  printf '%s\0%s\0%s\0%s\0%s\0%s\0' "$1" "$_offline_complete_sequence" "$3" "$4" "$2" "$5" >&3 2>/dev/null || return 1
  return 0
}
`

// ConsumeNUL consumes NUL-delimited editing records from fd 3 until EOF or the
// first invalid record. On every return the editing state is closed, so a
// transport failure cannot leave a stale snapshot available for insertion.
// The descriptor itself binds the records to one session; callers must not
// share it between sessions.
func ConsumeNUL(reader io.Reader, state *State) error {
	return ConsumeNULNotify(reader, state, nil)
}

// ConsumeNULNotify is ConsumeNUL with a callback invoked after every accepted
// record. The callback runs on the consuming goroutine and must return quickly;
// it must not write to the editing state or block on the same transport.
func ConsumeNULNotify(reader io.Reader, state *State, notify func()) error {
	if state == nil || reader == nil {
		if state != nil {
			state.Close()
		}
		return errors.New("invalid NUL editing stream")
	}
	defer state.Close()

	buffered := bufio.NewReaderSize(reader, 4096)
	fields := make([]string, 6)
	for {
		for i := range fields {
			field, err := readNULField(buffered)
			if err != nil {
				return err
			}
			fields[i] = field
		}
		if err := applyNULRecord(fields, state); err != nil {
			return err
		}
		if notify != nil {
			notify()
		}
	}
}

func readNULField(reader *bufio.Reader) (string, error) {
	data := make([]byte, 0, 64)
	for {
		value, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if value == 0 {
			if !utf8.Valid(data) {
				return "", errors.New("invalid UTF-8 in editing record")
			}
			return string(data), nil
		}
		if len(data) >= maxNULField {
			return "", errors.New("editing record field too long")
		}
		data = append(data, value)
	}
}

func applyNULRecord(fields []string, state *State) error {
	event, rawSequence, rawCursor, multibyte, line, cwd := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]
	if !utf8.ValidString(line) {
		return errors.New("invalid UTF-8 in editing buffer")
	}
	if !utf8.ValidString(cwd) {
		return errors.New("invalid UTF-8 in working directory")
	}
	sequence, err := strconv.ParseUint(rawSequence, 10, 64)
	if err != nil || sequence == 0 {
		return errors.New("invalid editing sequence")
	}
	switch event {
	case "begin", "end":
		if line != "" {
			return errors.New("editing boundary carried a buffer")
		}
		return state.Clear(sequence)
	case "update":
		cursor, err := strconv.Atoi(rawCursor)
		if err != nil || cursor < 0 {
			return errors.New("invalid editing cursor")
		}
		offset := cursor
		switch multibyte {
		case "1":
			offset, err = shellparse.ZshCursorToByte(line, cursor)
			if err != nil {
				return err
			}
		case "0":
			if cursor > len(line) || (cursor < len(line) && !utf8.RuneStart(line[cursor])) {
				return errors.New("invalid editing cursor")
			}
		default:
			return errors.New("invalid multibyte flag")
		}
		return state.UpdateWithCWD(sequence, line, offset, cwd)
	default:
		return errors.New("unknown editing event")
	}
}
