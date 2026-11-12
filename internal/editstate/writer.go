package editstate

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"offline-complete/internal/ipc"
)

// WriteUpdate writes one editing snapshot. The caller must authenticate the
// connection, serialize writes, maintain increasing sequences, and enforce a
// deadline. After any write error the stream must be closed, as a partial frame
// may have been sent. This function does not erase the encoded message.
func WriteUpdate(w io.Writer, session string, sequence uint64, line string, cursor int) error {
	return WriteUpdateWithCWD(w, session, sequence, line, cursor, "")
}

// WriteUpdateWithCWD writes an update carrying the shell's working directory.
func WriteUpdateWithCWD(w io.Writer, session string, sequence uint64, line string, cursor int, cwd string) error {
	if len(line) > 64*1024 || !utf8.ValidString(line) || cursor < 0 || cursor > len(line) || (cursor < len(line) && !utf8.RuneStart(line[cursor])) ||
		len(cwd) > 4096 || !utf8.ValidString(cwd) || strings.IndexFunc(cwd, unicode.IsControl) >= 0 {
		return errors.New("invalid editing snapshot")
	}
	var cwdValue *string
	if cwd != "" {
		cwdValue = &cwd
	}
	return writeMessage(w, session, sequence, "update", &line, &cursor, cwdValue)
}

// WriteClear writes one versioned invalidation. It has the same connection and
// error handling requirements as WriteUpdate.
func WriteClear(w io.Writer, session string, sequence uint64) error {
	return writeMessage(w, session, sequence, "clear", nil, nil, nil)
}

// WriteUpdateConn writes one frame with a total write deadline. The caller owns
// authentication, serialization and connection closure after any error. The
// connection's write deadline is overwritten and cleared, not restored.
func WriteUpdateConn(conn net.Conn, session string, sequence uint64, line string, cursor int, timeout time.Duration) error {
	return writeConn(conn, timeout, func() error { return WriteUpdate(conn, session, sequence, line, cursor) })
}

// WriteClearConn writes an invalidation with the same deadline and ownership
// requirements as WriteUpdateConn.
func WriteClearConn(conn net.Conn, session string, sequence uint64, timeout time.Duration) error {
	return writeConn(conn, timeout, func() error { return WriteClear(conn, session, sequence) })
}

func writeConn(conn net.Conn, timeout time.Duration, write func() error) (err error) {
	if conn == nil || timeout <= 0 {
		return errors.New("invalid editing write deadline")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.SetWriteDeadline(time.Time{})) }()
	return write()
}

func writeMessage(w io.Writer, session string, sequence uint64, kind string, line *string, cursor *int, cwd *string) error {
	if w == nil || session == "" || len(session) > ipc.MaxFrame || !utf8.ValidString(session) || sequence == 0 {
		return errors.New("invalid editing message arguments")
	}
	message := struct {
		Version  int     `json:"version"`
		Session  string  `json:"session_id"`
		Type     string  `json:"type"`
		Sequence uint64  `json:"sequence"`
		Line     *string `json:"line,omitempty"`
		Cursor   *int    `json:"cursor,omitempty"`
		CWD      *string `json:"cwd,omitempty"`
	}{1, session, kind, sequence, line, cursor, cwd}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(payload) > ipc.MaxFrame {
		return errors.New("editing frame too large")
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	n, err := w.Write(frame)
	if err != nil {
		return err
	}
	if n != len(frame) {
		return io.ErrShortWrite
	}
	return nil
}
