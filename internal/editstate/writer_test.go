package editstate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestWriteEditingFramesRoundTrip(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	state := new(State)
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(r, "session", state) }()
	if err := WriteUpdate(w, "session", 1, "git café", 9); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, state, 1, true)
	if s, ok := state.Snapshot(); !ok || s.Line != "git café" || s.Cursor != 9 {
		t.Fatalf("wrong snapshot: %+v", s)
	}
	if err := WriteClear(w, "session", 2); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, state, 0, false)
	w.Close()
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatalf("disconnect: %v", err)
	}
}

func TestWriteEditingFramesRejectsInvalidWithoutWriting(t *testing.T) {
	cases := []struct {
		session  string
		sequence uint64
		line     string
		cursor   int
	}{
		{"", 1, "ok", 2}, {"\xff", 1, "ok", 2}, {"s", 0, "ok", 2},
		{"s", 1, "\xff", 0}, {"s", 1, "é", 1}, {"s", 1, "ok", 3},
		{"s", 1, strings.Repeat("x", 65537), 0},
		{"s", 1, strings.Repeat("\x00", 12000), 0},
		{strings.Repeat("x", 65537), 1, "ok", 0},
	}
	for _, tc := range cases {
		var out bytes.Buffer
		if err := WriteUpdate(&out, tc.session, tc.sequence, tc.line, tc.cursor); err == nil {
			t.Fatal("accepted invalid update")
		}
		if out.Len() != 0 {
			t.Fatal("invalid update wrote bytes")
		}
	}
	var out bytes.Buffer
	if err := WriteClear(&out, "", 1); err == nil || out.Len() != 0 {
		t.Fatal("accepted invalid clear")
	}
	if err := WriteClear(&out, "s", 0); err == nil || out.Len() != 0 {
		t.Fatal("accepted zero sequence")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestWriteEditingFramesReportsShortWrite(t *testing.T) {
	if err := WriteClear(shortWriter{}, "s", 1); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
}

func TestWriteEditingConnDeadline(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	start := time.Now()
	err := WriteUpdateConn(client, "s", 1, "hello", 5, 40*time.Millisecond)
	var nerr net.Error
	if !errors.As(err, &nerr) || !nerr.Timeout() || time.Since(start) > time.Second {
		t.Fatalf("blocked write: %v", err)
	}
}

func TestWriteEditingConnClearsDeadline(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	received := make(chan error, 1)
	go func() {
		var header [4]byte
		_, err := io.ReadFull(peer, header[:])
		if err == nil {
			_, err = io.CopyN(io.Discard, peer, int64(binary.BigEndian.Uint32(header[:])))
		}
		received <- err
	}()
	if err := WriteClearConn(client, "s", 1, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := client.Write([]byte("x")); done <- err }()
	var b [1]byte
	if _, err := io.ReadFull(peer, b[:]); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil || b[0] != 'x' {
		t.Fatalf("deadline left behind: %v", err)
	}
}

func TestWriteEditingConnRejectsInvalidTimeout(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	if err := WriteClearConn(client, "s", 1, 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
	if err := WriteUpdateConn(nil, "s", 1, "", 0, time.Second); err == nil {
		t.Fatal("accepted nil connection")
	}
}

type deadlineFailureConn struct {
	net.Conn
	calls  int
	failAt int
}

var errDeadline = errors.New("deadline failure")

func (c *deadlineFailureConn) SetWriteDeadline(t time.Time) error {
	c.calls++
	if c.calls == c.failAt {
		return errDeadline
	}
	return c.Conn.SetWriteDeadline(t)
}
func TestWriteEditingConnReportsDeadlineErrors(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			conn := &deadlineFailureConn{Conn: client, failAt: failAt}
			if failAt == 2 {
				go io.Copy(io.Discard, peer)
			}
			if err := WriteClearConn(conn, "s", 1, time.Second); !errors.Is(err, errDeadline) {
				t.Fatalf("deadline error: %v", err)
			}
		})
	}
}
