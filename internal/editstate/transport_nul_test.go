package editstate

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func nulRecords(fields ...string) []byte {
	if len(fields) == 5 {
		fields = append(fields, "")
	}
	var data []byte
	for _, field := range fields {
		data = append(data, field...)
		data = append(data, 0)
	}
	return data
}

func waitEditingSnapshot(t *testing.T, state *State, wantLine string, wantCursor int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, present := state.Snapshot()
		if present && snapshot.Line == wantLine && snapshot.Cursor == wantCursor {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	snapshot, present := state.Snapshot()
	t.Fatalf("snapshot = %+v present=%v, want line %q cursor %d", snapshot, present, wantLine, wantCursor)
}

func waitEditingCleared(t *testing.T, state *State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, present := state.Snapshot(); !present {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("editing state was not cleared")
}

func TestConsumeNULAppliesRecordsAndClosesOnEOF(t *testing.T) {
	reader, writer := io.Pipe()
	state := &State{}
	result := make(chan error, 1)
	go func() { result <- ConsumeNUL(reader, state) }()

	if _, err := writer.Write(append(nulRecords("begin", "1", "0", "1", ""),
		nulRecords("update", "2", "6", "1", "git co")...)); err != nil {
		t.Fatal(err)
	}
	waitEditingSnapshot(t, state, "git co", 6)

	if _, err := writer.Write(nulRecords("end", "3", "0", "1", "")); err != nil {
		t.Fatal(err)
	}
	waitEditingCleared(t, state)

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, io.EOF) {
		t.Fatalf("ConsumeNUL = %v, want EOF", err)
	}
	if _, present := state.Snapshot(); present {
		t.Fatal("state still present after EOF")
	}
}

func TestConsumeNULRejectsInvalidRecordsAndClosesState(t *testing.T) {
	cases := [][]byte{
		nulRecords("update", "0", "0", "1", ""),
		nulRecords("update", "2", "7", "1", "git"),
		nulRecords("update", "2", "0", "2", "git"),
		nulRecords("end", "2", "0", "1", "buffered"),
		nulRecords("other", "2", "0", "1", ""),
		nulRecords("update", "2", "0", "1", string([]byte{0xff})),
		nulRecords("update", "2"),
	}
	for index, input := range cases {
		state := &State{}
		err := ConsumeNUL(bytes.NewReader(input), state)
		if err == nil {
			t.Errorf("case %d accepted invalid input", index)
		}
		if _, present := state.Snapshot(); present {
			t.Errorf("case %d left state present", index)
		}
	}
}

func TestConsumeNULRejectsOversizedField(t *testing.T) {
	state := &State{}
	input := nulRecords("update", "2", "0", "1", string(bytes.Repeat([]byte("a"), maxNULField+1)))
	if err := ConsumeNUL(bytes.NewReader(input), state); err == nil {
		t.Fatal("oversized field accepted")
	}
}

func TestZLETransportScriptDefinesCallbackBeforeEnabling(t *testing.T) {
	script := ZLETransportScript()
	callback := bytes.Index([]byte(script), []byte("function _offline_complete_zle_event"))
	enable := bytes.Index([]byte(script), []byte("\n_offline_complete_zle_enable || true"))
	if callback < 0 || enable < 0 || callback > enable {
		t.Fatal("transport script order is wrong")
	}
}
