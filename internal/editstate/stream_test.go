package editstate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"offline-complete/internal/ipc"
)

func streamFrame(payload string) []byte {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	return append(header[:], []byte(payload)...)
}

func waitSnapshot(t *testing.T, state *State, version uint64, present bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := state.Snapshot()
		if ok == present && (!present || snapshot.Version == version) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("snapshot did not reach expected state")
}

type byteReader struct{ io.Reader }

func (r byteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestConsumeStreamRejectsUnencodableSessionBeforeReading(t *testing.T) {
	session := strings.Repeat("<", ipc.MaxFrame/2)
	var output bytes.Buffer
	if err := WriteClear(&output, session, 1); err == nil {
		t.Fatal("test session unexpectedly fits in a clear frame")
	}
	input := bytes.NewBuffer(streamFrame(`{"version":1}`))
	before := input.Len()
	state := new(State)
	if err := state.Update(1, "old", 3); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeStream(input, session, state); err == nil {
		t.Fatal("unencodable session was accepted")
	}
	if input.Len() != before {
		t.Fatalf("consumed %d bytes before rejecting session", before-input.Len())
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("invalid session left old snapshot available")
	}
}

func TestConsumeStreamRejectsSessionThatCannotCarryUpdateBeforeReading(t *testing.T) {
	session := strings.Repeat("a", 65536-57)
	input := bytes.NewBuffer(streamFrame(`{"version":1}`))
	before := input.Len()
	state := new(State)
	if err := state.Update(1, "old", 3); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeStream(input, session, state); err == nil {
		t.Fatal("session incapable of carrying an update was accepted")
	}
	if input.Len() != before {
		t.Fatalf("consumed %d bytes before rejecting session", before-input.Len())
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("invalid session left old snapshot available")
	}
}

func TestConsumeStreamLifecycle(t *testing.T) {
	reader, writer := io.Pipe()
	state := new(State)
	done := make(chan error, 1)
	go func() { done <- ConsumeStream(byteReader{reader}, "session-a", state) }()
	defer reader.Close()
	write := func(payload string) {
		t.Helper()
		if _, err := writer.Write(streamFrame(payload)); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":1,"session_id":"session-a","type":"update","sequence":1,"line":"git café","cursor":9}`)
	waitSnapshot(t, state, 1, true)
	if snapshot, _ := state.Snapshot(); snapshot.Line != "git café" || snapshot.Cursor != 9 {
		t.Fatalf("wrong snapshot: %+v", snapshot)
	}
	write(`{"version":1,"session_id":"session-a","type":"clear","sequence":2}`)
	waitSnapshot(t, state, 0, false)
	write(`{"version":1,"session_id":"session-a","type":"update","sequence":3,"line":"next","cursor":4}`)
	waitSnapshot(t, state, 3, true)
	writer.Close()
	if err := <-done; err == nil {
		t.Fatal("disconnect accepted without error")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("disconnect retained snapshot")
	}
	if err := state.Update(4, "late", 4); err == nil {
		t.Fatal("closed state accepted late update")
	}
}

func TestConsumeStreamRejectsInvalidMessages(t *testing.T) {
	valid := `{"version":1,"session_id":"s","type":"update","sequence":1,"line":"ok","cursor":2}`
	cases := []string{
		`{"version":1,"session_id":"other","type":"clear","sequence":2}`,
		`{"version":1,"session_id":"s","type":"clear","sequence":1}`,
		`{"version":1,"session_id":"s","type":"clear","sequence":2,"line":"secret"}`,
		`{"version":1,"session_id":"s","type":"update","sequence":2,"line":"é","cursor":1}`,
		`{"version":1,"version":1,"session_id":"s","type":"clear","sequence":2}`,
		`{"version":1,"session_id":"s","type":"clear","sequence":null}`,
		`{"version":1,"session_id":"s","type":"clear"}`,
		`{"version":1,"session_id":"s","type":"unknown","sequence":2}`,
		`{"version":1,"session_id":"s","type":"update","sequence":2,"line":"\ud800","cursor":0}`,
		`{"version":2,"session_id":"s","type":"clear","sequence":2}`,
		`{"version":1,"session_id":"s","type":"clear","sequence":2,"extra":0}`,
		`{"Version":1,"session_id":"s","type":"clear","sequence":2}`,
		`{"version":1,"session_id":"s","type":"update","sequence":2,"line":"text"}`,
		`{"version":1,"session_id":"s","type":"clear","sequence":2} {}`,
		"{\"version\":1,\"session_id\":\"s\",\"type\":\"update\",\"sequence\":2,\"line\":\"\xff\",\"cursor\":0}",
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			state := new(State)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() { done <- ConsumeStream(reader, "s", state) }()
			if _, err := writer.Write(append(streamFrame(valid), streamFrame(bad)...)); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("accepted invalid message")
				}
			case <-time.After(time.Second):
				t.Fatal("invalid message did not terminate stream")
			}
			if _, ok := state.Snapshot(); ok {
				t.Fatal("invalid stream retained state")
			}
			if err := state.Update(3, "late", 4); err == nil {
				t.Fatal("state remained open")
			}
		})
	}
}

func TestConsumeConnPartialFrameTimeoutClearsSnapshot(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	state := new(State)
	done := make(chan error, 1)
	go func() { done <- ConsumeConn(server, "s", state, 150*time.Millisecond) }()
	if _, err := client.Write(streamFrame(`{"version":1,"session_id":"s","type":"update","sequence":1,"line":"ok","cursor":2}`)); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, state, 1, true)
	if _, err := client.Write([]byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("expected timeout, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("partial frame did not time out")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("timeout retained snapshot")
	}
	if err := state.Update(2, "late", 4); err == nil {
		t.Fatal("timed-out state remained open")
	}
}

func TestConsumeConnRejectsZeroTimeout(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	state := new(State)
	if err := state.Update(1, "old", 3); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeConn(server, "s", state, 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
	if _, ok := state.Snapshot(); ok {
		t.Fatal("invalid timeout retained snapshot")
	}
}

func TestConsumeStreamRejectsOversizedAndTruncatedFrame(t *testing.T) {
	for _, data := range [][]byte{{0, 1, 0, 1}, {0, 0, 0, 10, '{'}, streamFrame(strings.Repeat("x", 65537))} {
		state := new(State)
		if err := state.Update(1, "old", 3); err != nil {
			t.Fatal(err)
		}
		if err := ConsumeStream(bytes.NewReader(data), "s", state); err == nil {
			t.Fatal("accepted invalid frame")
		}
		if _, ok := state.Snapshot(); ok {
			t.Fatal("invalid frame retained state")
		}
	}
}
