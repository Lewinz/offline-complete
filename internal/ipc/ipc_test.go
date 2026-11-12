package ipc_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"

	"offline-complete/internal/ipc"
)

func TestHandshakeRejectsNilIO(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		if _, err := ipc.ReadHandshake(nil, "session"); err == nil {
			t.Error("nil handshake reader accepted")
		}
	})
	t.Run("writer", func(t *testing.T) {
		if err := ipc.WriteHandshake(nil, ipc.Handshake{Version: 1, SessionID: "session"}); err == nil {
			t.Error("nil handshake writer accepted")
		}
	})
}

func TestOversizedSessionRejectedWithoutLargeAllocation(t *testing.T) {
	message := ipc.Handshake{Version: 1, SessionID: strings.Repeat("a", 4*1024*1024)}
	var output bytes.Buffer
	if err := ipc.WriteHandshake(&output, message); err == nil || output.Len() != 0 {
		t.Fatalf("oversized handshake: error = %v, bytes written = %d", err, output.Len())
	}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = ipc.WriteHandshake(io.Discard, message)
		}
	})
	if allocated := result.AllocedBytesPerOp(); allocated > 512*1024 {
		t.Fatalf("oversized handshake allocated %d bytes per call", allocated)
	}
}

func TestReadHandshakeRejectsInvalidExpectedSessionBeforeReading(t *testing.T) {
	for _, session := range []string{"", "\xff", strings.Repeat("a", ipc.MaxFrame+1), strings.Repeat("\x01", ipc.MaxFrame/2)} {
		reader := bytes.NewReader(frameOf([]byte(`{"version":1,"session_id":"valid"}`)))
		before := reader.Len()
		if _, err := ipc.ReadHandshake(reader, session); err == nil {
			t.Fatal("accepted invalid expected session")
		}
		if reader.Len() != before {
			t.Fatalf("invalid expected session consumed %d bytes", before-reader.Len())
		}
	}
}

func TestHandshakeRoundTripsWithShortReadsAndWrites(t *testing.T) {
	expected := ipc.Handshake{Version: 1, SessionID: "0123456789abcdef0123456789abcdef"}
	var writer shortWriter
	if err := ipc.WriteHandshake(&writer, expected); err != nil {
		t.Fatalf("WriteHandshake: %v", err)
	}
	reader := shortReader{data: writer.data}
	got, err := ipc.ReadHandshake(&reader, expected.SessionID)
	if err != nil {
		t.Fatalf("ReadHandshake: %v", err)
	}
	if got != expected {
		t.Fatalf("handshake = %+v, want %+v", got, expected)
	}
}

func TestRejectsInvalidHandshakesAndFrames(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	payloads := []string{
		`{"version":2,"session_id":"0123456789abcdef0123456789abcdef"}`,
		`{"version":1,"session_id":"wrong"}`,
		`{"version":1,"session_id":"0123456789abcdef0123456789abcdef","extra":1}`,
		`{broken`,
	}
	for _, payload := range payloads {
		frame := frameOf([]byte(payload))
		if _, err := ipc.ReadHandshake(bytes.NewReader(frame), id); err == nil {
			t.Errorf("accepted payload %q", payload)
		}
	}

	frames := [][]byte{
		{0, 0, 0, 0},
		{0, 1, 0, 1},
		{0, 0, 0, 3, '{'},
		{0, 0, 0, 1, 0xff},
	}
	for _, frame := range frames {
		if _, err := ipc.ReadHandshake(bytes.NewReader(frame), id); err == nil {
			t.Errorf("accepted frame %v", frame)
		}
	}
}

func TestRejectsAmbiguousHandshakeFields(t *testing.T) {
	for _, payload := range []string{
		`{"version":2,"version":1,"session_id":"session"}`,
		`{"version":1,"session_id":"other","session_id":"session"}`,
		`{"Version":1,"session_id":"session"}`,
		`{"version":1,"session_id":"session","SESSION_ID":"session"}`,
		`{"version":1,"session_id":"session","session_id":null}`,
	} {
		if _, err := ipc.ReadHandshake(bytes.NewReader(frameOf([]byte(payload))), "session"); err == nil {
			t.Errorf("accepted ambiguous handshake %s", payload)
		}
	}
}

func TestRejectsHandshakeWithEmptySessionID(t *testing.T) {
	frame := frameOf([]byte(`{"version":1,"session_id":""}`))
	if _, err := ipc.ReadHandshake(bytes.NewReader(frame), ""); err == nil {
		t.Fatal("accepted empty session ID")
	}
}

func TestRejectsInvalidUTF8InsideJSONString(t *testing.T) {
	payload := append([]byte(`{"version":1,"session_id":"`), 0xff)
	payload = append(payload, []byte(`"}`)...)
	if _, err := ipc.ReadHandshake(bytes.NewReader(frameOf(payload)), "�"); err == nil {
		t.Fatal("accepted invalid UTF-8 in handshake")
	}
}

func TestRejectsUnpairedSurrogatesInHandshake(t *testing.T) {
	for _, tc := range []struct{ payload, expected string }{
		{`{"version":1,"session_id":"\ud800"}`, "�"},
		{`{"version":1,"session_id":"\udc00"}`, "�"},
		{`{"version":1,"session_id":"\ud800A"}`, "�A"},
	} {
		if _, err := ipc.ReadHandshake(bytes.NewReader(frameOf([]byte(tc.payload))), tc.expected); err == nil {
			t.Errorf("accepted unpaired surrogate: %s", tc.payload)
		}
	}
}

func TestAcceptsValidUnicodeAndLiteralEscape(t *testing.T) {
	for _, tc := range []struct{ payload, expected string }{
		{`{"version":1,"session_id":"😀"}`, "😀"},
		{`{"version":1,"session_id":"\\ud800"}`, `\ud800`},
	} {
		if _, err := ipc.ReadHandshake(bytes.NewReader(frameOf([]byte(tc.payload))), tc.expected); err != nil {
			t.Errorf("rejected valid handshake %s: %v", tc.payload, err)
		}
	}
}

func TestWriteHandshakeRejectsInvalidVersionAndEmptySessionWithoutWriting(t *testing.T) {
	for _, message := range []ipc.Handshake{
		{Version: 0, SessionID: "session"},
		{Version: 2, SessionID: "session"},
		{Version: 1, SessionID: ""},
	} {
		var output bytes.Buffer
		if err := ipc.WriteHandshake(&output, message); err == nil {
			t.Errorf("accepted invalid handshake %+v", message)
		}
		if output.Len() != 0 {
			t.Errorf("wrote %d bytes for invalid handshake %+v", output.Len(), message)
		}
	}
}

func TestWriteHandshakeRejectsInvalidUTF8WithoutWriting(t *testing.T) {
	var output bytes.Buffer
	err := ipc.WriteHandshake(&output, ipc.Handshake{Version: 1, SessionID: string([]byte{0xff})})
	if err == nil {
		t.Fatal("accepted invalid UTF-8 session ID")
	}
	if output.Len() != 0 {
		t.Fatalf("wrote %d bytes for invalid handshake", output.Len())
	}
}

func frameOf(payload []byte) []byte {
	frame := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	return append(frame, payload...)
}

type shortReader struct {
	data []byte
}

func (r *shortReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:1], r.data)
	r.data = r.data[n:]
	return n, nil
}

type shortWriter struct {
	data []byte
}

func (w *shortWriter) Write(p []byte) (int, error) {
	n := 1
	if len(p) < n {
		n = len(p)
	}
	w.data = append(w.data, p[:n]...)
	return n, nil
}
