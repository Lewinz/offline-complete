package editstate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
	"unicode/utf8"

	"offline-complete/internal/ipc"
	"offline-complete/internal/strictjson"
)

// ConsumeStream consumes a session's editing frames until an error or EOF.
// The caller must authenticate the connection, impose deadlines, and close it.
// Every return permanently closes state; it does not erase historical memory.
func ConsumeStream(reader io.Reader, session string, state *State) error {
	return consumeStream(reader, session, state, nil)
}

// ConsumeConn applies one total read deadline to each frame, including idle
// time before its first byte. It owns the read deadline until it returns and
// clears it afterward; callers must authenticate and close the connection.
func ConsumeConn(conn net.Conn, session string, state *State, timeout time.Duration) (err error) {
	if state == nil {
		return errors.New("nil editing state")
	}
	if conn == nil || timeout <= 0 {
		state.Close()
		return errors.New("invalid editing stream deadline")
	}
	defer func() { err = errors.Join(err, conn.SetReadDeadline(time.Time{})) }()
	return consumeStream(conn, session, state, func() error {
		return conn.SetReadDeadline(time.Now().Add(timeout))
	})
}

func consumeStream(reader io.Reader, session string, state *State, beforeFrame func() error) error {
	if state == nil {
		return errors.New("nil editing state")
	}
	defer state.Close()
	if reader == nil || session == "" || len(session) > ipc.MaxFrame || !utf8.ValidString(session) {
		return errors.New("invalid editing stream arguments")
	}
	// Reject a session that cannot fit both kinds of minimally valid frame.
	if WriteClear(io.Discard, session, 1) != nil || WriteUpdate(io.Discard, session, 1, "", 0) != nil {
		return errors.New("invalid editing stream session")
	}
	for {
		if beforeFrame != nil {
			if err := beforeFrame(); err != nil {
				return err
			}
		}
		var header [4]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		length := binary.BigEndian.Uint32(header[:])
		if length == 0 || length > ipc.MaxFrame {
			return errors.New("invalid editing frame length")
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
		if err := applyFrame(payload, session, state); err != nil {
			return err
		}
	}
}

func applyFrame(payload []byte, session string, state *State) error {
	invalid := errors.New("invalid editing message")
	if !utf8.Valid(payload) || !json.Valid(payload) || strictjson.ValidateSurrogates(payload) != nil {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return invalid
	}
	var version int
	var id, kind, line, cwd string
	var sequence uint64
	var cursor int
	seen := make(map[string]bool, 7)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalid
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return invalid
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return invalid
		}
		switch key {
		case "version":
			err = json.Unmarshal(raw, &version)
		case "session_id":
			err = json.Unmarshal(raw, &id)
		case "type":
			err = json.Unmarshal(raw, &kind)
		case "sequence":
			err = json.Unmarshal(raw, &sequence)
		case "line":
			err = json.Unmarshal(raw, &line)
		case "cursor":
			err = json.Unmarshal(raw, &cursor)
		case "cwd":
			err = json.Unmarshal(raw, &cwd)
		default:
			return invalid
		}
		if err != nil {
			return invalid
		}
	}
	if _, err = decoder.Token(); err != nil {
		return invalid
	}
	if !seen["version"] || !seen["session_id"] || !seen["type"] || !seen["sequence"] || version != 1 || id != session || sequence == 0 {
		return invalid
	}
	switch kind {
	case "update":
		if !seen["line"] || !seen["cursor"] {
			return invalid
		}
		return state.UpdateWithCWD(sequence, line, cursor, cwd)
	case "clear":
		if seen["line"] || seen["cursor"] || seen["cwd"] {
			return invalid
		}
		return state.Clear(sequence)
	default:
		return invalid
	}
}
